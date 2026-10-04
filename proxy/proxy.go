// Copyright 2026 Mateusz Urbanek.

// Package proxy provides a bounded single-upstream MCP Streamable HTTP proxy.
package proxy

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httputil"
	"net/url"
	"path"
	"strconv"
	"strings"
)

const (
	maxConcurrent = 64
	maxBodyBytes  = 8 << 20
)

// Config identifies the one trusted upstream MCP HTTP endpoint.
type Config struct{ UpstreamURL string }

// NewHandler returns a handler for /mcp and /healthz. It does not authenticate clients.
func NewHandler(cfg Config) (http.Handler, error) {
	upstream, err := url.Parse(cfg.UpstreamURL)
	if err != nil {
		return nil, fmt.Errorf("parse upstream URL: %w", err)
	}
	if (upstream.Scheme != "http" && upstream.Scheme != "https") || upstream.Hostname() == "" || upstream.User != nil || upstream.RawQuery != "" || upstream.ForceQuery || upstream.Fragment != "" || strings.Contains(cfg.UpstreamURL, "#") || upstream.Opaque != "" || upstream.Path == "" || !strings.HasPrefix(upstream.Path, "/") || path.Clean(upstream.Path) != upstream.Path || upstream.EscapedPath() != upstream.Path || strings.HasSuffix(upstream.Path, "/") {
		return nil, errors.New("upstream must be an http(s) URL with a canonical endpoint path and no credentials, query or fragment")
	}
	if upstream.Port() != "" {
		port, err := strconv.Atoi(upstream.Port())
		if err != nil || port < 1 || port > 65535 {
			return nil, errors.New("upstream port must be between 1 and 65535")
		}
	}
	reverse := &httputil.ReverseProxy{
		Rewrite: func(p *httputil.ProxyRequest) {
			p.Out.Header = make(http.Header)
			for _, name := range []string{"Accept", "Content-Type", "Mcp-Session-Id", "Mcp-Protocol-Version", "Last-Event-Id"} {
				if values := p.In.Header.Values(name); len(values) != 0 {
					p.Out.Header[http.CanonicalHeaderKey(name)] = append([]string(nil), values...)
				}
			}
			p.SetURL(upstream)
			p.Out.URL.Path = upstream.Path
			p.Out.URL.RawPath = ""
			p.Out.URL.RawQuery = ""
		},
		ModifyResponse: func(resp *http.Response) error {
			resp.Header.Del("Set-Cookie")
			return nil
		},
		FlushInterval: -1,
	}
	slots := make(chan struct{}, maxConcurrent)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.EscapedPath() == "/healthz" {
			if r.Method != http.MethodGet {
				w.Header().Set("Allow", "GET")
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
				return
			}
			w.WriteHeader(http.StatusOK)
			return
		}
		if r.URL.EscapedPath() != "/mcp" || r.URL.RawQuery != "" {
			http.NotFound(w, r)
			return
		}
		if r.Method != http.MethodPost && r.Method != http.MethodGet && r.Method != http.MethodDelete {
			w.Header().Set("Allow", "GET, POST, DELETE")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		// No browser-origin access: the service intentionally has no client auth.
		if r.Header.Get("Origin") != "" {
			http.Error(w, "origin not allowed", http.StatusForbidden)
			return
		}
		if r.ContentLength > maxBodyBytes {
			http.Error(w, "request body too large", http.StatusRequestEntityTooLarge)
			return
		}
		select {
		case slots <- struct{}{}:
			defer func() { <-slots }()
		default:
			http.Error(w, "proxy at capacity", http.StatusServiceUnavailable)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
		reverse.ServeHTTP(w, r)
	}), nil
}
