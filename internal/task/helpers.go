package task

import (
	"strings"
	"time"

	"github.com/kocort/kocort/internal/core"
)

func nonZeroTime(value time.Time, fallback time.Time) time.Time {
	if value.IsZero() {
		return fallback
	}
	return value
}

func taskMaxInt(value int, fallback int) int {
	if value < 0 {
		return fallback
	}
	return value
}

func taskMaxInt64(value int64, fallback int64) int64 {
	if value < 0 {
		return fallback
	}
	return value
}

func ExtractFinalText(result core.AgentRunResult) string {
	for i := len(result.Payloads) - 1; i >= 0; i-- {
		if text := strings.TrimSpace(result.Payloads[i].Text); text != "" {
			return text
		}
	}
	return ""
}
