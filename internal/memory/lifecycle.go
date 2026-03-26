package memory

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/kocort/kocort/internal/core"
)

type Preparer interface {
	EnsurePrepared(ctx context.Context, identity core.AgentIdentity, session core.SessionResolution) error
}

type StatusProvider interface {
	SearchStatus(identity core.AgentIdentity) SearchStatus
}

type memoryWatchCoordinator struct {
	lexical *LexicalMemoryBackend

	mu       sync.Mutex
	watchers map[string]*memoryWatchState
}

type memoryWatchState struct {
	workspaceDir string
	watcher      *fsnotify.Watcher
	roots        []string
}

func newMemoryWatchCoordinator(lexical *LexicalMemoryBackend) *memoryWatchCoordinator {
	return &memoryWatchCoordinator{
		lexical:  lexical,
		watchers: map[string]*memoryWatchState{},
	}
}

func (c *memoryWatchCoordinator) EnsureWatching(identity core.AgentIdentity) error {
	workspaceDir := strings.TrimSpace(identity.WorkspaceDir)
	if workspaceDir == "" {
		return nil
	}
	c.mu.Lock()
	if _, ok := c.watchers[workspaceDir]; ok {
		c.mu.Unlock()
		return nil
	}
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		c.mu.Unlock()
		return err
	}
	state := &memoryWatchState{
		workspaceDir: workspaceDir,
		watcher:      watcher,
		roots:        collectMemoryWatchRoots(identity),
	}
	c.watchers[workspaceDir] = state
	c.mu.Unlock()

	for _, root := range state.roots {
		_ = watcher.Add(root)
	}
	go c.run(state)
	return nil
}

func (c *memoryWatchCoordinator) run(state *memoryWatchState) {
	debounce := time.NewTimer(time.Hour)
	if !debounce.Stop() {
		<-debounce.C
	}
	pending := false
	for {
		select {
		case event, ok := <-state.watcher.Events:
			if !ok {
				return
			}
			if event.Has(fsnotify.Create) {
				if info, err := os.Stat(event.Name); err == nil && info.IsDir() {
					_ = state.watcher.Add(event.Name)
				}
			}
			pending = true
			debounce.Reset(500 * time.Millisecond)
		case <-debounce.C:
			if pending {
				c.lexical.Invalidate(state.workspaceDir)
				pending = false
			}
		case _, ok := <-state.watcher.Errors:
			if !ok {
				return
			}
		}
	}
}

func collectMemoryWatchRoots(identity core.AgentIdentity) []string {
	seen := map[string]struct{}{}
	var roots []string
	add := func(path string) {
		path = strings.TrimSpace(path)
		if path == "" {
			return
		}
		abs := path
		if !filepath.IsAbs(abs) {
			abs = filepath.Join(identity.WorkspaceDir, abs)
		}
		abs = filepath.Clean(abs)
		if info, err := os.Stat(abs); err == nil && !info.IsDir() {
			abs = filepath.Dir(abs)
		}
		if _, ok := seen[abs]; ok {
			return
		}
		seen[abs] = struct{}{}
		roots = append(roots, abs)
	}
	add(identity.WorkspaceDir)
	add(filepath.Join(identity.WorkspaceDir, "memory"))
	for _, extra := range identity.MemoryExtraPaths {
		add(extra)
	}
	return roots
}

func (b *LexicalMemoryBackend) Invalidate(workspaceDir string) {
	if strings.TrimSpace(workspaceDir) == "" {
		return
	}
	b.mu.Lock()
	delete(b.cache, workspaceDir)
	b.mu.Unlock()
}

func (m *MemoryManager) preloadBuiltinIndex(identity core.AgentIdentity) {
	workspaceDir, err := EnsureWorkspaceDir(identity.WorkspaceDir)
	if err != nil || workspaceDir == "" {
		return
	}
	_, _ = m.lexical.LoadIndex(workspaceDir, identity)
}
