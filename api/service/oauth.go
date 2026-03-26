package service

// OAuth device-code flow for portal-auth model presets.

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/kocort/kocort/api/presets"
	"github.com/kocort/kocort/api/types"
	"github.com/kocort/kocort/runtime"
)

// ─────────────────────────────────────────────────────────────────────────────
// PKCE helpers
// ─────────────────────────────────────────────────────────────────────────────

func generatePKCEVerifier() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

func generatePKCEChallenge(verifier string) string {
	hash := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(hash[:])
}

// ─────────────────────────────────────────────────────────────────────────────
// In-memory pending session store
// ─────────────────────────────────────────────────────────────────────────────

type pendingOAuth struct {
	PresetID   string
	DeviceCode string
	Verifier   string
	TokenURL   string
	ClientID   string
	ExpiresAt  time.Time
	Interval   int
}

var (
	pendingOAuthMu       sync.Mutex
	pendingOAuthSessions = map[string]*pendingOAuth{} // key = sessionID (random)
)

// ─────────────────────────────────────────────────────────────────────────────
// Device code request
// ─────────────────────────────────────────────────────────────────────────────

// OAuthDeviceCodeRequest initiates a device-code OAuth flow for the given preset.
func OAuthDeviceCodeRequest(req types.OAuthDeviceCodeStartRequest) (*types.OAuthDeviceCodeStartResponse, error) {
	presetID := strings.TrimSpace(req.PresetID)
	preset, ok := presets.Find(presetID)
	if !ok {
		return nil, fmt.Errorf("unknown preset %q", presetID)
	}
	if preset.AuthKind != "oauth-device-code" {
		return nil, fmt.Errorf("preset %q does not support OAuth device-code auth", presetID)
	}
	if preset.OAuthConfig == nil {
		return nil, fmt.Errorf("preset %q has no OAuth configuration", presetID)
	}
	oc := preset.OAuthConfig

	verifier := generatePKCEVerifier()
	challenge := generatePKCEChallenge(verifier)

	formData := url.Values{
		"client_id":             {oc.ClientID},
		"scope":                 {oc.Scope},
		"code_challenge":        {challenge},
		"code_challenge_method": {"S256"},
	}

	resp, err := http.PostForm(oc.DeviceCodeURL, formData)
	if err != nil {
		return nil, fmt.Errorf("device code request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading device code response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("device code request returned %d: %s", resp.StatusCode, string(body))
	}

	var dcResp struct {
		DeviceCode              string `json:"device_code"`
		UserCode                string `json:"user_code"`
		VerificationURI         string `json:"verification_uri"`
		VerificationURIComplete string `json:"verification_uri_complete"`
		ExpiresIn               int    `json:"expires_in"`
		Interval                int    `json:"interval"`
	}
	if err := json.Unmarshal(body, &dcResp); err != nil {
		return nil, fmt.Errorf("parsing device code response: %w", err)
	}
	if dcResp.DeviceCode == "" || dcResp.VerificationURI == "" {
		return nil, fmt.Errorf("incomplete device code response")
	}

	interval := dcResp.Interval
	if interval <= 0 {
		interval = 5
	}

	// Generate a session ID and store the pending session
	sessionID := generateSessionID()
	pendingOAuthMu.Lock()
	pendingOAuthSessions[sessionID] = &pendingOAuth{
		PresetID:   presetID,
		DeviceCode: dcResp.DeviceCode,
		Verifier:   verifier,
		TokenURL:   oc.TokenURL,
		ClientID:   oc.ClientID,
		ExpiresAt:  time.Now().Add(time.Duration(dcResp.ExpiresIn) * time.Second),
		Interval:   interval,
	}
	pendingOAuthMu.Unlock()

	verifyURL := dcResp.VerificationURIComplete
	if verifyURL == "" {
		verifyURL = dcResp.VerificationURI
	}

	return &types.OAuthDeviceCodeStartResponse{
		SessionID:       sessionID,
		UserCode:        dcResp.UserCode,
		VerificationURL: verifyURL,
		ExpiresIn:       dcResp.ExpiresIn,
		Interval:        interval,
	}, nil
}

func generateSessionID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return fmt.Sprintf("%x", b)
}

// ─────────────────────────────────────────────────────────────────────────────
// Poll for token
// ─────────────────────────────────────────────────────────────────────────────

// OAuthDeviceCodePoll polls the OAuth provider for a token.
func OAuthDeviceCodePoll(rt *runtime.Runtime, req types.OAuthDeviceCodePollRequest) (*types.OAuthDeviceCodePollResponse, error) {
	sessionID := strings.TrimSpace(req.SessionID)

	pendingOAuthMu.Lock()
	session, ok := pendingOAuthSessions[sessionID]
	pendingOAuthMu.Unlock()

	if !ok {
		return nil, fmt.Errorf("unknown OAuth session %q", sessionID)
	}

	if time.Now().After(session.ExpiresAt) {
		pendingOAuthMu.Lock()
		delete(pendingOAuthSessions, sessionID)
		pendingOAuthMu.Unlock()
		return &types.OAuthDeviceCodePollResponse{
			Status: "expired",
			Error:  "OAuth session expired",
		}, nil
	}

	formData := url.Values{
		"grant_type":    {"urn:ietf:params:oauth:grant-type:device_code"},
		"client_id":     {session.ClientID},
		"device_code":   {session.DeviceCode},
		"code_verifier": {session.Verifier},
	}

	resp, err := http.PostForm(session.TokenURL, formData)
	if err != nil {
		return &types.OAuthDeviceCodePollResponse{
			Status: "error",
			Error:  fmt.Sprintf("token request failed: %v", err),
		}, nil
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return &types.OAuthDeviceCodePollResponse{
			Status: "error",
			Error:  "reading token response failed",
		}, nil
	}

	var tokenResp struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int    `json:"expires_in"`
		ResourceURL  string `json:"resource_url"`
		Error        string `json:"error"`
		ErrorDesc    string `json:"error_description"`
	}
	if err := json.Unmarshal(body, &tokenResp); err != nil {
		return &types.OAuthDeviceCodePollResponse{
			Status: "error",
			Error:  "parsing token response failed",
		}, nil
	}

	if tokenResp.Error == "authorization_pending" {
		return &types.OAuthDeviceCodePollResponse{Status: "pending"}, nil
	}
	if tokenResp.Error == "slow_down" {
		return &types.OAuthDeviceCodePollResponse{Status: "pending"}, nil
	}
	if tokenResp.Error != "" {
		return &types.OAuthDeviceCodePollResponse{
			Status: "error",
			Error:  tokenResp.ErrorDesc,
		}, nil
	}

	if resp.StatusCode != http.StatusOK {
		return &types.OAuthDeviceCodePollResponse{
			Status: "error",
			Error:  fmt.Sprintf("token endpoint returned %d", resp.StatusCode),
		}, nil
	}

	if tokenResp.AccessToken == "" {
		return &types.OAuthDeviceCodePollResponse{
			Status: "error",
			Error:  "incomplete token response",
		}, nil
	}

	// Success — store the credential and configure the model
	preset, _ := presets.Find(session.PresetID)
	cred := types.OAuthCredential{
		ProviderID:   preset.ID,
		AccessToken:  tokenResp.AccessToken,
		RefreshToken: tokenResp.RefreshToken,
		ExpiresAt:    time.Now().Add(time.Duration(tokenResp.ExpiresIn) * time.Second).Unix(),
	}

	if err := SaveOAuthCredential(rt, cred); err != nil {
		return &types.OAuthDeviceCodePollResponse{
			Status: "error",
			Error:  fmt.Sprintf("saving credential: %v", err),
		}, nil
	}

	// Clean up pending session
	pendingOAuthMu.Lock()
	delete(pendingOAuthSessions, sessionID)
	pendingOAuthMu.Unlock()

	resourceURL := tokenResp.ResourceURL
	if resourceURL != "" {
		// Use resource URL as base URL if provided
	}

	return &types.OAuthDeviceCodePollResponse{
		Status:      "success",
		AccessToken: tokenResp.AccessToken,
		BaseURL:     normalizeOAuthBaseURL(preset.BaseURL, resourceURL),
	}, nil
}

func normalizeOAuthBaseURL(defaultBase, resourceURL string) string {
	raw := strings.TrimSpace(resourceURL)
	if raw == "" {
		return defaultBase
	}
	// Ensure the URL has a protocol prefix.
	if !strings.HasPrefix(raw, "http") {
		raw = "https://" + raw
	}
	// Ensure the URL ends with /v1 (matching openclaw normalizeBaseUrl).
	raw = strings.TrimRight(raw, "/")
	if !strings.HasSuffix(raw, "/v1") {
		raw += "/v1"
	}
	return raw
}

// ─────────────────────────────────────────────────────────────────────────────
// Credential persistence
// ─────────────────────────────────────────────────────────────────────────────

func oauthCredentialPath(rt *runtime.Runtime) string {
	// Store alongside session data dir
	base := ""
	if rt.Sessions != nil {
		base = rt.Sessions.BaseDir()
	}
	if base == "" {
		home, _ := os.UserHomeDir()
		base = filepath.Join(home, ".kocort")
	}
	return filepath.Join(base, "oauth-credentials.json")
}

// SaveOAuthCredential saves an OAuth credential to disk.
func SaveOAuthCredential(rt *runtime.Runtime, cred types.OAuthCredential) error {
	creds, _ := LoadOAuthCredentials(rt)

	// Upsert
	found := false
	for i := range creds {
		if creds[i].ProviderID == cred.ProviderID {
			creds[i] = cred
			found = true
			break
		}
	}
	if !found {
		creds = append(creds, cred)
	}

	data, err := json.MarshalIndent(creds, "", "  ")
	if err != nil {
		return err
	}
	path := oauthCredentialPath(rt)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

// LoadOAuthCredentials loads all stored OAuth credentials.
func LoadOAuthCredentials(rt *runtime.Runtime) ([]types.OAuthCredential, error) {
	path := oauthCredentialPath(rt)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var creds []types.OAuthCredential
	if err := json.Unmarshal(data, &creds); err != nil {
		return nil, err
	}
	return creds, nil
}

// GetOAuthCredential returns the stored credential for a provider, or nil.
func GetOAuthCredential(rt *runtime.Runtime, providerID string) *types.OAuthCredential {
	creds, err := LoadOAuthCredentials(rt)
	if err != nil {
		return nil
	}
	for _, c := range creds {
		if c.ProviderID == providerID {
			return &c
		}
	}
	return nil
}

// RefreshOAuthToken attempts to refresh an OAuth token using the refresh token.
func RefreshOAuthToken(rt *runtime.Runtime, providerID string) (*types.OAuthCredential, error) {
	cred := GetOAuthCredential(rt, providerID)
	if cred == nil {
		return nil, fmt.Errorf("no OAuth credential for provider %q", providerID)
	}

	preset, ok := presets.Find(providerID)
	if !ok || preset.OAuthConfig == nil {
		return nil, fmt.Errorf("no OAuth configuration for provider %q", providerID)
	}

	formData := url.Values{
		"grant_type":    {"refresh_token"},
		"client_id":     {preset.OAuthConfig.ClientID},
		"refresh_token": {cred.RefreshToken},
	}

	resp, err := http.PostForm(preset.OAuthConfig.TokenURL, formData)
	if err != nil {
		return nil, fmt.Errorf("refresh request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading refresh response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("refresh failed (%d): %s", resp.StatusCode, string(body))
	}

	var tokenResp struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int    `json:"expires_in"`
	}
	if err := json.Unmarshal(body, &tokenResp); err != nil {
		return nil, fmt.Errorf("parsing refresh response: %w", err)
	}
	if tokenResp.AccessToken == "" {
		return nil, fmt.Errorf("refresh returned empty access token")
	}

	newCred := types.OAuthCredential{
		ProviderID:   providerID,
		AccessToken:  tokenResp.AccessToken,
		RefreshToken: tokenResp.RefreshToken,
		ExpiresAt:    time.Now().Add(time.Duration(tokenResp.ExpiresIn) * time.Second).Unix(),
	}
	if newCred.RefreshToken == "" {
		newCred.RefreshToken = cred.RefreshToken
	}

	if err := SaveOAuthCredential(rt, newCred); err != nil {
		return nil, fmt.Errorf("saving refreshed credential: %w", err)
	}
	return &newCred, nil
}

// DeleteOAuthCredential removes the stored credential for a provider.
func DeleteOAuthCredential(rt *runtime.Runtime, providerID string) error {
	creds, _ := LoadOAuthCredentials(rt)
	filtered := make([]types.OAuthCredential, 0, len(creds))
	for _, c := range creds {
		if c.ProviderID != providerID {
			filtered = append(filtered, c)
		}
	}
	data, err := json.MarshalIndent(filtered, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(oauthCredentialPath(rt), data, 0o600)
}
