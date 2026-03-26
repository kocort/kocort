package tool

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"

	"github.com/kocort/kocort/internal/config"
	"github.com/kocort/kocort/internal/core"
)

type GatewayTool struct{}

func NewGatewayTool() *GatewayTool { return &GatewayTool{} }

func (t *GatewayTool) Name() string { return "gateway" }

func (t *GatewayTool) Description() string {
	return "Restart, inspect config, apply config, or patch config on the running service."
}

func (t *GatewayTool) OpenAIFunctionTool() *core.OpenAIFunctionToolSchema {
	return &core.OpenAIFunctionToolSchema{
		Name:        t.Name(),
		Description: t.Description(),
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"action": map[string]any{
					"type":        "string",
					"description": "One of restart, config.get, config.schema.lookup, config.apply, config.patch, update.run.",
				},
				"path": map[string]any{
					"type":        "string",
					"description": "Dot path for config.schema.lookup.",
				},
				"raw": map[string]any{
					"type":        "string",
					"description": "JSON payload for config.apply or config.patch.",
				},
				"baseHash": map[string]any{
					"type":        "string",
					"description": "Optional optimistic concurrency hash from config.get.",
				},
				"note": map[string]any{
					"type":        "string",
					"description": "Optional human-readable note for the action result.",
				},
			},
			"required":             []string{"action"},
			"additionalProperties": false,
		},
	}
}

type gatewayRuntime interface {
	GatewayConfigSnapshot() (config.AppConfig, string, error)
	GatewayApplyConfig(context.Context, config.AppConfig) error
	GatewayPersistConfig(context.Context, bool, bool, bool) error
}

func (t *GatewayTool) Execute(ctx context.Context, toolCtx ToolContext, args map[string]any) (core.ToolResult, error) {
	runtime, ok := toolCtx.Runtime.(gatewayRuntime)
	if !ok {
		return core.ToolResult{}, fmt.Errorf("gateway control is not available in this runtime")
	}
	action, err := ReadStringParam(args, "action", true)
	if err != nil {
		return core.ToolResult{}, err
	}
	cfg, hash, err := runtime.GatewayConfigSnapshot()
	if err != nil {
		return core.ToolResult{}, err
	}
	switch strings.TrimSpace(action) {
	case "restart":
		if err := runtime.GatewayApplyConfig(ctx, cfg); err != nil {
			return core.ToolResult{}, err
		}
		return JSONResult(map[string]any{
			"status": "ok",
			"action": "restart",
			"mode":   "hot-reload",
			"hash":   hash,
			"note":   strings.TrimSpace(mustReadString(args, "note")),
		})
	case "config.get":
		raw, err := json.MarshalIndent(cfg, "", "  ")
		if err != nil {
			return core.ToolResult{}, err
		}
		return JSONResult(map[string]any{
			"status": "ok",
			"action": "config.get",
			"hash":   hash,
			"config": json.RawMessage(raw),
		})
	case "config.schema.lookup":
		path, err := ReadStringParam(args, "path", true)
		if err != nil {
			return core.ToolResult{}, err
		}
		info, err := lookupConfigSchema(cfg, path)
		if err != nil {
			return JSONResult(map[string]any{
				"status": "error",
				"action": "config.schema.lookup",
				"path":   path,
				"error":  err.Error(),
			})
		}
		return JSONResult(map[string]any{
			"status": "ok",
			"action": "config.schema.lookup",
			"hash":   hash,
			"lookup": info,
		})
	case "config.apply":
		raw, err := ReadStringParam(args, "raw", true)
		if err != nil {
			return core.ToolResult{}, err
		}
		if err := checkGatewayBaseHash(args, hash); err != nil {
			return JSONResult(map[string]any{"status": "error", "action": "config.apply", "error": err.Error(), "hash": hash})
		}
		var next config.AppConfig
		if err := json.Unmarshal([]byte(raw), &next); err != nil {
			return JSONResult(map[string]any{"status": "error", "action": "config.apply", "error": err.Error()})
		}
		if err := runtime.GatewayApplyConfig(ctx, next); err != nil {
			return core.ToolResult{}, err
		}
		if err := runtime.GatewayPersistConfig(ctx, true, true, true); err != nil {
			return core.ToolResult{}, err
		}
		nextHash, _ := configHash(next)
		return JSONResult(map[string]any{"status": "ok", "action": "config.apply", "hash": nextHash})
	case "config.patch":
		raw, err := ReadStringParam(args, "raw", true)
		if err != nil {
			return core.ToolResult{}, err
		}
		if err := checkGatewayBaseHash(args, hash); err != nil {
			return JSONResult(map[string]any{"status": "error", "action": "config.patch", "error": err.Error(), "hash": hash})
		}
		next, err := patchConfig(cfg, []byte(raw))
		if err != nil {
			return JSONResult(map[string]any{"status": "error", "action": "config.patch", "error": err.Error()})
		}
		if err := runtime.GatewayApplyConfig(ctx, next); err != nil {
			return core.ToolResult{}, err
		}
		if err := runtime.GatewayPersistConfig(ctx, true, true, true); err != nil {
			return core.ToolResult{}, err
		}
		nextHash, _ := configHash(next)
		return JSONResult(map[string]any{"status": "ok", "action": "config.patch", "hash": nextHash})
	case "update.run":
		return JSONResult(map[string]any{
			"status": "unavailable",
			"action": "update.run",
			"error":  "update.run is not implemented in this runtime",
		})
	default:
		return JSONResult(map[string]any{"status": "error", "error": fmt.Sprintf("unsupported action %q", action)})
	}
}

func mustReadString(args map[string]any, key string) string {
	value, _ := ReadStringParam(args, key, false)
	return value
}

func checkGatewayBaseHash(args map[string]any, current string) error {
	baseHash, _ := ReadStringParam(args, "baseHash", false)
	if strings.TrimSpace(baseHash) == "" {
		return nil
	}
	if strings.TrimSpace(baseHash) != strings.TrimSpace(current) {
		return fmt.Errorf("baseHash mismatch: current=%s", current)
	}
	return nil
}

func patchConfig(current config.AppConfig, raw []byte) (config.AppConfig, error) {
	baseBytes, err := json.Marshal(current)
	if err != nil {
		return config.AppConfig{}, err
	}
	var base map[string]any
	if err := json.Unmarshal(baseBytes, &base); err != nil {
		return config.AppConfig{}, err
	}
	var patch map[string]any
	if err := json.Unmarshal(raw, &patch); err != nil {
		return config.AppConfig{}, err
	}
	mergeMaps(base, patch)
	mergedBytes, err := json.Marshal(base)
	if err != nil {
		return config.AppConfig{}, err
	}
	var next config.AppConfig
	if err := json.Unmarshal(mergedBytes, &next); err != nil {
		return config.AppConfig{}, err
	}
	return next, nil
}

func mergeMaps(dst map[string]any, src map[string]any) {
	for key, value := range src {
		existing, exists := dst[key]
		nextMap, nextIsMap := value.(map[string]any)
		existingMap, existingIsMap := existing.(map[string]any)
		if exists && nextIsMap && existingIsMap {
			mergeMaps(existingMap, nextMap)
			dst[key] = existingMap
			continue
		}
		dst[key] = value
	}
}

func configHash(cfg config.AppConfig) (string, error) {
	raw, err := json.Marshal(cfg)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}

func lookupConfigSchema(cfg config.AppConfig, path string) (map[string]any, error) {
	raw, err := json.Marshal(cfg)
	if err != nil {
		return nil, err
	}
	var root any
	if err := json.Unmarshal(raw, &root); err != nil {
		return nil, err
	}
	value, exists := walkJSONPath(root, strings.Split(strings.TrimSpace(path), "."))
	info := map[string]any{
		"path":   strings.TrimSpace(path),
		"exists": exists,
		"type":   inferSchemaTypeFromStruct(reflect.TypeOf(config.AppConfig{}), strings.Split(strings.TrimSpace(path), ".")),
	}
	if exists {
		info["value"] = value
		info["valueType"] = inferValueType(value)
	}
	return info, nil
}

func walkJSONPath(root any, parts []string) (any, bool) {
	current := root
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		obj, ok := current.(map[string]any)
		if !ok {
			return nil, false
		}
		next, ok := obj[part]
		if !ok {
			return nil, false
		}
		current = next
	}
	return current, true
}

func inferSchemaTypeFromStruct(t reflect.Type, parts []string) string {
	current := t
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		for current.Kind() == reflect.Pointer {
			current = current.Elem()
		}
		if current.Kind() == reflect.Map {
			return "map"
		}
		if current.Kind() != reflect.Struct {
			return current.String()
		}
		found := false
		for i := 0; i < current.NumField(); i++ {
			field := current.Field(i)
			tag := strings.Split(field.Tag.Get("json"), ",")[0]
			if tag == part {
				current = field.Type
				found = true
				break
			}
		}
		if !found {
			return ""
		}
	}
	for current.Kind() == reflect.Pointer {
		current = current.Elem()
	}
	return current.String()
}

func inferValueType(value any) string {
	switch value.(type) {
	case map[string]any:
		return "object"
	case []any:
		return "array"
	case string:
		return "string"
	case bool:
		return "boolean"
	case float64:
		return "number"
	case nil:
		return "null"
	default:
		return fmt.Sprintf("%T", value)
	}
}
