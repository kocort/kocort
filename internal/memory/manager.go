package memory

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/kocort/kocort/internal/config"
	"github.com/kocort/kocort/internal/core"
)

const (
	DefaultMemoryFilename   = "MEMORY.md"
	DefaultMemoryAltFile    = "memory.md"
	DefaultMemoryTopK       = 5
	DefaultMemoryChunkLines = 12
	DefaultMemoryOverlap    = 3
)

type MemoryManager struct {
	Config  config.AppConfig
	lexical *LexicalMemoryBackend
	vector  *BuiltinVectorMemoryBackend
	qmd     *QMDMemoryBackend
	watch   *memoryWatchCoordinator
}

type MemoryBackend interface {
	Name() string
	IsAvailable(ctx context.Context, identity core.AgentIdentity) bool
	Recall(ctx context.Context, identity core.AgentIdentity, session core.SessionResolution, query MemorySearchQuery) ([]core.MemoryHit, error)
}

type MemorySearchQuery struct {
	Text            string
	Terms           []string
	MaxResults      int
	MinScore        float64
	HybridEnabled   bool
	VectorWeight    float64
	TextWeight      float64
	CandidateFactor int
}

type LexicalMemoryBackend struct {
	mu    sync.Mutex
	cache map[string]WorkspaceMemoryIndex
}

type BuiltinVectorMemoryBackend struct {
	lexical *LexicalMemoryBackend
}

type QMDMemoryBackend struct {
	Config config.AppConfig
	mu     sync.Mutex
	ready  map[string]struct{}
}

type WorkspaceMemoryIndex struct {
	Key    string
	Chunks []MemoryChunk
}

type SearchStatus struct {
	Backend  string `json:"backend,omitempty"`
	Provider string `json:"provider,omitempty"`
	Fallback string `json:"fallback,omitempty"`
	Mode     string `json:"mode,omitempty"`
}

type MemoryChunk struct {
	ID       string
	Source   string
	Path     string
	FromLine int
	ToLine   int
	Text     string
	Terms    []string
}

type ScoredMemoryChunk struct {
	Chunk MemoryChunk
	Score float64
}

func NewMemoryManager(cfg config.AppConfig) *MemoryManager {
	lexical := &LexicalMemoryBackend{cache: map[string]WorkspaceMemoryIndex{}}
	manager := &MemoryManager{
		Config:  cfg,
		lexical: lexical,
		vector:  &BuiltinVectorMemoryBackend{lexical: lexical},
		qmd:     &QMDMemoryBackend{Config: cfg, ready: map[string]struct{}{}},
	}
	manager.watch = newMemoryWatchCoordinator(lexical)
	return manager
}

func NewManager(cfg config.AppConfig) *MemoryManager {
	return NewMemoryManager(cfg)
}

func NewWorkspaceMemoryProvider() *MemoryManager {
	return NewMemoryManager(config.AppConfig{})
}

func (m *MemoryManager) Recall(ctx context.Context, identity core.AgentIdentity, session core.SessionResolution, message string) ([]core.MemoryHit, error) {
	if !MemoryEnabled(identity) {
		return nil, nil
	}
	if err := m.EnsurePrepared(ctx, identity, session); err != nil {
		return nil, err
	}
	queryTerms := TokenizeSearchText(message)
	if len(queryTerms) == 0 {
		return nil, nil
	}
	query := MemorySearchQuery{
		Text:            strings.TrimSpace(message),
		Terms:           queryTerms,
		MaxResults:      FallbackInt(identity.MemoryQueryMaxResults, DefaultMemoryTopK),
		MinScore:        identity.MemoryQueryMinScore,
		HybridEnabled:   identity.MemoryHybridEnabled,
		VectorWeight:    FallbackFloat(identity.MemoryHybridVectorWeight, 0.45),
		TextWeight:      FallbackFloat(identity.MemoryHybridTextWeight, 0.55),
		CandidateFactor: FallbackInt(identity.MemoryHybridCandidateFactor, 3),
	}

	candidates := m.resolveBackendOrder(identity)
	var lastErr error
	for _, backend := range candidates {
		if backend == nil || !backend.IsAvailable(ctx, identity) {
			continue
		}
		hits, err := backend.Recall(ctx, identity, session, query)
		if err != nil {
			lastErr = err
			continue
		}
		transcriptHits, transcriptErr := recallSessionTranscriptHits(identity, session, query)
		if transcriptErr == nil && len(transcriptHits) > 0 {
			hits = mergeSupplementalMemoryHits(hits, transcriptHits)
		}
		return ClipMemoryHits(hits, query.MaxResults, query.MinScore), nil
	}
	if lastErr != nil {
		return nil, lastErr
	}
	return nil, nil
}

func (m *MemoryManager) EnsurePrepared(ctx context.Context, identity core.AgentIdentity, session core.SessionResolution) error {
	if strings.EqualFold(strings.TrimSpace(identity.MemoryProvider), "qmd") || strings.EqualFold(strings.TrimSpace(m.Config.Memory.Backend), "qmd") {
		if err := m.qmd.EnsurePrepared(ctx, identity, session); err != nil {
			return err
		}
	}
	if m.watch != nil && identity.MemorySyncWatch {
		if err := m.watch.EnsureWatching(identity); err != nil {
			return err
		}
	}
	if identity.MemorySyncOnSessionStart {
		go m.preloadBuiltinIndex(identity)
	}
	return nil
}

func (m *MemoryManager) SearchStatus(identity core.AgentIdentity) SearchStatus {
	provider := strings.TrimSpace(strings.ToLower(identity.MemoryProvider))
	if provider == "" {
		provider = strings.TrimSpace(strings.ToLower(m.Config.Memory.Backend))
	}
	if provider == "" {
		provider = "builtin"
	}
	fallback := strings.TrimSpace(strings.ToLower(identity.MemoryFallback))
	if provider == "qmd" {
		mode := "search"
		if m.Config.Memory.QMD != nil && strings.TrimSpace(m.Config.Memory.QMD.SearchMode) != "" {
			mode = strings.TrimSpace(strings.ToLower(m.Config.Memory.QMD.SearchMode))
		}
		return SearchStatus{
			Backend:  "qmd",
			Provider: provider,
			Fallback: fallback,
			Mode:     mode,
		}
	}
	mode := "text"
	if identity.MemoryHybridEnabled {
		mode = "hybrid"
	} else if identity.MemoryVectorEnabled {
		mode = "vector"
	}
	return SearchStatus{
		Backend:  "builtin",
		Provider: provider,
		Fallback: fallback,
		Mode:     mode,
	}
}

func (m *MemoryManager) resolveBackendOrder(identity core.AgentIdentity) []MemoryBackend {
	provider := strings.TrimSpace(strings.ToLower(identity.MemoryProvider))
	if provider == "" {
		provider = strings.TrimSpace(strings.ToLower(m.Config.Memory.Backend))
	}
	if provider == "" {
		provider = "builtin"
	}
	fallback := strings.TrimSpace(strings.ToLower(identity.MemoryFallback))
	var backends []MemoryBackend
	switch provider {
	case "qmd":
		backends = append(backends, m.qmd)
	default:
		backends = append(backends, m.builtinBackend(identity))
	}
	switch fallback {
	case "", "none":
	default:
		switch fallback {
		case "qmd":
			backends = append(backends, m.qmd)
		default:
			backends = append(backends, m.builtinBackend(identity))
		}
	}
	return DedupeMemoryBackends(backends)
}

func (m *MemoryManager) builtinBackend(identity core.AgentIdentity) MemoryBackend {
	if identity.MemoryHybridEnabled || identity.MemoryVectorEnabled {
		return HybridMemoryBackendWrapper{Lexical: m.lexical, Vector: m.vector}
	}
	return m.lexical
}

type HybridMemoryBackendWrapper struct {
	Lexical *LexicalMemoryBackend
	Vector  *BuiltinVectorMemoryBackend
}

func (b HybridMemoryBackendWrapper) Name() string {
	return "builtin"
}

func (b HybridMemoryBackendWrapper) IsAvailable(ctx context.Context, identity core.AgentIdentity) bool {
	return b.Lexical.IsAvailable(ctx, identity)
}

func (b HybridMemoryBackendWrapper) Recall(ctx context.Context, identity core.AgentIdentity, session core.SessionResolution, query MemorySearchQuery) ([]core.MemoryHit, error) {
	lexicalHits, err := b.Lexical.Recall(ctx, identity, session, query)
	if err != nil {
		return nil, err
	}
	vectorHits, err := b.Vector.Recall(ctx, identity, session, query)
	if err != nil {
		return lexicalHits, nil
	}
	return MergeHybridMemoryHits(lexicalHits, vectorHits, query), nil
}

func (b *LexicalMemoryBackend) Name() string {
	return "builtin-text"
}

func (b *LexicalMemoryBackend) IsAvailable(_ context.Context, identity core.AgentIdentity) bool {
	return strings.TrimSpace(identity.WorkspaceDir) != ""
}

func (b *LexicalMemoryBackend) Recall(_ context.Context, identity core.AgentIdentity, _ core.SessionResolution, query MemorySearchQuery) ([]core.MemoryHit, error) {
	workspaceDir, err := EnsureWorkspaceDir(identity.WorkspaceDir)
	if err != nil || workspaceDir == "" {
		return nil, err
	}
	index, err := b.LoadIndex(workspaceDir, identity)
	if err != nil {
		return nil, err
	}
	return SearchMemoryIndex(index.Chunks, query.Text, query.Terms, query.MaxResults), nil
}

func (b *BuiltinVectorMemoryBackend) Name() string {
	return "builtin-vector"
}

func (b *BuiltinVectorMemoryBackend) IsAvailable(_ context.Context, identity core.AgentIdentity) bool {
	return identity.MemoryVectorEnabled || identity.MemoryHybridEnabled
}

func (b *BuiltinVectorMemoryBackend) Recall(_ context.Context, identity core.AgentIdentity, _ core.SessionResolution, query MemorySearchQuery) ([]core.MemoryHit, error) {
	workspaceDir, err := EnsureWorkspaceDir(identity.WorkspaceDir)
	if err != nil || workspaceDir == "" {
		return nil, err
	}
	index, err := b.lexical.LoadIndex(workspaceDir, identity)
	if err != nil {
		return nil, err
	}
	return SearchMemoryIndexVector(index.Chunks, query.Text, query.Terms, query.MaxResults), nil
}

func (b *QMDMemoryBackend) Name() string {
	return "qmd"
}

func (b *QMDMemoryBackend) IsAvailable(_ context.Context, _ core.AgentIdentity) bool {
	return strings.TrimSpace(QMDMemoryCommand(b.Config)) != ""
}

func (b *QMDMemoryBackend) Recall(ctx context.Context, identity core.AgentIdentity, session core.SessionResolution, query MemorySearchQuery) ([]core.MemoryHit, error) {
	command := QMDMemoryCommand(b.Config)
	if strings.TrimSpace(command) == "" {
		return nil, fmt.Errorf("qmd command is not configured")
	}
	searchMode := "search"
	if b.Config.Memory.QMD != nil && strings.TrimSpace(b.Config.Memory.QMD.SearchMode) != "" {
		searchMode = strings.TrimSpace(b.Config.Memory.QMD.SearchMode)
	}
	timeout := 5 * time.Second
	if b.Config.Memory.QMD != nil && b.Config.Memory.QMD.Limits != nil && b.Config.Memory.QMD.Limits.TimeoutMs > 0 {
		timeout = time.Duration(b.Config.Memory.QMD.Limits.TimeoutMs) * time.Millisecond
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	limit := query.MaxResults
	if b.Config.Memory.QMD != nil && b.Config.Memory.QMD.Limits != nil && b.Config.Memory.QMD.Limits.MaxResults > 0 {
		limit = minPositive(limit, b.Config.Memory.QMD.Limits.MaxResults)
	}
	if limit <= 0 {
		limit = DefaultMemoryTopK
	}
	runSearch := func() ([]core.MemoryHit, error) {
		collectionNames := resolveQMDCollectionNames(identity, session, b.Config)
		if len(collectionNames) == 0 {
			output, err := runQMDSearchCommand(runCtx, command, identity.WorkspaceDir, []string{searchMode, strings.TrimSpace(query.Text), "--json", "-n", fmt.Sprintf("%d", limit)})
			if err != nil {
				if isQMDNoResultsOutput(output) {
					return nil, nil
				}
				return nil, fmt.Errorf("qmd search failed: %w: %s", err, strings.TrimSpace(output))
			}
			hits, err := parseQMDSearchHits([]byte(output), query, b.Config)
			if err != nil {
				return nil, err
			}
			return hits, nil
		}
		var merged []core.MemoryHit
		for _, collection := range collectionNames {
			args := []string{searchMode, strings.TrimSpace(query.Text), "--json", "-n", fmt.Sprintf("%d", limit), "-c", collection}
			output, err := runQMDSearchCommand(runCtx, command, identity.WorkspaceDir, args)
			if err != nil {
				if isQMDNoResultsOutput(output) {
					continue
				}
				return nil, fmt.Errorf("qmd search failed: %w: %s", err, strings.TrimSpace(output))
			}
			hits, parseErr := parseQMDSearchHits([]byte(output), query, b.Config)
			if parseErr != nil {
				return nil, parseErr
			}
			merged = append(merged, hits...)
		}
		return ClipMemoryHits(merged, query.MaxResults, query.MinScore), nil
	}

	hits, err := runSearch()
	if err == nil || !isMissingCollectionSearchError(err.Error()) {
		return hits, err
	}
	b.InvalidatePrepared(identity, session)
	if prepErr := b.EnsurePrepared(ctx, identity, session); prepErr != nil {
		return nil, err
	}
	return runSearch()
}

func (p *LexicalMemoryBackend) LoadIndex(workspaceDir string, identity core.AgentIdentity) (WorkspaceMemoryIndex, error) {
	files, err := ListMemoryFilesForIdentity(workspaceDir, identity)
	if err != nil {
		return WorkspaceMemoryIndex{}, err
	}
	key, err := BuildWorkspaceMemoryIndexKey(workspaceDir, files)
	if err != nil {
		return WorkspaceMemoryIndex{}, err
	}
	p.mu.Lock()
	if cached, ok := p.cache[workspaceDir]; ok && cached.Key == key {
		p.mu.Unlock()
		return cached, nil
	}
	p.mu.Unlock()

	index, err := BuildWorkspaceMemoryIndex(workspaceDir, files, key, identity)
	if err != nil {
		return WorkspaceMemoryIndex{}, err
	}
	p.mu.Lock()
	p.cache[workspaceDir] = index
	p.mu.Unlock()
	return index, nil
}

func BuildWorkspaceMemoryIndexKey(workspaceDir string, files []string) (string, error) {
	h := sha1.New()
	_, _ = h.Write([]byte(workspaceDir)) // hash.Write never returns an error
	_, _ = h.Write([]byte{0})            // hash.Write never returns an error
	for _, absPath := range files {
		info, err := os.Stat(absPath)
		if err != nil {
			return "", err
		}
		_, _ = h.Write([]byte(absPath))                                                  // hash.Write never returns an error
		_, _ = h.Write([]byte(info.ModTime().UTC().Format("20060102T150405.000000000"))) // hash.Write never returns an error
		_, _ = h.Write([]byte{0})                                                        // hash.Write never returns an error
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func ListMemoryFilesForIdentity(workspaceDir string, identity core.AgentIdentity) ([]string, error) {
	includeWorkspace := true
	if len(identity.MemorySources) > 0 {
		includeWorkspace = false
		for _, source := range identity.MemorySources {
			switch strings.ToLower(strings.TrimSpace(source)) {
			case "default", "memory", "workspace":
				includeWorkspace = true
			}
		}
	}
	seen := map[string]struct{}{}
	var files []string
	if includeWorkspace {
		workspaceFiles, err := ListWorkspaceMemoryFiles(workspaceDir)
		if err != nil {
			return nil, err
		}
		for _, path := range workspaceFiles {
			if _, ok := seen[path]; ok {
				continue
			}
			seen[path] = struct{}{}
			files = append(files, path)
		}
	}
	for _, rawPath := range identity.MemoryExtraPaths {
		trimmed := strings.TrimSpace(rawPath)
		if trimmed == "" {
			continue
		}
		resolved := trimmed
		if !filepath.IsAbs(resolved) {
			resolved = filepath.Join(workspaceDir, resolved)
		}
		info, err := os.Stat(resolved)
		if err != nil {
			continue
		}
		if info.IsDir() {
			dirEntries, err := os.ReadDir(resolved)
			if err != nil {
				continue
			}
			for _, entry := range dirEntries {
				if entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
					continue
				}
				fullPath := filepath.Join(resolved, entry.Name())
				if _, ok := seen[fullPath]; ok {
					continue
				}
				seen[fullPath] = struct{}{}
				files = append(files, fullPath)
			}
			continue
		}
		if _, ok := seen[resolved]; ok {
			continue
		}
		seen[resolved] = struct{}{}
		files = append(files, resolved)
	}
	sort.Strings(files)
	return files, nil
}

func BuildWorkspaceMemoryIndex(workspaceDir string, files []string, key string, identity core.AgentIdentity) (WorkspaceMemoryIndex, error) {
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
		relSource, relErr := filepath.Rel(workspaceDir, absPath)
		if relErr != nil {
			relSource = filepath.Base(absPath)
		}
		source := filepath.ToSlash(relSource)
		fileChunks := BuildMemoryChunksForFile(source, content, identity)
		chunks = append(chunks, fileChunks...)
	}
	sort.SliceStable(chunks, func(i, j int) bool {
		if chunks[i].Path == chunks[j].Path {
			return chunks[i].FromLine < chunks[j].FromLine
		}
		return chunks[i].Path < chunks[j].Path
	})
	return WorkspaceMemoryIndex{
		Key:    key,
		Chunks: chunks,
	}, nil
}

func BuildMemoryChunksForFile(source string, content string, identity core.AgentIdentity) []MemoryChunk {
	lines := strings.Split(content, "\n")
	var chunks []MemoryChunk
	chunkIndex := 0
	chunkLines := FallbackInt(identity.MemoryChunkTokens, DefaultMemoryChunkLines)
	overlap := identity.MemoryChunkOverlap
	if overlap <= 0 || overlap >= chunkLines {
		overlap = DefaultMemoryOverlap
	}
	step := chunkLines - overlap
	if step <= 0 {
		step = chunkLines
	}
	for start := 0; start < len(lines); start += step {
		end := start + chunkLines
		if end > len(lines) {
			end = len(lines)
		}
		text := strings.TrimSpace(strings.Join(lines[start:end], "\n"))
		if text == "" {
			continue
		}
		chunkIndex++
		chunks = append(chunks, MemoryChunk{
			ID:       source + "#" + CompactChunkID(chunkIndex),
			Source:   source,
			Path:     source,
			FromLine: start + 1,
			ToLine:   end,
			Text:     text,
			Terms:    TokenizeSearchText(text),
		})
		if end == len(lines) {
			break
		}
	}
	return MergeAdjacentTinyChunks(chunks)
}

func MergeAdjacentTinyChunks(chunks []MemoryChunk) []MemoryChunk {
	if len(chunks) < 2 {
		return chunks
	}
	merged := make([]MemoryChunk, 0, len(chunks))
	for i := 0; i < len(chunks); i++ {
		current := chunks[i]
		if len(current.Text) >= 120 || i == len(chunks)-1 {
			merged = append(merged, current)
			continue
		}
		next := chunks[i+1]
		if current.Path != next.Path {
			merged = append(merged, current)
			continue
		}
		combined := strings.TrimSpace(current.Text + "\n" + next.Text)
		merged = append(merged, MemoryChunk{
			ID:       current.ID,
			Source:   current.Source,
			Path:     current.Path,
			FromLine: current.FromLine,
			ToLine:   next.ToLine,
			Text:     combined,
			Terms:    TokenizeSearchText(combined),
		})
		i++
	}
	return merged
}

func CompactChunkID(index int) string {
	return fmt.Sprintf("%04d", index)
}

func SearchMemoryIndex(chunks []MemoryChunk, query string, queryTerms []string, limit int) []core.MemoryHit {
	queryLower := strings.ToLower(strings.TrimSpace(query))
	var scored []ScoredMemoryChunk
	for _, chunk := range chunks {
		score := ScoreMemoryChunk(chunk, queryLower, queryTerms)
		if score <= 0 {
			continue
		}
		scored = append(scored, ScoredMemoryChunk{Chunk: chunk, Score: score})
	}
	return BuildMemoryHitsFromScoredChunks(scored, limit)
}

func SearchMemoryIndexVector(chunks []MemoryChunk, query string, queryTerms []string, limit int) []core.MemoryHit {
	queryTokens := ToCharacterNgrams(strings.ToLower(strings.TrimSpace(query)), 3)
	var scored []ScoredMemoryChunk
	for _, chunk := range chunks {
		score := ScoreMemoryChunkVector(chunk, queryTokens, queryTerms)
		if score <= 0 {
			continue
		}
		scored = append(scored, ScoredMemoryChunk{Chunk: chunk, Score: score})
	}
	return BuildMemoryHitsFromScoredChunks(scored, limit)
}

func BuildMemoryHitsFromScoredChunks(scored []ScoredMemoryChunk, limit int) []core.MemoryHit {
	sort.SliceStable(scored, func(i, j int) bool {
		if scored[i].Score == scored[j].Score {
			if scored[i].Chunk.Path == scored[j].Chunk.Path {
				return scored[i].Chunk.FromLine < scored[j].Chunk.FromLine
			}
			return scored[i].Chunk.Path < scored[j].Chunk.Path
		}
		return scored[i].Score > scored[j].Score
	})
	if limit <= 0 {
		limit = DefaultMemoryTopK
	}
	if len(scored) > limit {
		scored = scored[:limit]
	}
	hits := make([]core.MemoryHit, 0, len(scored))
	for _, item := range scored {
		snippet := item.Chunk.Text
		if len(snippet) > 500 {
			snippet = strings.TrimSpace(snippet[:500]) + "..."
		}
		hits = append(hits, core.MemoryHit{
			ID:       item.Chunk.ID,
			Source:   item.Chunk.Source,
			Path:     item.Chunk.Path,
			Snippet:  snippet,
			Score:    item.Score,
			FromLine: item.Chunk.FromLine,
			ToLine:   item.Chunk.ToLine,
		})
	}
	return hits
}

func ScoreMemoryChunk(chunk MemoryChunk, queryLower string, queryTerms []string) float64 {
	if len(queryTerms) == 0 {
		return 0
	}
	termMatches := 0
	chunkLower := strings.ToLower(chunk.Text)
	for _, term := range queryTerms {
		if strings.Contains(chunkLower, term) {
			termMatches++
		}
	}
	if termMatches == 0 {
		return 0
	}
	score := float64(termMatches)
	if len(queryLower) >= 6 && strings.Contains(chunkLower, queryLower) {
		score += 3
	}
	pathLower := strings.ToLower(chunk.Path)
	for _, term := range queryTerms {
		if strings.Contains(pathLower, term) {
			score += 0.35
		}
	}
	if strings.HasSuffix(pathLower, "memory.md") {
		score += 0.1
	}
	score += 1.0 / float64(1+chunk.FromLine)
	return score
}

func ScoreMemoryChunkVector(chunk MemoryChunk, queryTokens []string, queryTerms []string) float64 {
	if len(queryTokens) == 0 {
		return 0
	}
	chunkTokens := ToCharacterNgrams(strings.ToLower(chunk.Text), 3)
	if len(chunkTokens) == 0 {
		return 0
	}
	score := JaccardSimilarity(queryTokens, chunkTokens)
	if score <= 0 {
		return 0
	}
	for _, term := range queryTerms {
		if strings.Contains(strings.ToLower(chunk.Path), term) {
			score += 0.05
		}
	}
	return score
}

func MergeHybridMemoryHits(textHits []core.MemoryHit, vectorHits []core.MemoryHit, query MemorySearchQuery) []core.MemoryHit {
	type mergedHit struct {
		core.MemoryHit
		textScore   float64
		vectorScore float64
	}
	index := map[string]*mergedHit{}
	for _, hit := range textHits {
		copy := hit
		index[hit.ID] = &mergedHit{MemoryHit: copy, textScore: hit.Score}
	}
	for _, hit := range vectorHits {
		if existing, ok := index[hit.ID]; ok {
			existing.vectorScore = hit.Score
			continue
		}
		copy := hit
		index[hit.ID] = &mergedHit{MemoryHit: copy, vectorScore: hit.Score}
	}
	merged := make([]mergedHit, 0, len(index))
	for _, item := range index {
		item.Score = (item.textScore * query.TextWeight) + (item.vectorScore * query.VectorWeight)
		merged = append(merged, *item)
	}
	sort.SliceStable(merged, func(i, j int) bool {
		if merged[i].Score == merged[j].Score {
			if merged[i].Path == merged[j].Path {
				return merged[i].FromLine < merged[j].FromLine
			}
			return merged[i].Path < merged[j].Path
		}
		return merged[i].Score > merged[j].Score
	})
	limit := query.MaxResults
	if limit <= 0 {
		limit = DefaultMemoryTopK
	}
	if len(merged) > limit {
		merged = merged[:limit]
	}
	hits := make([]core.MemoryHit, 0, len(merged))
	for _, item := range merged {
		if item.Score < query.MinScore {
			continue
		}
		hits = append(hits, item.MemoryHit)
	}
	return hits
}

func DedupeMemoryBackends(backends []MemoryBackend) []MemoryBackend {
	seen := map[string]struct{}{}
	var out []MemoryBackend
	for _, backend := range backends {
		if backend == nil {
			continue
		}
		name := strings.TrimSpace(backend.Name())
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		out = append(out, backend)
	}
	return out
}

func ClipMemoryHits(hits []core.MemoryHit, maxResults int, minScore float64) []core.MemoryHit {
	if minScore > 0 {
		filtered := hits[:0]
		for _, hit := range hits {
			if hit.Score >= minScore {
				filtered = append(filtered, hit)
			}
		}
		hits = filtered
	}
	if maxResults <= 0 {
		return hits
	}
	if len(hits) > maxResults {
		return append([]core.MemoryHit{}, hits[:maxResults]...)
	}
	return hits
}

func TokenizeSearchText(text string) []string {
	text = strings.ToLower(text)
	replacer := strings.NewReplacer(
		"\n", " ",
		"\r", " ",
		"\t", " ",
		",", " ",
		".", " ",
		":", " ",
		";", " ",
		"(", " ",
		")", " ",
		"[", " ",
		"]", " ",
		"{", " ",
		"}", " ",
		"!", " ",
		"?", " ",
		"/", " ",
		"\\", " ",
		"\"", " ",
		"'", " ",
		"-", " ",
		"_", " ",
	)
	cleaned := replacer.Replace(text)
	parts := strings.Fields(cleaned)
	seen := map[string]struct{}{}
	var out []string
	for _, part := range parts {
		if len(part) < 2 {
			continue
		}
		if _, ok := seen[part]; ok {
			continue
		}
		seen[part] = struct{}{}
		out = append(out, part)
	}
	return out
}

func ToCharacterNgrams(text string, n int) []string {
	if len(text) == 0 {
		return nil
	}
	text = strings.TrimSpace(text)
	if len(text) <= n {
		return []string{text}
	}
	seen := map[string]struct{}{}
	var out []string
	for i := 0; i <= len(text)-n; i++ {
		gram := text[i : i+n]
		if _, ok := seen[gram]; ok {
			continue
		}
		seen[gram] = struct{}{}
		out = append(out, gram)
	}
	return out
}

func JaccardSimilarity(a []string, b []string) float64 {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	setA := map[string]struct{}{}
	for _, item := range a {
		setA[item] = struct{}{}
	}
	intersection := 0
	union := len(setA)
	seenB := map[string]struct{}{}
	for _, item := range b {
		if _, ok := seenB[item]; ok {
			continue
		}
		seenB[item] = struct{}{}
		if _, ok := setA[item]; ok {
			intersection++
		} else {
			union++
		}
	}
	if union == 0 {
		return 0
	}
	return math.Round((float64(intersection)/float64(union))*1000) / 1000
}

func FallbackInt(value int, fallback int) int {
	if value > 0 {
		return value
	}
	return fallback
}

func FallbackFloat(value float64, fallback float64) float64 {
	if value > 0 {
		return value
	}
	return fallback
}

func QMDMemoryCommand(cfg config.AppConfig) string {
	if cfg.Memory.QMD == nil {
		return ""
	}
	return strings.TrimSpace(cfg.Memory.QMD.Command)
}

func MemoryEnabled(identity core.AgentIdentity) bool {
	switch strings.TrimSpace(strings.ToLower(identity.MemoryProvider)) {
	case "off", "none", "disabled":
		return false
	}
	if identity.MemoryEnabled {
		return true
	}
	return true
}

type qmdHit struct {
	ID       string  `json:"id"`
	DocID    string  `json:"docid"`
	Path     string  `json:"path"`
	File     string  `json:"file"`
	Source   string  `json:"source"`
	URI      string  `json:"uri"`
	Snippet  string  `json:"snippet"`
	Score    float64 `json:"score"`
	FromLine int     `json:"fromLine"`
	ToLine   int     `json:"toLine"`
	Line     int     `json:"line"`
	Start    int     `json:"start"`
	End      int     `json:"end"`
}

func parseQMDSearchHits(data []byte, query MemorySearchQuery, cfg config.AppConfig) ([]core.MemoryHit, error) {
	trimmed := strings.TrimSpace(string(data))
	if trimmed == "" || isQMDNoResultsOutput(trimmed) {
		return nil, nil
	}
	payload := extractFirstJSONArray(trimmed)
	if payload == "" {
		payload = trimmed
	}
	var raw []qmdHit
	if err := json.Unmarshal([]byte(payload), &raw); err != nil {
		return nil, fmt.Errorf("parse qmd search output: %w", err)
	}
	maxSnippetChars := 500
	if cfg.Memory.QMD != nil && cfg.Memory.QMD.Limits != nil && cfg.Memory.QMD.Limits.MaxSnippetChars > 0 {
		maxSnippetChars = cfg.Memory.QMD.Limits.MaxSnippetChars
	}
	hits := make([]core.MemoryHit, 0, len(raw))
	for _, item := range raw {
		path := firstNonEmpty(item.Path, item.File, item.Source, item.URI, item.DocID, item.ID)
		if path == "" {
			continue
		}
		snippet := strings.TrimSpace(item.Snippet)
		if maxSnippetChars > 0 && len(snippet) > maxSnippetChars {
			snippet = strings.TrimSpace(snippet[:maxSnippetChars]) + "..."
		}
		fromLine, toLine := normalizeQMDLineRange(item)
		hits = append(hits, core.MemoryHit{
			ID:       firstNonEmpty(item.ID, item.DocID, path),
			Source:   path,
			Path:     path,
			Snippet:  snippet,
			Score:    item.Score,
			FromLine: fromLine,
			ToLine:   toLine,
		})
	}
	return ClipMemoryHits(hits, query.MaxResults, query.MinScore), nil
}

func normalizeQMDLineRange(hit qmdHit) (int, int) {
	fromLine := hit.FromLine
	toLine := hit.ToLine
	if fromLine <= 0 && hit.Line > 0 {
		fromLine = hit.Line
	}
	if fromLine <= 0 && hit.Start > 0 {
		fromLine = hit.Start
	}
	if toLine <= 0 && hit.End >= fromLine && fromLine > 0 {
		toLine = hit.End
	}
	if fromLine > 0 && toLine <= 0 {
		toLine = fromLine
	}
	return fromLine, toLine
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func isQMDNoResultsOutput(raw string) bool {
	lines := strings.Split(strings.ReplaceAll(raw, "\r\n", "\n"), "\n")
	for _, line := range lines {
		normalized := strings.ToLower(strings.Join(strings.Fields(strings.TrimSpace(line)), " "))
		if normalized == "" {
			continue
		}
		if normalized == "no results found" || normalized == "no results found." {
			return true
		}
		if strings.HasSuffix(normalized, ": no results found") || strings.HasSuffix(normalized, ": no results found.") {
			return true
		}
	}
	return false
}

func extractFirstJSONArray(raw string) string {
	start := strings.Index(raw, "[")
	if start < 0 {
		return ""
	}
	depth := 0
	inString := false
	escaped := false
	for i := start; i < len(raw); i++ {
		char := raw[i]
		if inString {
			if escaped {
				escaped = false
				continue
			}
			if char == '\\' {
				escaped = true
				continue
			}
			if char == '"' {
				inString = false
			}
			continue
		}
		switch char {
		case '"':
			inString = true
		case '[':
			depth++
		case ']':
			depth--
			if depth == 0 {
				return raw[start : i+1]
			}
		}
	}
	return ""
}

func minPositive(values ...int) int {
	best := 0
	for _, value := range values {
		if value <= 0 {
			continue
		}
		if best == 0 || value < best {
			best = value
		}
	}
	return best
}

func EnsureWorkspaceDir(dir string) (string, error) {
	cleaned := strings.TrimSpace(dir)
	if cleaned == "" {
		return "", nil
	}
	abs, err := filepath.Abs(cleaned)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(abs, 0o755); err != nil {
		return "", err
	}
	return abs, nil
}

func ListWorkspaceMemoryFiles(workspaceDir string) ([]string, error) {
	if strings.TrimSpace(workspaceDir) == "" {
		return nil, nil
	}
	var files []string
	for _, name := range []string{DefaultMemoryFilename, DefaultMemoryAltFile} {
		full := filepath.Join(workspaceDir, name)
		if _, err := os.Stat(full); err == nil {
			files = append(files, full)
		}
	}
	memoryDir := filepath.Join(workspaceDir, "memory")
	entries, err := os.ReadDir(memoryDir)
	if err != nil {
		if os.IsNotExist(err) {
			sort.Strings(files)
			return files, nil
		}
		return nil, err
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if strings.HasPrefix(name, ".") {
			continue
		}
		full := filepath.Join(memoryDir, name)
		info, err := entry.Info()
		if err != nil || info.Mode().Type() != fs.ModeIrregular && !info.Mode().IsRegular() {
			continue
		}
		files = append(files, full)
	}
	sort.Strings(files)
	return files, nil
}
