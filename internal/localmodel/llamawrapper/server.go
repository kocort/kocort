package llamawrapper

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
)

// Server wraps an Engine with an HTTP server exposing OpenAI-compatible endpoints.
//
// Quick start:
//
//	srv, err := localmodel.NewServer(localmodel.ServerConfig{
//	    Addr: ":8080",
//	    EngineConfig: localmodel.EngineConfig{
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
	engine    *Engine
	server    *http.Server
	listener  net.Listener
	modelName string
	created   int64
	mu        sync.Mutex
	stopped   bool
}

// NewServer creates a new Server with the given configuration.
// The engine is initialized but the model is not yet loaded.
// Call Start() to load the model and begin serving.
func NewServer(cfg ServerConfig) (*Server, error) {
	engine, err := NewEngine(cfg.EngineConfig)
	if err != nil {
		return nil, fmt.Errorf("create engine: %w", err)
	}

	modelName := filepath.Base(cfg.EngineConfig.ModelPath)
	modelName = strings.TrimSuffix(modelName, filepath.Ext(modelName))

	s := &Server{
		engine:    engine,
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
func NewServerFromEngine(engine *Engine, addr string) (*Server, error) {
	s := &Server{
		engine:    engine,
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
		if err := s.engine.load(); err != nil {
			errCh <- fmt.Errorf("load model: %w", err)
			return
		}
		slog.Info("[server] model loaded, starting engine run loop")
		go s.engine.Run(ctx)
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
	if err := s.engine.load(); err != nil {
		return fmt.Errorf("load model: %w", err)
	}
	slog.Info("[server] model loaded")

	go s.engine.Run(ctx)
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

	var firstErr error
	if err := s.server.Shutdown(ctx); err != nil {
		firstErr = err
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

// Engine returns the underlying inference engine for programmatic use.
func (s *Server) Engine() *Engine {
	return s.engine
}
