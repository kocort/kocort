package api

import (
	"embed"
	"io"
	"io/fs"
	"net/http"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

//go:embed all:static/dist
var embeddedWebFS embed.FS

//go:embed all:static_fallback
var embeddedFallbackWebFS embed.FS

func resolveEmbeddedStaticFS() fs.FS {
	distFS, err := fs.Sub(embeddedWebFS, "static/dist")
	if err == nil && embeddedStaticFSUsable(distFS) {
		return distFS
	}
	fallbackFS, err := fs.Sub(embeddedFallbackWebFS, "static_fallback")
	if err != nil {
		panic(err)
	}
	return fallbackFS
}

func embeddedStaticFSUsable(webFS fs.FS) bool {
	if _, err := fs.Stat(webFS, "index.html"); err != nil {
		return false
	}
	entries, err := fs.ReadDir(webFS, ".")
	if err != nil {
		return false
	}
	for _, entry := range entries {
		name := entry.Name()
		if name == ".gitkeep" {
			continue
		}
		if entry.IsDir() {
			return true
		}
		if name != "index.html" && name != "404.html" {
			return true
		}
	}
	return false
}

func (s *Server) registerStaticRoutes(engine *gin.Engine) {
	distFS := resolveEmbeddedStaticFS()
	fileServer := http.FileServer(http.FS(distFS))

	engine.GET("/", s.serveEmbeddedIndex(distFS))

	entries, err := fs.ReadDir(distFS, ".")
	if err != nil {
		panic(err)
	}
	for _, entry := range entries {
		name := entry.Name()
		if name == "index.html" || name == "404.html" || name == ".gitkeep" {
			continue
		}
		routePath := "/" + name
		if entry.IsDir() {
			engine.GET(routePath+"/*filepath", s.serveEmbeddedAsset(fileServer))
			continue
		}
		engine.GET(routePath, s.serveEmbeddedAsset(fileServer))
	}

	engine.NoRoute(s.serveEmbeddedFallback(distFS, fileServer))
}

func (s *Server) serveEmbeddedIndex(distFS fs.FS) gin.HandlerFunc {
	return func(c *gin.Context) {
		serveEmbeddedFile(c, distFS, "index.html")
	}
}

func (s *Server) serveEmbeddedAsset(fileServer http.Handler) gin.HandlerFunc {
	return func(c *gin.Context) {
		http.StripPrefix("/", fileServer).ServeHTTP(c.Writer, c.Request)
	}
}

func (s *Server) serveEmbeddedFallback(distFS fs.FS, fileServer http.Handler) gin.HandlerFunc {
	return func(c *gin.Context) {
		if isReservedRoute(c.Request.URL.Path) {
			c.Status(http.StatusNotFound)
			return
		}

		requestPath := strings.TrimPrefix(path.Clean(c.Request.URL.Path), "/")
		if requestPath != "" && requestPath != "." {
			if resolved, ok := resolveEmbeddedRequestPath(distFS, requestPath); ok {
				if resolved == requestPath {
					http.StripPrefix("/", fileServer).ServeHTTP(c.Writer, c.Request)
					return
				}
				serveEmbeddedFile(c, distFS, resolved)
				return
			}
			if filepath.Ext(requestPath) != "" {
				c.Status(http.StatusNotFound)
				return
			}
		}

		serveEmbeddedFile(c, distFS, "index.html")
	}
}

func resolveEmbeddedRequestPath(distFS fs.FS, requestPath string) (string, bool) {
	candidates := []string{requestPath}
	if filepath.Ext(requestPath) == "" {
		candidates = append(candidates, requestPath+".html")
	}
	for _, candidate := range candidates {
		stat, err := fs.Stat(distFS, candidate)
		if err != nil || stat.IsDir() {
			continue
		}
		return candidate, true
	}
	return "", false
}

func serveEmbeddedFile(c *gin.Context, distFS fs.FS, name string) {
	file, err := distFS.Open(name)
	if err != nil {
		c.Status(http.StatusNotFound)
		return
	}
	defer file.Close()

	stat, err := file.Stat()
	if err != nil || stat.IsDir() {
		c.Status(http.StatusNotFound)
		return
	}
	readSeeker, ok := file.(io.ReadSeeker)
	if !ok {
		c.Status(http.StatusInternalServerError)
		return
	}

	if c.ContentType() == "" {
		c.Header("Content-Type", contentTypeByName(name))
	}
	http.ServeContent(c.Writer, c.Request, stat.Name(), time.Time{}, readSeeker)
}

func contentTypeByName(name string) string {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".html":
		return "text/html; charset=utf-8"
	case ".css":
		return "text/css; charset=utf-8"
	case ".js":
		return "application/javascript; charset=utf-8"
	case ".svg":
		return "image/svg+xml"
	case ".json":
		return "application/json; charset=utf-8"
	case ".txt":
		return "text/plain; charset=utf-8"
	default:
		return "application/octet-stream"
	}
}

func isReservedRoute(requestPath string) bool {
	requestPath = strings.TrimSpace(requestPath)
	return requestPath == "/healthz" ||
		strings.HasPrefix(requestPath, "/api/") ||
		strings.HasPrefix(requestPath, "/rpc/") ||
		strings.HasPrefix(requestPath, "/channels/")
}
