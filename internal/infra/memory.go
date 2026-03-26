package infra

import (
	"github.com/kocort/kocort/internal/config"
	"github.com/kocort/kocort/internal/core"
	memorypkg "github.com/kocort/kocort/internal/memory"
)

const (
	DefaultMemoryTopK       = memorypkg.DefaultMemoryTopK
	DefaultMemoryChunkLines = memorypkg.DefaultMemoryChunkLines
	DefaultMemoryOverlap    = memorypkg.DefaultMemoryOverlap
)

type MemoryManager = memorypkg.MemoryManager
type MemoryBackend = memorypkg.MemoryBackend
type MemorySearchQuery = memorypkg.MemorySearchQuery
type LexicalMemoryBackend = memorypkg.LexicalMemoryBackend
type BuiltinVectorMemoryBackend = memorypkg.BuiltinVectorMemoryBackend
type QMDMemoryBackend = memorypkg.QMDMemoryBackend
type WorkspaceMemoryIndex = memorypkg.WorkspaceMemoryIndex
type MemoryChunk = memorypkg.MemoryChunk
type ScoredMemoryChunk = memorypkg.ScoredMemoryChunk

func NewMemoryManager(cfg config.AppConfig) *MemoryManager {
	return memorypkg.NewManager(cfg)
}

func NewWorkspaceMemoryProvider() *MemoryManager {
	return memorypkg.NewWorkspaceMemoryProvider()
}

func BuildWorkspaceMemoryIndexKey(workspaceDir string, files []string) (string, error) {
	return memorypkg.BuildWorkspaceMemoryIndexKey(workspaceDir, files)
}

func ListMemoryFilesForIdentity(workspaceDir string, identity core.AgentIdentity) ([]string, error) {
	return memorypkg.ListMemoryFilesForIdentity(workspaceDir, identity)
}

func BuildWorkspaceMemoryIndex(workspaceDir string, files []string, key string, identity core.AgentIdentity) (WorkspaceMemoryIndex, error) {
	return memorypkg.BuildWorkspaceMemoryIndex(workspaceDir, files, key, identity)
}

func BuildMemoryChunksForFile(source string, content string, identity core.AgentIdentity) []MemoryChunk {
	return memorypkg.BuildMemoryChunksForFile(source, content, identity)
}

func MergeAdjacentTinyChunks(chunks []MemoryChunk) []MemoryChunk {
	return memorypkg.MergeAdjacentTinyChunks(chunks)
}

func CompactChunkID(index int) string {
	return memorypkg.CompactChunkID(index)
}

func SearchMemoryIndex(chunks []MemoryChunk, query string, queryTerms []string, limit int) []core.MemoryHit {
	return memorypkg.SearchMemoryIndex(chunks, query, queryTerms, limit)
}

func SearchMemoryIndexVector(chunks []MemoryChunk, query string, queryTerms []string, limit int) []core.MemoryHit {
	return memorypkg.SearchMemoryIndexVector(chunks, query, queryTerms, limit)
}

func ScoreMemoryChunk(chunk MemoryChunk, queryLower string, queryTerms []string) float64 {
	return memorypkg.ScoreMemoryChunk(chunk, queryLower, queryTerms)
}

func ScoreMemoryChunkVector(chunk MemoryChunk, queryTokens []string, queryTerms []string) float64 {
	return memorypkg.ScoreMemoryChunkVector(chunk, queryTokens, queryTerms)
}

func MergeHybridMemoryHits(textHits []core.MemoryHit, vectorHits []core.MemoryHit, query MemorySearchQuery) []core.MemoryHit {
	return memorypkg.MergeHybridMemoryHits(textHits, vectorHits, query)
}

func DedupeMemoryBackends(backends []MemoryBackend) []MemoryBackend {
	return memorypkg.DedupeMemoryBackends(backends)
}

func ClipMemoryHits(hits []core.MemoryHit, maxResults int, minScore float64) []core.MemoryHit {
	return memorypkg.ClipMemoryHits(hits, maxResults, minScore)
}

func TokenizeSearchText(text string) []string {
	return memorypkg.TokenizeSearchText(text)
}

func ToCharacterNgrams(text string, n int) []string {
	return memorypkg.ToCharacterNgrams(text, n)
}

func JaccardSimilarity(a []string, b []string) float64 {
	return memorypkg.JaccardSimilarity(a, b)
}

func FallbackInt(value int, fallback int) int {
	return memorypkg.FallbackInt(value, fallback)
}

func FallbackFloat(value float64, fallback float64) float64 {
	return memorypkg.FallbackFloat(value, fallback)
}

func QMDMemoryCommand(cfg config.AppConfig) string {
	return memorypkg.QMDMemoryCommand(cfg)
}

func MemoryEnabled(identity core.AgentIdentity) bool {
	return memorypkg.MemoryEnabled(identity)
}
