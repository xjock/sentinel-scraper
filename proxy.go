package main

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"time"
)

// newHTTPClient returns an *http.Client with the given timeout.
// If a SOCKS5 proxy is configured via http_proxy/https_proxy environment
// variables, the client is wired to dial through it. This is necessary
// because Go's standard-library ProxyFromEnvironment does not support
// the socks5:// scheme without golang.org/x/net/proxy.
func newHTTPClient(timeout time.Duration) *http.Client {
	transport := &http.Transport{
		DialContext:           defaultDialContext(),
		ForceAttemptHTTP2:     false,
		MaxIdleConns:          100,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
		DisableKeepAlives:     true,
	}
	// Only use ProxyFromEnvironment for HTTP/HTTPS proxies. SOCKS5 is handled
	// by the custom DialContext above.
	if u := proxyURLFromEnv(); u != nil && u.Scheme != "socks5" {
		transport.Proxy = http.ProxyFromEnvironment
	}
	return &http.Client{
		Timeout:   timeout,
		Transport: transport,
	}
}

// defaultDialContext returns a DialContext function that uses a SOCKS5 proxy
// when one is configured in the environment; otherwise it uses net.Dialer.
func defaultDialContext() func(ctx context.Context, network, addr string) (net.Conn, error) {
	proxyURL := proxyURLFromEnv()
	if proxyURL == nil || proxyURL.Scheme != "socks5" {
		d := &net.Dialer{
			Timeout:   30 * time.Second,
			KeepAlive: 30 * time.Second,
		}
		return d.DialContext
	}
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		return dialSOCKS5(ctx, proxyURL.Host, addr)
	}
}

// proxyURLFromEnv returns the SOCKS5/HTTP proxy URL from the environment.
// It prefers https_proxy, then http_proxy.
func proxyURLFromEnv() *url.URL {
	for _, name := range []string{"HTTPS_PROXY", "https_proxy", "HTTP_PROXY", "http_proxy"} {
		if v := os.Getenv(name); v != "" {
			if u, err := url.Parse(v); err == nil {
				return u
			}
		}
	}
	return nil
}

// dialSOCKS5 connects to targetAddr through a SOCKS5 proxy at proxyAddr.
// It supports remote domain-name resolution (socks5h semantics) by sending
// the domain name to the proxy.
func dialSOCKS5(ctx context.Context, proxyAddr, targetAddr string) (net.Conn, error) {
	// Parse target address into host and port.
	host, portStr, err := net.SplitHostPort(targetAddr)
	if err != nil {
		return nil, fmt.Errorf("invalid target address %q: %w", targetAddr, err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		return nil, fmt.Errorf("invalid target port %q: %w", portStr, err)
	}

	// Connect to SOCKS5 proxy.
	dialer := &net.Dialer{Timeout: 30 * time.Second}
	conn, err := dialer.DialContext(ctx, "tcp", proxyAddr)
	if err != nil {
		return nil, fmt.Errorf("connect to socks5 proxy: %w", err)
	}

	// 1. Greeting: version 5, 1 method, no auth.
	if _, err := conn.Write([]byte{0x05, 0x01, 0x00}); err != nil {
		conn.Close()
		return nil, fmt.Errorf("socks5 greeting: %w", err)
	}
	resp := make([]byte, 2)
	if _, err := io.ReadFull(conn, resp); err != nil {
		conn.Close()
		return nil, fmt.Errorf("socks5 greeting response: %w", err)
	}
	if resp[0] != 0x05 {
		conn.Close()
		return nil, fmt.Errorf("socks5 unsupported version: %d", resp[0])
	}
	if resp[1] != 0x00 {
		conn.Close()
		return nil, fmt.Errorf("socks5 auth method not accepted: %d", resp[1])
	}

	// 2. Request: connect, remote DNS (domain name).
	req := []byte{0x05, 0x01, 0x00, 0x03, byte(len(host))}
	req = append(req, host...)
	req = append(req, byte(port>>8), byte(port))
	if _, err := conn.Write(req); err != nil {
		conn.Close()
		return nil, fmt.Errorf("socks5 request: %w", err)
	}

	reply := make([]byte, 4)
	if _, err := io.ReadFull(conn, reply); err != nil {
		conn.Close()
		return nil, fmt.Errorf("socks5 reply header: %w", err)
	}
	if reply[0] != 0x05 {
		conn.Close()
		return nil, fmt.Errorf("socks5 reply version mismatch: %d", reply[0])
	}
	if reply[1] != 0x00 {
		conn.Close()
		return nil, fmt.Errorf("socks5 connect failed: %d", reply[1])
	}

	// Discard the bound address returned by the proxy.
	switch reply[3] {
	case 0x01: // IPv4
		if _, err := io.ReadFull(conn, make([]byte, 4+2)); err != nil {
			conn.Close()
			return nil, fmt.Errorf("socks5 ipv4 bound address: %w", err)
		}
	case 0x03: // Domain
		lenBuf := make([]byte, 1)
		if _, err := io.ReadFull(conn, lenBuf); err != nil {
			conn.Close()
			return nil, fmt.Errorf("socks5 domain length: %w", err)
		}
		if _, err := io.ReadFull(conn, make([]byte, int(lenBuf[0])+2)); err != nil {
			conn.Close()
			return nil, fmt.Errorf("socks5 domain bound address: %w", err)
		}
	case 0x04: // IPv6
		if _, err := io.ReadFull(conn, make([]byte, 16+2)); err != nil {
			conn.Close()
			return nil, fmt.Errorf("socks5 ipv6 bound address: %w", err)
		}
	default:
		conn.Close()
		return nil, fmt.Errorf("socks5 unsupported address type: %d", reply[3])
	}

	return conn, nil
}
