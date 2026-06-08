package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestNoOpAuth(t *testing.T) {
	auth := NoOpAuth{}
	req, _ := http.NewRequest("GET", "http://example.com", nil)
	if err := auth.Apply(req); err != nil {
		t.Fatalf("NoOpAuth.Apply should not error, got %v", err)
	}
	if req.Header.Get("Authorization") != "" {
		t.Error("NoOpAuth should not set Authorization header")
	}
}

func TestCDSEAuth_TokenCache(t *testing.T) {
	callCount := int32(0)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&callCount, 1)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"access_token":"cached_tok","expires_in":3600}`)
	}))
	defer srv.Close()

	auth := NewCDSEAuth("user", "pass")
	auth.tokenEndpoint = srv.URL

	req1, _ := http.NewRequest("GET", "http://example.com", nil)
	if err := auth.Apply(req1); err != nil {
		t.Fatalf("first Apply failed: %v", err)
	}

	req2, _ := http.NewRequest("GET", "http://example.com", nil)
	if err := auth.Apply(req2); err != nil {
		t.Fatalf("second Apply failed: %v", err)
	}

	if atomic.LoadInt32(&callCount) != 1 {
		t.Errorf("expected 1 token fetch, got %d", callCount)
	}
	if req1.Header.Get("Authorization") != "Bearer cached_tok" {
		t.Errorf("expected Bearer cached_tok, got %s", req1.Header.Get("Authorization"))
	}
}

func TestCDSEAuth_FetchToken_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if err := r.ParseForm(); err != nil {
			t.Fatalf("parse form: %v", err)
		}
		if r.FormValue("grant_type") != "password" {
			t.Errorf("expected grant_type=password, got %s", r.FormValue("grant_type"))
		}
		if r.FormValue("username") != "testuser" {
			t.Errorf("expected username=testuser, got %s", r.FormValue("username"))
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"access_token":"tok123","expires_in":60}`)
	}))
	defer srv.Close()

	auth := NewCDSEAuth("testuser", "testpass")
	auth.tokenEndpoint = srv.URL

	tok, err := auth.fetchToken(t.Context())
	if err != nil {
		t.Fatalf("fetchToken failed: %v", err)
	}
	if tok != "tok123" {
		t.Errorf("expected tok123, got %s", tok)
	}
}

func TestCDSEAuth_FetchToken_RetryThenSuccess(t *testing.T) {
	attempts := int32(0)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c := atomic.AddInt32(&attempts, 1)
		if c < 2 {
			w.WriteHeader(http.StatusServiceUnavailable)
			fmt.Fprint(w, "busy")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"access_token":"retry_tok","expires_in":60}`)
	}))
	defer srv.Close()

	auth := NewCDSEAuth("u", "p")
	auth.tokenEndpoint = srv.URL

	tok, err := auth.fetchToken(t.Context())
	if err != nil {
		t.Fatalf("fetchToken failed: %v", err)
	}
	if tok != "retry_tok" {
		t.Errorf("expected retry_tok, got %s", tok)
	}
	if atomic.LoadInt32(&attempts) < 2 {
		t.Errorf("expected at least 2 attempts, got %d", attempts)
	}
}

func TestCDSEAuth_FetchToken_MaxRetriesExceeded(t *testing.T) {
	attempts := int32(0)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&attempts, 1)
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(w, `{"error":"invalid_grant"}`)
	}))
	defer srv.Close()

	auth := NewCDSEAuth("u", "p")
	auth.tokenEndpoint = srv.URL

	_, err := auth.fetchToken(t.Context())
	if err == nil {
		t.Fatal("expected error after max retries")
	}
	if !strings.Contains(err.Error(), "401") {
		t.Errorf("expected 401 in error, got: %v", err)
	}
}

func TestCDSEAuth_TokenExpiry(t *testing.T) {
	callCount := int32(0)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c := atomic.AddInt32(&callCount, 1)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"access_token":"tok%d","expires_in":1}`, c)
	}))
	defer srv.Close()

	auth := NewCDSEAuth("u", "p")
	auth.tokenEndpoint = srv.URL

	req1, _ := http.NewRequest("GET", "http://example.com", nil)
	if err := auth.Apply(req1); err != nil {
		t.Fatalf("first Apply failed: %v", err)
	}

	time.Sleep(2 * time.Second)

	req2, _ := http.NewRequest("GET", "http://example.com", nil)
	if err := auth.Apply(req2); err != nil {
		t.Fatalf("second Apply after expiry failed: %v", err)
	}

	if atomic.LoadInt32(&callCount) != 2 {
		t.Errorf("expected 2 token fetches after expiry, got %d", callCount)
	}
}

func TestEarthdataAuth_BasicAuth(t *testing.T) {
	auth := NewEarthdataAuth("user", "pass")

	req, _ := http.NewRequest("GET", "http://example.com", nil)
	if err := auth.Apply(req); err != nil {
		t.Fatalf("Apply failed: %v", err)
	}

	authHeader := req.Header.Get("Authorization")
	if !strings.HasPrefix(authHeader, "Basic ") {
		t.Errorf("expected Basic auth, got %s", authHeader)
	}
	expected := "Basic " + basicAuth("user", "pass")
	if authHeader != expected {
		t.Errorf("expected %s, got %s", expected, authHeader)
	}
}

func TestEarthdataAuth_FetchToken_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		if !strings.HasPrefix(auth, "Basic ") {
			t.Errorf("expected Basic auth prefix, got %s", auth)
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"access_token":"ed123","expires_in":60}`)
	}))
	defer srv.Close()

	auth := NewEarthdataAuth("testuser", "testpass")
	auth.tokenEndpoint = srv.URL

	tok, err := auth.fetchToken(t.Context())
	if err != nil {
		t.Fatalf("fetchToken failed: %v", err)
	}
	if tok != "ed123" {
		t.Errorf("expected ed123, got %s", tok)
	}
}

func TestEarthdataAuth_FetchToken_MaxRetriesExceeded(t *testing.T) {
	attempts := int32(0)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&attempts, 1)
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(w, `{"error":"invalid_credentials"}`)
	}))
	defer srv.Close()

	auth := NewEarthdataAuth("u", "p")
	auth.tokenEndpoint = srv.URL

	_, err := auth.fetchToken(t.Context())
	if err == nil {
		t.Fatal("expected error after max retries")
	}
	if !strings.Contains(err.Error(), "401") {
		t.Errorf("expected 401 in error, got: %v", err)
	}
}

func TestEarthdataAuth_FetchToken_RetryThenSuccess(t *testing.T) {
	attempts := int32(0)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c := atomic.AddInt32(&attempts, 1)
		if c < 2 {
			w.WriteHeader(http.StatusServiceUnavailable)
			fmt.Fprint(w, "busy")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"access_token":"retry_tok","expires_in":60}`)
	}))
	defer srv.Close()

	auth := NewEarthdataAuth("u", "p")
	auth.tokenEndpoint = srv.URL

	tok, err := auth.fetchToken(t.Context())
	if err != nil {
		t.Fatalf("fetchToken failed: %v", err)
	}
	if tok != "retry_tok" {
		t.Errorf("expected retry_tok, got %s", tok)
	}
}

func TestBasicAuth(t *testing.T) {
	got := basicAuth("user", "pass")
	want := "dXNlcjpwYXNz"
	if got != want {
		t.Errorf("basicAuth(\"user\",\"pass\") = %q, want %q", got, want)
	}
}
