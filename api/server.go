package api

import (
	"context"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/kocort/kocort/internal/config"
	gw "github.com/kocort/kocort/internal/gateway"
	"github.com/kocort/kocort/runtime"
)

type Server struct {
	Runtime *runtime.Runtime
	Config  config.GatewayConfig
}

func NewServer(rt *runtime.Runtime, cfg config.GatewayConfig) *Server {
	return &Server{Runtime: rt, Config: cfg}
}

func (s *Server) Addr() string {
	return gw.ResolveAddr(s.Config.Bind, s.Config.Port)
}

func (s *Server) Start(ctx context.Context) error {
	return gw.ListenAndServe(ctx, s.Addr(), s.Handler())
}

func (s *Server) Handler() http.Handler {
	if s.Runtime == nil {
		return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "runtime is required", http.StatusInternalServerError)
		})
	}
	gin.SetMode(gin.ReleaseMode)
	engine := gin.New()
	engine.RedirectTrailingSlash = false
	engine.RedirectFixedPath = false
	engine.Use(gin.Recovery())
	engine.Use(s.corsMiddleware())
	engine.Use(s.authMiddleware())
	s.registerRoutes(engine)
	return engine
}
