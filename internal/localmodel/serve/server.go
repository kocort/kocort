package serve

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/kocort/kocort/internal/localmodel/engine"
)

// ServerConfig holds the HTTP server configuration.
type ServerConfig struct {
	// Addr is the listen address (e.g. "127.0.0.1:8080" or ":0" for ephemeral).
	Addr string

	// EngineConfig configures the underlying inference engine.
	EngineConfig engine.EngineConfig
}

// Server wraps an Engine with an HTTP server exposing OpenAI-compatible endpoints.
//
// Quick start:
//
//	srv, err := serve.NewServer(serve.ServerConfig{
//	    Addr: ":8080",
//	    EngineConfig: engine.EngineConfig{
//	        ModelPath:   "path/to/model.gguf",
//	        ContextSize: 4096,
//	    },
//	})
//	if err != nil { log.Fatal(err) }
//	defer srv.Stop()
//
//	if err := srv.Start(ctx); err != nil && err != http.ErrServerClosed {
//	    log.Fatal(err)
//	}
type Server struct {
	engine    Inference
	server    *http.Server
	listener  net.Listener
	modelName string
	created   int64
	mu        sync.Mutex
	runCancel context.CancelFunc
	runDone   chan struct{}
	stopped   bool
}

// NewServer creates a new Server with the given configuration.
// The engine is initialized but the model is not yet loaded.
// Call Start() to load the model and begin serving.
func NewServer(cfg ServerConfig) (*Server, error) {
	eng, err := engine.NewEngine(cfg.EngineConfig)
	if err != nil {
		return nil, fmt.Errorf("create engine: %w", err)
	}

	modelName := filepath.Base(cfg.EngineConfig.ModelPath)
	modelName = strings.TrimSuffix(modelName, filepath.Ext(modelName))

	s := &Server{
		engine:    eng,
		modelName: modelName,
		created:   time.Now().Unix(),
	}

	mux := http.NewServeMux()
	s.installHandlers(mux)

	s.server = &http.Server{
		Handler:      mux,
		ReadTimeout:  5 * time.Minute,
		WriteTimeout: 10 * time.Minute,
		IdleTimeout:  60 * time.Second,
	}

	addr := cfg.Addr
	if addr == "" {
		addr = ":0"
	}

	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("listen %s: %w", addr, err)
	}
	s.listener = ln

	return s, nil
}

// NewServerFromEngine creates a Server wrapping an existing Engine.
// The engine must implement the Inference interface (e.g. *engine.Engine).
func NewServerFromEngine(eng Inference, addr string) (*Server, error) {
	s := &Server{
		engine:    eng,
		modelName: "local",
		created:   time.Now().Unix(),
	}

	mux := http.NewServeMux()
	s.installHandlers(mux)

	s.server = &http.Server{
		Handler:      mux,
		ReadTimeout:  5 * time.Minute,
		WriteTimeout: 10 * time.Minute,
		IdleTimeout:  60 * time.Second,
	}

	if addr == "" {
		addr = ":0"
	}

	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("listen %s: %w", addr, err)
	}
	s.listener = ln

	return s, nil
}

// Start loads the model and begins serving. Blocks until the server stops.
// The context controls model loading — if cancelled before the model is ready,
// loading is aborted. Use Stop() to gracefully shut down.
func (s *Server) Start(ctx context.Context) error {
	// Load the model in the background and start the engine loop.
	errCh := make(chan error, 1)
	go func() {
		slog.Info("[server] loading model...")
		if err := s.engine.Load(); err != nil {
			errCh <- fmt.Errorf("load model: %w", err)
			return
		}
		slog.Info("[server] model loaded, starting engine run loop")
		runCtx, cancel := context.WithCancel(ctx)
		runDone := make(chan struct{})
		s.mu.Lock()
		s.runCancel = cancel
		s.runDone = runDone
		s.mu.Unlock()
		go func() {
			defer close(runDone)
			s.engine.Run(runCtx)
		}()
		close(errCh)
	}()

	// Wait for the model to finish loading.
	if err := <-errCh; err != nil {
		return err
	}

	slog.Info("[server] listening", "addr", s.listener.Addr().String())
	return s.server.Serve(s.listener)
}

// StartAsync starts the server in a non-blocking fashion, returning once
// the model is loaded and the server is ready. The engine's decode loop
// and HTTP server run in background goroutines.
func (s *Server) StartAsync(ctx context.Context) error {
	slog.Info("[server] loading model...")
	if err := s.engine.Load(); err != nil {
		return fmt.Errorf("load model: %w", err)
	}
	slog.Info("[server] model loaded")

	runCtx, cancel := context.WithCancel(ctx)
	runDone := make(chan struct{})
	s.mu.Lock()
	s.runCancel = cancel
	s.runDone = runDone
	s.mu.Unlock()
	go func() {
		defer close(runDone)
		s.engine.Run(runCtx)
	}()
	go func() {
		if err := s.server.Serve(s.listener); err != nil && err != http.ErrServerClosed {
			slog.Error("[server] serve error", "err", err)
		}
	}()

	slog.Info("[server] serving", "addr", s.listener.Addr().String())
	return nil
}

// Stop gracefully shuts down the HTTP server and closes the engine.
func (s *Server) Stop() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stopped {
		return nil
	}
	s.stopped = true

	slog.Info("[server] shutting down...")

	// Give in-flight requests 30 seconds to finish.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	runCancel := s.runCancel
	runDone := s.runDone
	s.runCancel = nil
	s.runDone = nil

	var firstErr error
	if err := s.server.Shutdown(ctx); err != nil {
		firstErr = err
	}
	if runCancel != nil {
		runCancel()
	}
	s.engine.RequestStop()
	if runDone != nil {
		select {
		case <-runDone:
		case <-time.After(10 * time.Second):
			slog.Warn("[server] decode loop did not exit within timeout, force-closing engine")
		}
	}
	s.engine.Close()
	return firstErr
}

// Addr returns the actual address the server is listening on.
// Useful when the server was started with ":0" (ephemeral port).
func (s *Server) Addr() net.Addr {
	if s.listener != nil {
		return s.listener.Addr()
	}
	return nil
}

// Engine returns the underlying inference engine.
// Returns the Inference interface; callers that need the concrete
// *engine.Engine can type-assert: eng := srv.Engine().(*engine.Engine).
func (s *Server) Engine() Inference {
	return s.engine
}
