package handlers

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/kocort/kocort/internal/core"
)

func writeStructuredError(c *gin.Context, status int, code string, err error) {
	c.JSON(status, gin.H{
		"error":   code,
		"message": err.Error(),
	})
}

func writeChatError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, core.ErrNoDefaultModelConfigured):
		writeStructuredError(c, http.StatusBadRequest, "NO_DEFAULT_MODEL", err)
	default:
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
	}
}
