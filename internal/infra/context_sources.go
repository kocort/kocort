package infra

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/kocort/kocort/internal/config"
	"github.com/kocort/kocort/internal/core"
)

type ContextSource interface {
	ID() string
	Type() string
	Status(ctx context.Context, identity core.AgentIdentity) core.ContextSourceStatus
	Query(ctx context.Context, identity core.AgentIdentity, session core.SessionResolution, query string) ([]core.ContextSourceHit, error)
}

type ContextSourceRegistry struct {
	config config.AppConfig
	mu     sync.Mutex
	index  map[string]core.ContextSourceStatus
}

func NewContextSourceRegistry(cfg config.AppConfig) *ContextSourceRegistry {
	return &ContextSourceRegistry{
		config: cfg,
		index:  map[string]core.ContextSourceStatus{},
	}
}

func (r *ContextSourceRegistry) Query(ctx context.Context, identity core.AgentIdentity, session core.SessionResolution, query string) ([]core.ContextSourceHit, error) {
	if r == nil || len(r.config.Data.Entries) == 0 {
		return nil, nil
	}
	var hits []core.ContextSourceHit
	for id, cfg := range r.config.Data.Entries {
		source := BuildContextSource(id, cfg)
		if source == nil {
			continue
		}
		status := source.Status(ctx, identity)
		r.recordStatus(status)
		if !status.Enabled || !status.Available {
			continue
		}
		found, err := source.Query(ctx, identity, session, query)
		if err != nil {
			status.LastError = strings.TrimSpace(err.Error())
			r.recordStatus(status)
			continue
		}
		hits = append(hits, found...)
	}
	sort.SliceStable(hits, func(i, j int) bool {
		if hits[i].Score == hits[j].Score {
			if hits[i].SourceID == hits[j].SourceID {
				return hits[i].Location < hits[j].Location
			}
			return hits[i].SourceID < hits[j].SourceID
		}
		return hits[i].Score > hits[j].Score
	})
	if len(hits) > 6 {
		hits = hits[:6]
	}
	return hits, nil
}

func (r *ContextSourceRegistry) Statuses(ctx context.Context, identity core.AgentIdentity) []core.ContextSourceStatus {
	if r == nil {
		return nil
	}
	if len(r.config.Data.Entries) > 0 {
		for id, cfg := range r.config.Data.Entries {
			source := BuildContextSource(id, cfg)
			if source == nil {
				continue
			}
			r.recordStatus(source.Status(ctx, identity))
		}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]core.ContextSourceStatus, 0, len(r.index))
	for _, status := range r.index {
		out = append(out, status)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func (r *ContextSourceRegistry) recordStatus(status core.ContextSourceStatus) {
	if r == nil || strings.TrimSpace(status.ID) == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.index[status.ID] = status
}

func BuildContextSource(id string, cfg config.DataSourceConfig) ContextSource {
	switch strings.ToLower(strings.TrimSpace(cfg.Type)) {
	case "file", "files":
		return &FileContextSource{id: strings.TrimSpace(id), config: cfg}
	case "sqlite":
		return &SQLiteContextSource{id: strings.TrimSpace(id), config: cfg}
	default:
		return nil
	}
}

type FileContextSource struct {
	id     string
	config config.DataSourceConfig
}

func (s *FileContextSource) ID() string   { return s.id }
func (s *FileContextSource) Type() string { return "file" }

func (s *FileContextSource) Status(_ context.Context, identity core.AgentIdentity) core.ContextSourceStatus {
	path := ResolveContextSourcePath(identity.WorkspaceDir, s.config.Path)
	enabled := s.config.Enabled == nil || *s.config.Enabled
	status := core.ContextSourceStatus{ID: s.id, Type: s.Type(), Enabled: enabled, Path: path}
	if !enabled {
		return status
	}
	info, err := os.Stat(path)
	if err != nil {
		status.LastError = err.Error()
		return status
	}
	status.Available = true
	status.LastIndexedAt = info.ModTime().UTC()
	if info.IsDir() {
		files, _ := ListWorkspaceMemoryFiles(path) // optional; missing files is acceptable
		status.ItemCount = len(files)
	} else {
		status.ItemCount = 1
	}
	return status
}

func (s *FileContextSource) Query(_ context.Context, identity core.AgentIdentity, _ core.SessionResolution, query string) ([]core.ContextSourceHit, error) {
	path := ResolveContextSourcePath(identity.WorkspaceDir, s.config.Path)
	files, err := CollectContextSourceFiles(path)
	if err != nil {
		return nil, err
	}
	terms := TokenizeSearchText(query)
	if len(terms) == 0 {
		return nil, nil
	}
	var chunks []MemoryChunk
	for _, absPath := range files {
		contentBytes, err := os.ReadFile(absPath)
		if err != nil {
			continue
		}
		content := strings.ReplaceAll(string(contentBytes), "\r\n", "\n")
		if strings.TrimSpace(content) == "" {
			continue
		}
		rel := absPath
		if relPath, relErr := filepath.Rel(identity.WorkspaceDir, absPath); relErr == nil {
			rel = filepath.ToSlash(relPath)
		}
		chunks = append(chunks, BuildMemoryChunksForFile(rel, content, identity)...)
	}
	memoryHits := SearchMemoryIndex(chunks, query, terms, max(1, FallbackInt(s.config.MaxRows, 4)))
	return ConvertMemoryHitsToContextHits(s.id, s.Type(), memoryHits), nil
}

type SQLiteContextSource struct {
	id     string
	config config.DataSourceConfig
}

func (s *SQLiteContextSource) ID() string   { return s.id }
func (s *SQLiteContextSource) Type() string { return "sqlite" }

func (s *SQLiteContextSource) Status(_ context.Context, identity core.AgentIdentity) core.ContextSourceStatus {
	path := ResolveContextSourcePath(identity.WorkspaceDir, s.config.Path)
	enabled := s.config.Enabled == nil || *s.config.Enabled
	status := core.ContextSourceStatus{ID: s.id, Type: s.Type(), Enabled: enabled, Path: path}
	if !enabled {
		return status
	}
	if _, err := exec.LookPath("sqlite3"); err != nil {
		status.LastError = "sqlite3 not found"
		return status
	}
	if _, err := os.Stat(path); err != nil {
		status.LastError = err.Error()
		return status
	}
	tables, err := SQLiteListTables(path)
	if err != nil {
		status.LastError = err.Error()
		return status
	}
	status.Available = true
	status.ItemCount = len(tables)
	status.LastIndexedAt = time.Now().UTC()
	return status
}

func (s *SQLiteContextSource) Query(_ context.Context, identity core.AgentIdentity, _ core.SessionResolution, query string) ([]core.ContextSourceHit, error) {
	path := ResolveContextSourcePath(identity.WorkspaceDir, s.config.Path)
	terms := TokenizeSearchText(query)
	if len(terms) == 0 {
		return nil, nil
	}
	tables := append([]string{}, s.config.Tables...)
	if len(tables) == 0 {
		listed, err := SQLiteListTables(path)
		if err != nil {
			return nil, err
		}
		tables = listed
	}
	var hits []core.ContextSourceHit
	maxRows := max(1, FallbackInt(s.config.MaxRows, 5))
	for _, table := range tables {
		cols, err := SQLiteTextColumns(path, table)
		if err != nil || len(cols) == 0 {
			continue
		}
		rows, err := SQLiteQueryRows(path, table, cols, strings.Join(terms, " "), maxRows)
		if err != nil {
			continue
		}
		for _, row := range rows {
			score := ScoreSQLiteRow(row, terms, table)
			if score <= 0 {
				continue
			}
			hits = append(hits, core.ContextSourceHit{
				SourceID: s.id,
				Type:     s.Type(),
				Path:     path,
				Location: fmt.Sprintf("%s#rowid=%s", table, row["rowid"]),
				Snippet:  TrimSnippet(strings.TrimSpace(row["text"]), 500),
				Score:    score,
			})
		}
	}
	sort.SliceStable(hits, func(i, j int) bool {
		if hits[i].Score == hits[j].Score {
			return hits[i].Location < hits[j].Location
		}
		return hits[i].Score > hits[j].Score
	})
	if len(hits) > maxRows {
		hits = hits[:maxRows]
	}
	return hits, nil
}

func ResolveContextSourcePath(workspaceDir, rawPath string) string {
	trimmed := strings.TrimSpace(rawPath)
	if trimmed == "" {
		return ""
	}
	if filepath.IsAbs(trimmed) {
		return trimmed
	}
	return filepath.Join(strings.TrimSpace(workspaceDir), trimmed)
}

func CollectContextSourceFiles(path string) ([]string, error) {
	if strings.TrimSpace(path) == "" {
		return nil, fmt.Errorf("missing source path")
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return []string{path}, nil
	}
	return ListWorkspaceMemoryFiles(path)
}

func ConvertMemoryHitsToContextHits(sourceID, kind string, hits []core.MemoryHit) []core.ContextSourceHit {
	out := make([]core.ContextSourceHit, 0, len(hits))
	for _, hit := range hits {
		out = append(out, core.ContextSourceHit{
			SourceID: sourceID,
			Type:     kind,
			Path:     hit.Path,
			Location: fmt.Sprintf("%s:%d-%d", hit.Path, hit.FromLine, hit.ToLine),
			Snippet:  hit.Snippet,
			Score:    hit.Score,
		})
	}
	return out
}

func SQLiteListTables(path string) ([]string, error) {
	out, err := exec.Command("sqlite3", "-batch", "-noheader", path, `SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%' ORDER BY name;`).CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("sqlite list tables: %w: %s", err, strings.TrimSpace(string(out)))
	}
	var tables []string
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			tables = append(tables, line)
		}
	}
	return tables, nil
}

func SQLiteTextColumns(path, table string) ([]string, error) {
	out, err := exec.Command("sqlite3", "-batch", "-noheader", "-separator", "\t", path, fmt.Sprintf(`PRAGMA table_info(%s);`, SQLiteQuoteIdentifier(table))).CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("sqlite table info: %w: %s", err, strings.TrimSpace(string(out)))
	}
	var cols []string
	for _, line := range strings.Split(string(out), "\n") {
		parts := strings.Split(line, "\t")
		if len(parts) < 3 {
			continue
		}
		name := strings.TrimSpace(parts[1])
		typ := strings.ToUpper(strings.TrimSpace(parts[2]))
		if name == "" {
			continue
		}
		if typ == "" || strings.Contains(typ, "CHAR") || strings.Contains(typ, "TEXT") || strings.Contains(typ, "CLOB") {
			cols = append(cols, name)
		}
	}
	return cols, nil
}

func SQLiteQueryRows(path, table string, cols []string, query string, limit int) ([]map[string]string, error) {
	var selects []string
	var concatParts []string
	for _, col := range cols {
		selects = append(selects, fmt.Sprintf("CAST(%s AS TEXT)", SQLiteQuoteIdentifier(col)))
		concatParts = append(concatParts, fmt.Sprintf("COALESCE(CAST(%s AS TEXT),'')", SQLiteQuoteIdentifier(col)))
	}
	sql := fmt.Sprintf(
		`SELECT rowid, trim(%s) FROM %s WHERE lower(%s) LIKE lower('%%%s%%') LIMIT %d;`,
		strings.Join(selects, " || ' ' || "),
		SQLiteQuoteIdentifier(table),
		strings.Join(concatParts, " || ' ' || "),
		SQLiteQuoteLiteral(query),
		limit,
	)
	out, err := exec.Command("sqlite3", "-batch", "-noheader", "-separator", "\t", path, sql).CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("sqlite query rows: %w: %s", err, strings.TrimSpace(string(out)))
	}
	var rows []map[string]string
	for _, line := range strings.Split(string(out), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		parts := strings.SplitN(line, "\t", 2)
		if len(parts) != 2 {
			continue
		}
		rows = append(rows, map[string]string{
			"rowid": strings.TrimSpace(parts[0]),
			"text":  strings.TrimSpace(parts[1]),
		})
	}
	return rows, nil
}

func SQLiteQuoteIdentifier(value string) string {
	return `"` + strings.ReplaceAll(strings.TrimSpace(value), `"`, `""`) + `"`
}

func SQLiteQuoteLiteral(value string) string {
	return strings.ReplaceAll(value, `'`, `''`)
}

func ScoreSQLiteRow(row map[string]string, terms []string, table string) float64 {
	text := strings.ToLower(strings.TrimSpace(row["text"]))
	if text == "" {
		return 0
	}
	score := 0.0
	for _, term := range terms {
		if strings.Contains(text, term) {
			score += 1
		}
		if strings.Contains(strings.ToLower(table), term) {
			score += 0.15
		}
	}
	return score
}

func TrimSnippet(text string, limit int) string {
	if limit <= 0 || len(text) <= limit {
		return text
	}
	return strings.TrimSpace(text[:limit]) + "..."
}
