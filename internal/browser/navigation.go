package browser

import (
	"fmt"
	"net/url"
	"strings"
)

func validateNavigationURL(raw string) error {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return fmt.Errorf("url is required")
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("invalid url: %w", err)
	}
	scheme := strings.ToLower(strings.TrimSpace(parsed.Scheme))
	switch scheme {
	case "http", "https", "about":
		return nil
	case "":
		return fmt.Errorf("url must include a scheme")
	case "javascript", "data", "file", "vbscript":
		return fmt.Errorf("navigation scheme %q is not allowed", scheme)
	default:
		return fmt.Errorf("navigation scheme %q is not supported", scheme)
	}
}
