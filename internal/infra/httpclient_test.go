package infra

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestDynamicHTTPClient_DirectConnection(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	dc := NewDynamicHTTPClient(&StaticProxyProvider{URL: ""}, 5*time.Second)
	resp, err := dc.Client().Get(ts.URL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	if got := dc.Client().Timeout; got != 0 {
		t.Fatalf("expected shared dynamic client to have no timeout, got %s", got)
	}
}

func TestDynamicHTTPClient_CachesClient(t *testing.T) {
	dc := NewDynamicHTTPClient(&StaticProxyProvider{URL: "http://proxy:8080"}, 5*time.Second)
	c1 := dc.Client()
	c2 := dc.Client()
	if c1 != c2 {
		t.Fatal("expected same cached client")
	}
}

type mutableProxy struct{ url string }

func (p *mutableProxy) EffectiveProxyURL() string { return p.url }

func TestDynamicHTTPClient_UpdatesOnProxyChange(t *testing.T) {
	p := &mutableProxy{url: "http://proxy1:8080"}
	dc := NewDynamicHTTPClient(p, 5*time.Second)
	c1 := dc.Client()

	// Change proxy → should get a new client.
	p.url = "http://proxy2:9090"
	c2 := dc.Client()
	if c1 == c2 {
		t.Fatal("expected new client after proxy change")
	}
}

func TestDynamicHTTPClient_NilProvider(t *testing.T) {
	dc := NewDynamicHTTPClient(nil, 5*time.Second)
	c := dc.Client()
	if c == nil {
		t.Fatal("client should not be nil even with nil provider")
	}
}

func TestDynamicHTTPClient_ClientWithTimeout(t *testing.T) {
	dc := NewDynamicHTTPClient(&StaticProxyProvider{URL: ""}, 5*time.Second)
	c := dc.ClientWithTimeout(0)
	if c == nil {
		t.Fatal("client should not be nil")
	}
	if c.Timeout != 0 {
		t.Fatalf("expected zero timeout for download client, got %s", c.Timeout)
	}
	if timed := dc.ClientWithTimeout(5 * time.Second); timed.Timeout != 5*time.Second {
		t.Fatalf("expected explicit timeout client to use 5s, got %s", timed.Timeout)
	}
	if cached := dc.Client(); cached.Timeout != 0 {
		t.Fatalf("expected cached shared client timeout to remain 0, got %s", cached.Timeout)
	}
}

func TestStaticProxyProvider(t *testing.T) {
	p := &StaticProxyProvider{URL: "http://example.com:8080"}
	if got := p.EffectiveProxyURL(); got != "http://example.com:8080" {
		t.Fatalf("expected http://example.com:8080, got %s", got)
	}
}
