package llamawrapper

import (
	"encoding/json"
	"encoding/xml"
	"fmt"
	"math"
	"math/rand"
	"regexp"
	"strconv"
	"strings"
)

const (
	toolCallOpen  = "<tool_call>"
	toolCallClose = "</tool_call>"
)

// ── Tool call JSON parsing (Qwen3) ──────────────────────────────────────────

// parseToolCallJSON parses a Qwen3-style JSON tool call block.
// Expected format: {"name": "function_name", "arguments": {"key": "value", ...}}
func parseToolCallJSON(raw string, tools []Tool) (ToolCall, error) {
	raw = strings.TrimSpace(raw)

	var parsed struct {
		Name      string         `json:"name"`
		Arguments map[string]any `json:"arguments"`
	}

	if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
		return ToolCall{}, fmt.Errorf("failed to parse qwen3 tool call JSON: %w", err)
	}

	if parsed.Name == "" {
		return ToolCall{}, fmt.Errorf("empty function name in qwen3 tool call")
	}

	argsBytes, err := json.Marshal(parsed.Arguments)
	if err != nil {
		return ToolCall{}, fmt.Errorf("failed to marshal qwen3 tool call arguments: %w", err)
	}

	return ToolCall{
		ID:   genToolCallID(),
		Type: "function",
		Function: ToolFunction{
			Name:      parsed.Name,
			Arguments: string(argsBytes),
		},
	}, nil
}

// ── Tool call XML parsing (Qwen3.5) ─────────────────────────────────────────

type xmlFuncCall struct {
	XMLName    xml.Name       `xml:"function"`
	Name       string         `xml:"name,attr"`
	Parameters []xmlFuncParam `xml:"parameter"`
}

type xmlFuncParam struct {
	Name  string `xml:"name,attr"`
	Value string `xml:",chardata"`
}

var (
	qwenTagRe    = regexp.MustCompile(`<(\w+)=([^>]+)>`)
	qwenXMLTagRe = regexp.MustCompile(`</?(?:function|parameter)(?:\s+name="[^"]*")?>`)
)

// parseToolCallXML parses a Qwen-style tool call block into a ToolCall.
func parseToolCallXML(raw string, tools []Tool) (ToolCall, error) {
	xmlStr := transformQwenToXML(raw)

	var fc xmlFuncCall
	if err := xml.Unmarshal([]byte(xmlStr), &fc); err != nil {
		return ToolCall{}, err
	}

	tc := ToolCall{
		ID:   genToolCallID(),
		Type: "function",
		Function: ToolFunction{
			Name: fc.Name,
		},
	}

	args := make(map[string]any, len(fc.Parameters))
	for _, p := range fc.Parameters {
		args[p.Name] = parseTypedValue(p.Value, lookupParamTypes(tools, fc.Name, p.Name))
	}

	b, err := json.Marshal(args)
	if err != nil {
		return ToolCall{}, err
	}
	tc.Function.Arguments = string(b)
	return tc, nil
}

func transformQwenToXML(raw string) string {
	transformed := qwenTagRe.ReplaceAllStringFunc(raw, func(match string) string {
		groups := qwenTagRe.FindStringSubmatch(match)
		tag := groups[1]
		var escaped strings.Builder
		_ = xml.EscapeText(&escaped, []byte(groups[2]))
		return fmt.Sprintf(`<%s name="%s">`, tag, escaped.String())
	})

	var out strings.Builder
	lastIdx := 0
	for _, loc := range qwenXMLTagRe.FindAllStringIndex(transformed, -1) {
		if loc[0] > lastIdx {
			escapeXMLText(&out, transformed[lastIdx:loc[0]])
		}
		out.WriteString(transformed[loc[0]:loc[1]])
		lastIdx = loc[1]
	}
	if lastIdx < len(transformed) {
		escapeXMLText(&out, transformed[lastIdx:])
	}
	return out.String()
}

func escapeXMLText(sb *strings.Builder, s string) {
	for _, r := range s {
		switch r {
		case '&':
			sb.WriteString("&amp;")
		case '<':
			sb.WriteString("&lt;")
		case '>':
			sb.WriteString("&gt;")
		default:
			sb.WriteRune(r)
		}
	}
}

func genToolCallID() string {
	const chars = "abcdefghijklmnopqrstuvwxyz0123456789"
	b := make([]byte, 8)
	for i := range b {
		b[i] = chars[rand.Intn(len(chars))]
	}
	return "call_" + string(b)
}

// ── Type coercion for tool parameters ────────────────────────────────────────

type toolSchema struct {
	Properties map[string]toolProp `json:"properties"`
}

type toolProp struct {
	Type  string     `json:"type"`
	AnyOf []toolProp `json:"anyOf"`
}

func lookupParamTypes(tools []Tool, funcName, paramName string) []string {
	for _, tool := range tools {
		if tool.Function.Name == funcName {
			return propTypes(tool, paramName)
		}
	}
	return nil
}

func propTypes(tool Tool, name string) []string {
	var schema toolSchema
	if err := json.Unmarshal(tool.Function.Parameters, &schema); err != nil {
		return nil
	}
	prop, ok := schema.Properties[name]
	if !ok {
		return nil
	}
	if len(prop.AnyOf) > 0 {
		var types []string
		for _, a := range prop.AnyOf {
			if a.Type != "" {
				types = append(types, a.Type)
			}
		}
		return types
	}
	if prop.Type == "" {
		return nil
	}
	return []string{prop.Type}
}

func parseTypedValue(raw string, paramTypes []string) any {
	raw = strings.TrimPrefix(raw, "\n")
	raw = strings.TrimSuffix(raw, "\n")

	if strings.EqualFold(raw, "null") {
		return nil
	}
	if len(paramTypes) == 0 {
		return raw
	}

	typeSet := make(map[string]bool, len(paramTypes))
	for _, t := range paramTypes {
		typeSet[t] = true
	}

	if typeSet["boolean"] {
		switch strings.ToLower(raw) {
		case "true":
			return true
		case "false":
			return false
		}
		if len(paramTypes) == 1 {
			return false
		}
	}

	if typeSet["integer"] {
		if i, err := strconv.ParseInt(raw, 10, 64); err == nil {
			if i >= math.MinInt32 && i <= math.MaxInt32 {
				return int(i)
			}
			return i
		}
		if len(paramTypes) == 1 {
			return raw
		}
	}

	if typeSet["number"] {
		if f, err := strconv.ParseFloat(raw, 64); err == nil {
			if f == math.Trunc(f) {
				i := int64(f)
				if i >= math.MinInt32 && i <= math.MaxInt32 {
					return int(i)
				}
				return i
			}
			return f
		}
		if len(paramTypes) == 1 {
			return raw
		}
	}

	if typeSet["array"] {
		var arr []any
		if err := json.Unmarshal([]byte(raw), &arr); err == nil {
			return arr
		}
		if len(paramTypes) == 1 {
			return raw
		}
	}

	if typeSet["object"] {
		var obj map[string]any
		if err := json.Unmarshal([]byte(raw), &obj); err == nil {
			return obj
		}
		if len(paramTypes) == 1 {
			return raw
		}
	}

	return raw
}
