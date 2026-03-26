package task

import (
	"encoding/base64"
	"strings"
)

func decodeStrictBase64(input string) ([]byte, error) {
	trimmed := strings.TrimSpace(input)
	if strings.HasPrefix(trimmed, "data:") {
		if comma := strings.Index(trimmed, ","); comma >= 0 {
			trimmed = trimmed[comma+1:]
		}
	}
	return base64.StdEncoding.DecodeString(trimmed)
}
