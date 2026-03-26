package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/kocort/kocort/internal/config"
	"github.com/kocort/kocort/internal/core"
)

type qmdManagedCollection struct {
	Name    string
	Path    string
	Pattern string
}

func (b *QMDMemoryBackend) InvalidatePrepared(identity core.AgentIdentity, session core.SessionResolution) {
	collections := resolveQMDManagedCollections(identity, session, b.Config)
	if len(collections) == 0 {
		return
	}
	key := buildQMDReadyKey(identity, collections)
	b.mu.Lock()
	delete(b.ready, key)
	b.mu.Unlock()
}

func (b *QMDMemoryBackend) EnsurePrepared(ctx context.Context, identity core.AgentIdentity, session core.SessionResolution) error {
	if includeSessionTranscriptSource(identity) {
		if _, err := EnsureSessionTranscriptExport(identity.WorkspaceDir, session); err != nil {
			return err
		}
	}
	collections := resolveQMDManagedCollections(identity, session, b.Config)
	if len(collections) == 0 {
		return nil
	}
	command := QMDMemoryCommand(b.Config)
	if strings.TrimSpace(command) == "" {
		return nil
	}
	key := buildQMDReadyKey(identity, collections)
	b.mu.Lock()
	if _, ok := b.ready[key]; ok {
		b.mu.Unlock()
		return nil
	}
	b.mu.Unlock()

	existing, _ := b.listCollections(ctx, command, identity.WorkspaceDir)
	for _, collection := range collections {
		if _, ok := existing[collection.Name]; ok {
			continue
		}
		if err := os.MkdirAll(collection.Path, 0o755); err != nil {
			return err
		}
		if _, err := runQMDSearchCommand(ctx, command, identity.WorkspaceDir, []string{
			"collection", "add", collection.Path, "--name", collection.Name, "--mask", collection.Pattern,
		}); err != nil {
			return err
		}
	}
	b.mu.Lock()
	b.ready[key] = struct{}{}
	b.mu.Unlock()
	return nil
}

func (b *QMDMemoryBackend) listCollections(ctx context.Context, command, workspaceDir string) (map[string]struct{}, error) {
	output, err := runQMDSearchCommand(ctx, command, workspaceDir, []string{"collection", "list", "--json"})
	if err != nil && strings.TrimSpace(output) == "" {
		return map[string]struct{}{}, err
	}
	result := map[string]struct{}{}
	payload := extractFirstJSONArray(strings.TrimSpace(output))
	if payload == "" {
		payload = strings.TrimSpace(output)
	}
	if payload == "" {
		return result, nil
	}
	var raw []map[string]any
	if err := json.Unmarshal([]byte(payload), &raw); err == nil {
		for _, entry := range raw {
			if name, ok := entry["name"].(string); ok && strings.TrimSpace(name) != "" {
				result[strings.TrimSpace(name)] = struct{}{}
			}
		}
		return result, nil
	}
	var names []string
	if err := json.Unmarshal([]byte(payload), &names); err == nil {
		for _, name := range names {
			if strings.TrimSpace(name) != "" {
				result[strings.TrimSpace(name)] = struct{}{}
			}
		}
	}
	return result, nil
}

func resolveQMDManagedCollections(identity core.AgentIdentity, session core.SessionResolution, cfg config.AppConfig) []qmdManagedCollection {
	workspaceDir := strings.TrimSpace(identity.WorkspaceDir)
	agentID := strings.TrimSpace(identity.ID)
	seen := map[string]struct{}{}
	var collections []qmdManagedCollection
	if cfg.Memory.QMD != nil {
		for _, entry := range cfg.Memory.QMD.Paths {
			path := strings.TrimSpace(entry.Path)
			name := strings.TrimSpace(entry.Name)
			pattern := strings.TrimSpace(entry.Pattern)
			if path == "" || name == "" {
				continue
			}
			if pattern == "" {
				pattern = "**/*.md"
			}
			if !filepath.IsAbs(path) {
				path = filepath.Join(workspaceDir, path)
			}
			scopedName := name
			if agentID != "" {
				scopedName = fmt.Sprintf("%s-%s", name, agentID)
			}
			key := scopedName + "\x00" + path + "\x00" + pattern
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			collections = append(collections, qmdManagedCollection{
				Name:    scopedName,
				Path:    filepath.Clean(path),
				Pattern: pattern,
			})
		}
	}
	if includeSessionTranscriptSource(identity) && strings.TrimSpace(session.SessionID) != "" {
		path := filepath.Join(workspaceDir, "memory", ".sessions")
		name := "sessions"
		if agentID != "" {
			name = fmt.Sprintf("%s-%s", name, agentID)
		}
		key := name + "\x00" + path + "\x00*.md"
		if _, ok := seen[key]; !ok {
			seen[key] = struct{}{}
			collections = append(collections, qmdManagedCollection{
				Name:    name,
				Path:    filepath.Clean(path),
				Pattern: "*.md",
			})
		}
	}
	return collections
}

func buildQMDReadyKey(identity core.AgentIdentity, collections []qmdManagedCollection) string {
	parts := []string{identity.ID, identity.WorkspaceDir}
	for _, collection := range collections {
		parts = append(parts, collection.Name, collection.Path, collection.Pattern)
	}
	return strings.Join(parts, "\x00")
}

func isMissingCollectionSearchError(message string) bool {
	normalized := strings.ToLower(strings.TrimSpace(message))
	return strings.Contains(normalized, "collection not found")
}
