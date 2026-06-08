package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// Authenticator attaches credentials to an HTTP request.
type Authenticator interface {
	Apply(req *http.Request) error
}

// NoOpAuth does nothing; used for open STAC APIs such as Earth Search.
type NoOpAuth struct{}

func (NoOpAuth) Apply(req *http.Request) error { return nil }

// CDSEAuth implements CDSE Keycloak OAuth2 password grant flow.
type CDSEAuth struct {
	Username string
	Password string

	mu            sync.RWMutex
	token         string
	expiresAt     time.Time
	margin        time.Duration
	tokenEndpoint string // overridden in tests
}

func NewCDSEAuth(username, password string) *CDSEAuth {
	return &CDSEAuth{
		Username: username,
		Password: password,
		margin:   30 * time.Second,
	}
}

func (o *CDSEAuth) Apply(req *http.Request) error {
	tok, err := o.tokenWithRefresh(req.Context())
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	return nil
}

func (o *CDSEAuth) tokenWithRefresh(ctx context.Context) (string, error) {
	o.mu.RLock()
	tok, valid := o.token, time.Now().Add(o.margin).Before(o.expiresAt)
	o.mu.RUnlock()
	if valid && tok != "" {
		return tok, nil
	}

	o.mu.Lock()
	defer o.mu.Unlock()
	if time.Now().Add(o.margin).Before(o.expiresAt) && o.token != "" {
		return o.token, nil
	}
	return o.fetchToken(ctx)
}

// EarthdataAuth implements NASA Earthdata Login (URS) Bearer token flow.
// Tokens are obtained via Basic Auth to the URS token endpoint and cached
// until expiry.
type EarthdataAuth struct {
	Username string
	Password string

	mu            sync.RWMutex
	token         string
	expiresAt     time.Time
	margin        time.Duration
	tokenEndpoint string // overridden in tests
}

func NewEarthdataAuth(username, password string) *EarthdataAuth {
	return &EarthdataAuth{
		Username: username,
		Password: password,
		margin:   30 * time.Second,
	}
}

func (e *EarthdataAuth) Apply(req *http.Request) error {
	req.Header.Set("Authorization", "Basic "+basicAuth(e.Username, e.Password))
	return nil
}

func (e *EarthdataAuth) tokenWithRefresh(ctx context.Context) (string, error) {
	e.mu.RLock()
	tok, valid := e.token, time.Now().Add(e.margin).Before(e.expiresAt)
	e.mu.RUnlock()
	if valid && tok != "" {
		return tok, nil
	}

	e.mu.Lock()
	defer e.mu.Unlock()
	if time.Now().Add(e.margin).Before(e.expiresAt) && e.token != "" {
		return e.token, nil
	}
	return e.fetchToken(ctx)
}

func (e *EarthdataAuth) fetchToken(ctx context.Context) (string, error) {
	const maxRetries = 3
	var lastErr error
	for attempt := 0; attempt <= maxRetries; attempt++ {
		if attempt > 0 {
			wait := time.Duration(attempt) * time.Second
			time.Sleep(wait)
		}

		tokenURL := e.tokenEndpoint
		if tokenURL == "" {
			tokenURL = "https://urs.earthdata.nasa.gov/api/users/token"
		}
		req, err := http.NewRequestWithContext(ctx, "POST", tokenURL, nil)
		if err != nil {
			return "", err
		}
		req.Header.Set("Authorization", "Basic "+basicAuth(e.Username, e.Password))

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			lastErr = fmt.Errorf("token request failed: %w", err)
			continue
		}

		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		if resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusCreated {
			var tr struct {
				AccessToken string `json:"access_token"`
				TokenType   string `json:"token_type"`
				ExpiresIn   int    `json:"expires_in"`
			}
			if err := json.Unmarshal(body, &tr); err != nil {
				// Fallback: some URS endpoints return token as plain text
				e.token = strings.TrimSpace(string(body))
				e.expiresAt = time.Now().Add(2 * time.Hour)
				return e.token, nil
			}
			e.token = tr.AccessToken
			if tr.ExpiresIn > 0 {
				e.expiresAt = time.Now().Add(time.Duration(tr.ExpiresIn) * time.Second)
			} else {
				e.expiresAt = time.Now().Add(2 * time.Hour)
			}
			return e.token, nil
		}

		lastErr = fmt.Errorf("token endpoint returned %d: %s", resp.StatusCode, string(body))
		if resp.StatusCode >= 500 || resp.StatusCode == http.StatusTooManyRequests {
			continue
		}
		break
	}
	return "", lastErr
}

func basicAuth(username, password string) string {
	auth := username + ":" + password
	return base64.StdEncoding.EncodeToString([]byte(auth))
}

func (o *CDSEAuth) fetchToken(ctx context.Context) (string, error) {
	data := url.Values{}
	data.Set("grant_type", "password")
	data.Set("client_id", "cdse-public")
	data.Set("username", o.Username)
	data.Set("password", o.Password)

	const maxRetries = 3
	var lastErr error
	for attempt := 0; attempt <= maxRetries; attempt++ {
		if attempt > 0 {
			wait := time.Duration(attempt) * time.Second
			time.Sleep(wait)
		}

		tokenURL := o.tokenEndpoint
		if tokenURL == "" {
			tokenURL = "https://identity.dataspace.copernicus.eu/auth/realms/CDSE/protocol/openid-connect/token"
		}
		req, err := http.NewRequestWithContext(ctx, "POST", tokenURL, strings.NewReader(data.Encode()))
		if err != nil {
			return "", err
		}
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			lastErr = fmt.Errorf("token request failed: %w", err)
			continue
		}

		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		if resp.StatusCode == http.StatusOK {
			var tr struct {
				AccessToken string `json:"access_token"`
				ExpiresIn   int    `json:"expires_in"`
			}
			if err := json.Unmarshal(body, &tr); err != nil {
				return "", fmt.Errorf("decode token response: %w", err)
			}
			o.token = tr.AccessToken
			o.expiresAt = time.Now().Add(time.Duration(tr.ExpiresIn) * time.Second)
			return o.token, nil
		}

		lastErr = fmt.Errorf("token endpoint returned %d: %s", resp.StatusCode, string(body))
		if resp.StatusCode >= 500 || resp.StatusCode == http.StatusTooManyRequests {
			continue
		}
		break
	}
	return "", lastErr
}
