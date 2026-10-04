// Copyright 2026 Mateusz Urbanek.

package proxy_test

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/shanduur/agentic-snake/proxy"
)

func TestHTTPTransport(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/remote/mcp" || r.Method != http.MethodPost {
			t.Errorf("upstream request: %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("Authorization") != "" || r.Header.Get("Forwarded") != "" || r.Header.Get("X-Forwarded-For") != "" || r.Header.Get("Cookie") != "" || r.Header.Get("X-Real-Ip") != "" {
			t.Errorf("identity leaked: %v", r.Header)
		}
		if got := r.Header.Get("Mcp-Session-Id"); got != "session-1" {
			t.Errorf("session = %q", got)
		}
		if got := r.Header.Get("Mcp-Protocol-Version"); got != "2025-03-26" {
			t.Errorf("protocol version = %q", got)
		}
		b, _ := io.ReadAll(r.Body)
		if string(b) != `{"jsonrpc":"2.0"}` {
			t.Errorf("body = %q", b)
		}
		w.Header().Set("Mcp-Session-Id", "session-2")
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Set-Cookie", "secret=1")
		w.WriteHeader(http.StatusAccepted)
		fmt.Fprint(w, `{"result":"ok"}`)
	}))
	defer upstream.Close()
	handler, err := proxy.NewHandler(proxy.Config{UpstreamURL: upstream.URL + "/remote/mcp"})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()
	req, _ := http.NewRequest(http.MethodPost, server.URL+"/mcp", strings.NewReader(`{"jsonrpc":"2.0"}`))
	req.Header.Set("Authorization", "Bearer downstream-secret")
	req.Header.Set("Cookie", "secret=downstream")
	req.Header.Set("Forwarded", "for=untrusted")
	req.Header.Set("X-Forwarded-For", "10.0.0.1")
	req.Header.Set("X-Real-IP", "10.0.0.1")
	req.Header.Set("Mcp-Session-Id", "session-1")
	req.Header.Set("Mcp-Protocol-Version", "2025-03-26")
	req.Header.Set("Content-Type", "application/json")
	resp, err := server.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusAccepted || string(body) != `{"result":"ok"}` || resp.Header.Get("Mcp-Session-Id") != "session-2" || resp.Header.Get("Set-Cookie") != "" {
		t.Errorf("response: %d %s %v", resp.StatusCode, body, resp.Header)
	}
}

func TestSSEStreaming(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.Header.Get("Accept") != "text/event-stream" || r.Header.Get("Last-Event-Id") != "event-1" {
			t.Errorf("SSE request: %s %v", r.Method, r.Header)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Mcp-Session-Id", "session-1")
		fmt.Fprint(w, "event: message\ndata: first\n\n")
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	defer upstream.Close()
	handler, err := proxy.NewHandler(proxy.Config{UpstreamURL: upstream.URL + "/remote/mcp"})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+"/mcp", nil)
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Last-Event-ID", "event-1")
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.Header.Get("Mcp-Session-Id") != "session-1" {
		t.Errorf("session = %q", resp.Header.Get("Mcp-Session-Id"))
	}
	line, err := bufio.NewReader(resp.Body).ReadString('\n')
	cancel()
	if err != nil || line != "event: message\n" {
		t.Errorf("stream first line = %q, %v", line, err)
	}
}

func TestInvalidConfig(t *testing.T) {
	for _, raw := range []string{"", "ftp://example.com/mcp", "http://user:pass@example.com/mcp", "http://example.com/mcp?x=1", "http://example.com/mcp#frag", "http://example.com", "http://example.com/mcp/../other", "http://example.com:bad/mcp", "http://example.com:99999/mcp", "http://example.com:0/mcp"} {
		t.Run(raw, func(t *testing.T) {
			if _, err := proxy.NewHandler(proxy.Config{UpstreamURL: raw}); err == nil {
				t.Fatalf("accepted %q", raw)
			}
		})
	}
}

func TestRouteBoundary(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { t.Error("unexpected upstream call") }))
	defer upstream.Close()
	handler, err := proxy.NewHandler(proxy.Config{UpstreamURL: upstream.URL + "/mcp"})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		method, path string
		status       int
	}{{"GET", "/healthz", 200}, {"POST", "/healthz", 405}, {"PUT", "/mcp", 405}, {"GET", "/mcp/other", 404}, {"GET", "/mcp%2fother", 404}} {
		req := httptest.NewRequest(tc.method, tc.path, nil)
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)
		if rr.Code != tc.status {
			t.Errorf("%s %s: got %d want %d", tc.method, tc.path, rr.Code, tc.status)
		}
	}
}
