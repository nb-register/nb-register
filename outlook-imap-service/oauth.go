package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

type OAuthManager struct {
	mu           sync.Mutex
	refreshToken string
	accessToken  string
	expiresAt    time.Time
	clientID     string
	scope        string
	tokenURL     string
	httpClient   *http.Client
}

func NewOAuthManager(refreshToken string) *OAuthManager {
	timeout := envInt("OUTLOOK_HTTP_TIMEOUT_SECONDS", defaultHTTPTimeoutSeconds)
	if timeout <= 0 {
		timeout = defaultHTTPTimeoutSeconds
	}
	scope := normalizeScope(envStr("OUTLOOK_OAUTH_SCOPE", defaultOAuthScope))
	if scope == "" {
		scope = defaultOAuthScope
	}
	return &OAuthManager{
		refreshToken: strings.TrimSpace(refreshToken),
		clientID:     envStr("OUTLOOK_OAUTH_CLIENT_ID", defaultOAuthClientID),
		scope:        scope,
		tokenURL:     envStr("OUTLOOK_OAUTH_TOKEN_URL", defaultTokenURL),
		httpClient:   &http.Client{Timeout: time.Duration(timeout) * time.Second},
	}
}

func (m *OAuthManager) GetAccessToken(ctx context.Context) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.accessToken != "" && time.Now().Before(m.expiresAt.Add(-60*time.Second)) {
		return m.accessToken, nil
	}
	return m.refreshLocked(ctx)
}

func (m *OAuthManager) RefreshAccessToken(ctx context.Context) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.refreshLocked(ctx)
}

func (m *OAuthManager) CurrentTokens() (string, string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.refreshToken, m.accessToken
}

func (m *OAuthManager) refreshLocked(ctx context.Context) (string, error) {
	if strings.TrimSpace(m.refreshToken) == "" {
		return "", fmt.Errorf("refresh token is missing")
	}
	form := url.Values{}
	form.Set("grant_type", "refresh_token")
	form.Set("client_id", m.clientID)
	form.Set("refresh_token", m.refreshToken)
	form.Set("scope", m.scope)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, m.tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := m.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		body = map[string]any{"raw": string(raw)}
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("token refresh failed: %v", body)
	}
	accessToken, _ := body["access_token"].(string)
	if accessToken == "" {
		return "", fmt.Errorf("token refresh returned empty access token")
	}
	m.accessToken = accessToken
	if nextRefreshToken, _ := body["refresh_token"].(string); nextRefreshToken != "" {
		m.refreshToken = nextRefreshToken
	}
	m.expiresAt = time.Now().Add(time.Duration(expiresInSeconds(body["expires_in"])) * time.Second)
	return m.accessToken, nil
}

func expiresInSeconds(value any) int64 {
	switch v := value.(type) {
	case float64:
		if v > 0 {
			return int64(v)
		}
	case string:
		n, err := strconv.ParseInt(v, 10, 64)
		if err == nil && n > 0 {
			return n
		}
	}
	return 3600
}
