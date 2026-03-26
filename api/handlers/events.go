package handlers

// SSE events handler for chat streaming.

import (
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	gw "github.com/kocort/kocort/internal/gateway"
	"github.com/kocort/kocort/runtime"
)

// Events holds dependencies for events handler.
type Events struct {
	Runtime *runtime.Runtime
}

// Serve handles GET /api/workspace/chat/events and GET /rpc/chat.events (SSE).
func (h *Events) Serve(c *gin.Context) {
	if h.Runtime == nil || h.Runtime.EventHub == nil {
		c.String(http.StatusNotImplemented, "webchat is not configured")
		return
	}
	sessionKey := strings.TrimSpace(c.Query("sessionKey"))
	if sessionKey == "" {
		c.String(http.StatusBadRequest, "missing sessionKey")
		return
	}
	w := c.Writer
	r := c.Request
	flusher, ok := w.(http.Flusher)
	if !ok {
		c.String(http.StatusInternalServerError, "streaming is not supported")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	_, _ = w.Write([]byte(": connected\n\n"))
	flusher.Flush()

	events, cancel := h.Runtime.EventHub.Subscribe(sessionKey)
	defer cancel()

	ticker := time.NewTicker(20 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
			_, _ = w.Write([]byte(": ping\n\n"))
			flusher.Flush()
		case event, ok := <-events:
			if !ok {
				return
			}
			body, err := gw.EncodeSSEEvent(event)
			if err != nil {
				continue
			}
			eventName := strings.TrimSpace(event.Event)
			if eventName == "" {
				eventName = "message"
			}
			_, _ = w.Write([]byte("event: " + eventName + "\n"))
			_, _ = w.Write([]byte("data: "))
			_, _ = w.Write(body)
			_, _ = w.Write([]byte("\n\n"))
			flusher.Flush()
		}
	}
}