package infra

import (
	"net/http"
	"net/url"
	"strings"
)

// Deprecated: NewProxyHTTPClient builds an *http.Client with a static proxy.
// Prefer DynamicHTTPClient for proxy-aware usage that reacts to config changes.
//
// NewProxyHTTPClient builds an *http.Client that routes all requests through
// the given proxy URL. If proxyURL is empty, a plain *http.Client is returned
// (i.e. no proxy). Supported schemes: http, https, socks5.
func NewProxyHTTPClient(proxyURL string) *http.Client {
	proxyURL = strings.TrimSpace(proxyURL)
	if proxyURL == "__SYSTEM__" {
		return &http.Client{
			Transport: &http.Transport{Proxy: http.ProxyFromEnvironment},
		}
	}
	if proxyURL == "" {
		return &http.Client{}
	}
	parsed, err := url.Parse(proxyURL)
	if err != nil || parsed.Host == "" {
		// Invalid URL — fall back to a plain client.
		return &http.Client{}
	}
	transport := &http.Transport{
		Proxy: http.ProxyURL(parsed),
	}
	return &http.Client{Transport: transport}
}
