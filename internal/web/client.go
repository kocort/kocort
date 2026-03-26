package web

import (
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/kocort/kocort/internal/infra"
)

const (
	DefaultFetchTimeout  = 20 * time.Second
	DefaultSearchTimeout = 20 * time.Second
	DefaultSearchURL     = "https://api.search.brave.com/res/v1/web/search"
	DefaultUserAgent     = "kocort/1.0 (+https://github.com/kocort/kocort parity)"
)

type Client struct {
	httpClient *http.Client
	dynamic    *infra.DynamicHTTPClient
}

// NewDynamicClient creates a Client that resolves proxy settings dynamically.
// Each HTTP request uses the current proxy from the DynamicHTTPClient.
func NewDynamicClient(dc *infra.DynamicHTTPClient) *Client {
	return &Client{dynamic: dc}
}

// Deprecated: NewClient creates a Client with a static proxy URL.
// Prefer NewDynamicClient for proxy-aware usage.
func NewClient(proxyURL string, timeout time.Duration) *Client {
	if timeout <= 0 {
		timeout = DefaultFetchTimeout
	}
	client := infra.NewProxyHTTPClient(proxyURL)
	client.Timeout = timeout
	return &Client{httpClient: client}
}

func NewClientWithHTTPClient(client *http.Client) *Client {
	if client == nil {
		client = &http.Client{Timeout: DefaultFetchTimeout}
	}
	return &Client{httpClient: client}
}

// client returns the underlying *http.Client. If a DynamicHTTPClient is
// present, it returns the dynamically-resolved client; otherwise the static one.
func (c *Client) client() *http.Client {
	if c.dynamic != nil {
		return c.dynamic.Client()
	}
	if c.httpClient != nil {
		return c.httpClient
	}
	return &http.Client{Timeout: DefaultFetchTimeout}
}

type FetchResult struct {
	URL         string `json:"url"`
	FinalURL    string `json:"finalUrl,omitempty"`
	Status      int    `json:"status"`
	ContentType string `json:"contentType,omitempty"`
	Title       string `json:"title,omitempty"`
	Text        string `json:"text"`
}

func (c *Client) Fetch(fetchURL string, userAgent string, maxChars int) (FetchResult, error) {
	if strings.TrimSpace(fetchURL) == "" {
		return FetchResult{}, fmt.Errorf("url is required")
	}
	if maxChars <= 0 {
		maxChars = 12000
	}
	req, err := http.NewRequest(http.MethodGet, fetchURL, nil)
	if err != nil {
		return FetchResult{}, err
	}
	req.Header.Set("User-Agent", nonEmpty(strings.TrimSpace(userAgent), DefaultUserAgent))
	resp, err := c.client().Do(req)
	if err != nil {
		return FetchResult{}, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, int64(maxChars*4)))
	if err != nil {
		return FetchResult{}, err
	}
	text, title := extractReadableText(string(body))
	if len(text) > maxChars {
		text = text[:maxChars]
	}
	return FetchResult{
		URL:         fetchURL,
		FinalURL:    resp.Request.URL.String(),
		Status:      resp.StatusCode,
		ContentType: resp.Header.Get("Content-Type"),
		Title:       title,
		Text:        text,
	}, nil
}

type SearchResult struct {
	Title       string `json:"title"`
	URL         string `json:"url"`
	Description string `json:"description,omitempty"`
}

func (c *Client) SearchBrave(endpoint string, apiKey string, query string, count int) ([]SearchResult, error) {
	if strings.TrimSpace(apiKey) == "" {
		return nil, fmt.Errorf("BRAVE_SEARCH_API_KEY is not configured")
	}
	if strings.TrimSpace(query) == "" {
		return nil, fmt.Errorf("query is required")
	}
	if count <= 0 {
		count = 5
	}
	base := nonEmpty(strings.TrimSpace(endpoint), DefaultSearchURL)
	u, err := url.Parse(base)
	if err != nil {
		return nil, err
	}
	q := u.Query()
	q.Set("q", query)
	q.Set("count", fmt.Sprintf("%d", count))
	u.RawQuery = q.Encode()
	req, err := http.NewRequest(http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", DefaultUserAgent)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-Subscription-Token", apiKey)
	resp, err := c.client().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return nil, fmt.Errorf("search request failed: %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
	var payload struct {
		Web struct {
			Results []struct {
				Title       string `json:"title"`
				URL         string `json:"url"`
				Description string `json:"description"`
			} `json:"results"`
		} `json:"web"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, err
	}
	results := make([]SearchResult, 0, len(payload.Web.Results))
	for _, item := range payload.Web.Results {
		results = append(results, SearchResult{
			Title:       strings.TrimSpace(item.Title),
			URL:         strings.TrimSpace(item.URL),
			Description: strings.TrimSpace(item.Description),
		})
	}
	return results, nil
}

var (
	scriptStyleRE = regexp.MustCompile(`(?is)<(script|style)[^>]*>.*?</(script|style)>`)
	tagRE         = regexp.MustCompile(`(?s)<[^>]+>`)
	titleRE       = regexp.MustCompile(`(?is)<title[^>]*>(.*?)</title>`)
	spaceRE       = regexp.MustCompile(`[ \t\f\v]+`)
	multiNLRE     = regexp.MustCompile(`\n{3,}`)
)

func extractReadableText(raw string) (string, string) {
	title := ""
	if m := titleRE.FindStringSubmatch(raw); len(m) == 2 {
		title = strings.TrimSpace(html.UnescapeString(tagRE.ReplaceAllString(m[1], "")))
	}
	cleaned := scriptStyleRE.ReplaceAllString(raw, " ")
	cleaned = strings.ReplaceAll(cleaned, "\r\n", "\n")
	cleaned = tagRE.ReplaceAllString(cleaned, "\n")
	cleaned = html.UnescapeString(cleaned)
	lines := strings.Split(cleaned, "\n")
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(spaceRE.ReplaceAllString(line, " "))
		if line == "" {
			continue
		}
		out = append(out, line)
	}
	text := strings.Join(out, "\n")
	text = multiNLRE.ReplaceAllString(text, "\n\n")
	return strings.TrimSpace(text), title
}

func nonEmpty(value string, fallback string) string {
	if strings.TrimSpace(value) != "" {
		return value
	}
	return fallback
}
