// Package infra provides infrastructure utilities for the kocort runtime.
//
// This file implements a dynamic proxy-aware HTTP client that reads the
// effective proxy URL from a shared ProxyProvider on every request.
// All components should use this client instead of managing their own
// proxy URLs — when the proxy configuration changes (via the UI or API),
// subsequent HTTP requests automatically pick up the new setting.
package infra

import (
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// ---------------------------------------------------------------------------
// ProxyProvider — the contract that the config layer implements
// ---------------------------------------------------------------------------

// ProxyProvider returns the effective proxy URL for outgoing HTTP requests.
// The returned value follows the same convention as
// config.NetworkConfig.EffectiveProxyURL:
//
//	""           → direct connection (no proxy)
//	"__SYSTEM__" → use http.ProxyFromEnvironment
//	"http://…"   → explicit proxy URL
type ProxyProvider interface {
	EffectiveProxyURL() string
}

// ---------------------------------------------------------------------------
// StaticProxyProvider — a trivial implementation for tests / one-shot use
// ---------------------------------------------------------------------------

// StaticProxyProvider implements ProxyProvider with a fixed value.
type StaticProxyProvider struct {
	URL string
}

// EffectiveProxyURL returns the static URL.
func (p *StaticProxyProvider) EffectiveProxyURL() string { return p.URL }

// ---------------------------------------------------------------------------
// AtomicProxyProvider — a thread-safe mutable proxy provider
// ---------------------------------------------------------------------------

// AtomicProxyProvider is a ProxyProvider whose value can be updated at
// any time. It is safe for concurrent use. Typical usage: the runtime
// creates one on startup; when ApplyConfig is called, it calls Set(...)
// with the new effective proxy URL. All components sharing the same
// DynamicHTTPClient automatically pick up the change.
type AtomicProxyProvider struct {
	mu  sync.RWMutex
	url string
}

// NewAtomicProxyProvider creates a new AtomicProxyProvider with the given
// initial proxy URL.
func NewAtomicProxyProvider(initial string) *AtomicProxyProvider {
	return &AtomicProxyProvider{url: strings.TrimSpace(initial)}
}

// EffectiveProxyURL returns the current proxy URL.
func (p *AtomicProxyProvider) EffectiveProxyURL() string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.url
}

// Set updates the proxy URL. All DynamicHTTPClient instances that reference
// this provider will pick up the change on their next request.
func (p *AtomicProxyProvider) Set(proxyURL string) {
	p.mu.Lock()
	p.url = strings.TrimSpace(proxyURL)
	p.mu.Unlock()
}

// ---------------------------------------------------------------------------
// DynamicHTTPClient — the global proxy-aware HTTP client
// ---------------------------------------------------------------------------

// DynamicHTTPClient is an HTTP client whose proxy setting is resolved
// dynamically from a ProxyProvider on every outgoing request.
// It is safe for concurrent use.
type DynamicHTTPClient struct {
	provider ProxyProvider

	// Cache: avoids rebuilding the transport when the proxy hasn't changed.
	mu           sync.Mutex
	cachedProxy  string
	cachedClient *http.Client
}

// NewDynamicHTTPClient creates a new dynamic HTTP client.
// If provider is nil, all requests use a direct connection.
// The timeout parameter is kept for backward compatibility but is ignored:
// the shared global client has no total timeout, and callers that need one
// should explicitly use ClientWithTimeout.
func NewDynamicHTTPClient(provider ProxyProvider, _ time.Duration) *DynamicHTTPClient {
	return &DynamicHTTPClient{
		provider: provider,
	}
}

func (d *DynamicHTTPClient) currentProxyURL() string {
	if d.provider == nil {
		return ""
	}
	return strings.TrimSpace(d.provider.EffectiveProxyURL())
}

// NewDynamicHTTPClientFromClient wraps an existing *http.Client in a
// DynamicHTTPClient. The provider is nil, so the cached client is returned
// as-is on every call. This is intended for testing where callers need to
// inject a mock transport.
func NewDynamicHTTPClientFromClient(c *http.Client) *DynamicHTTPClient {
	return &DynamicHTTPClient{
		cachedClient: c,
		cachedProxy:  "",
	}
}

// Client returns an *http.Client configured with the current proxy setting.
// The returned client is cached and reused as long as the proxy URL has not
// changed, so callers may invoke this on every request without cost.
// The shared client intentionally has no total timeout; callers that need a
// deadline should supply a request context or use ClientWithTimeout.
func (d *DynamicHTTPClient) Client() *http.Client {
	proxyURL := d.currentProxyURL()

	d.mu.Lock()
	defer d.mu.Unlock()

	if d.cachedClient != nil && d.cachedProxy == proxyURL {
		return d.cachedClient
	}

	d.cachedProxy = proxyURL
	d.cachedClient = buildHTTPClient(proxyURL, 0)
	return d.cachedClient
}

// ClientWithTimeout returns an *http.Client configured with the current proxy
// setting and the provided timeout. A zero timeout disables the client's
// overall deadline, which is useful for large streaming downloads that should
// be controlled only by request context cancellation.
func (d *DynamicHTTPClient) ClientWithTimeout(timeout time.Duration) *http.Client {
	return buildHTTPClient(d.currentProxyURL(), timeout)
}

// Do executes an HTTP request using the dynamic proxy.
func (d *DynamicHTTPClient) Do(req *http.Request) (*http.Response, error) {
	return d.Client().Do(req)
}

// ---------------------------------------------------------------------------
// Helper: build an *http.Client for the given proxy URL
// ---------------------------------------------------------------------------

func buildHTTPClient(proxyURL string, timeout time.Duration) *http.Client {
	proxyURL = strings.TrimSpace(proxyURL)
	transport := http.DefaultTransport.(*http.Transport).Clone()

	if proxyURL == "__SYSTEM__" {
		transport.Proxy = http.ProxyFromEnvironment
		return &http.Client{
			Timeout:   timeout,
			Transport: transport,
		}
	}
	if proxyURL == "" {
		return &http.Client{Timeout: timeout, Transport: transport}
	}
	parsed, err := url.Parse(proxyURL)
	if err != nil || parsed.Host == "" {
		return &http.Client{Timeout: timeout, Transport: transport}
	}
	transport.Proxy = http.ProxyURL(parsed)
	return &http.Client{
		Timeout:   timeout,
		Transport: transport,
	}
}
