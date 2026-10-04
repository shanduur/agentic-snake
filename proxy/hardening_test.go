// Copyright 2026 Mateusz Urbanek.

package proxy_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/shanduur/agentic-snake/proxy"
)

func TestCloseCancelsActiveCallAndRejectsNewOperations(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	s := mcp.NewServer(&mcp.Implementation{Name: "slow", Version: "1"}, nil)
	s.AddTool(&mcp.Tool{Name: "slow", InputSchema: map[string]any{"type": "object"}}, func(ctx context.Context, _ *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		close(started)
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-release:
			return nil, context.Canceled
		}
	})
	up := httptest.NewServer(mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return s }, nil))
	defer up.Close()
	g, err := proxy.NewGateway(context.Background(), proxy.Config{Upstreams: []proxy.Upstream{{Name: "slow", URL: up.URL + "/mcp"}}, CallTimeout: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	h := httptest.NewServer(g.Handler())
	defer func() {
		close(release)
		_ = g.Close()
		h.Close()
	}()
	c := client(t, h.URL+"/mcp")
	done := make(chan error, 1)
	go func() {
		_, err := c.CallTool(context.Background(), &mcp.CallToolParams{Name: "mcp_4_slow_slow"})
		done <- err
	}()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("call did not start")
	}
	closed := make(chan struct{})
	go func() { _ = g.Close(); close(closed) }()
	select {
	case <-closed:
	case <-time.After(2 * time.Second):
		t.Fatal("close blocked by active call")
	}
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("active call survived close")
	}
	if err := g.Refresh(context.Background()); err == nil {
		t.Fatal("refresh succeeded after close")
	}
	resp, err := http.Get(h.URL + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("health after close = %d", resp.StatusCode)
	}
	if err := g.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestCloseBoundedWhenUpstreamDeleteStalls(t *testing.T) {
	deleting := make(chan struct{}, 4)
	release := make(chan struct{})
	s := mcp.NewServer(&mcp.Implementation{Name: "stall", Version: "1"}, nil)
	s.AddTool(&mcp.Tool{Name: "ok", InputSchema: map[string]any{"type": "object"}}, func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return &mcp.CallToolResult{}, nil
	})
	inner := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return s }, nil)
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			select {
			case deleting <- struct{}{}:
			default:
			}
			<-release // Deliberately ignores the HTTP request context.
			return
		}
		inner.ServeHTTP(w, r)
	}))
	defer func() {
		close(release)
		up.Close()
	}()
	g, err := proxy.NewGateway(context.Background(), proxy.Config{Upstreams: []proxy.Upstream{
		{Name: "stall1", URL: up.URL + "/mcp"},
		{Name: "stall2", URL: up.URL + "/mcp"},
		{Name: "stall3", URL: up.URL + "/mcp"},
		{Name: "stall4", URL: up.URL + "/mcp"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	closed := make(chan struct{})
	go func() { _ = g.Close(); close(closed) }()
	deadline := time.After(1500 * time.Millisecond)
	for i := 0; i < 4; i++ {
		select {
		case <-deleting:
		case <-deadline:
			t.Fatalf("only %d upstream DELETEs were attempted before close deadline", i)
		}
	}
	select {
	case <-closed:
	case <-deadline:
		t.Fatal("gateway Close serialized stalled upstream DELETEs")
	}
}

func TestRefreshDoesNotWaitForActiveToolCall(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	s := mcp.NewServer(&mcp.Implementation{Name: "refresh", Version: "1"}, nil)
	s.AddTool(&mcp.Tool{Name: "slow", InputSchema: map[string]any{"type": "object"}}, func(ctx context.Context, _ *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		close(started)
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-release:
			return nil, context.Canceled
		}
	})
	inner := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return s }, nil)
	var failList atomic.Bool
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if failList.Load() && r.Method == http.MethodPost {
			body, _ := io.ReadAll(r.Body)
			if strings.Contains(string(body), `"method":"tools/list"`) {
				http.Error(w, "unavailable", http.StatusServiceUnavailable)
				return
			}
			r.Body = io.NopCloser(strings.NewReader(string(body)))
		}
		inner.ServeHTTP(w, r)
	}))
	defer up.Close()
	g, err := proxy.NewGateway(context.Background(), proxy.Config{Upstreams: []proxy.Upstream{{Name: "refresh", URL: up.URL + "/mcp"}}, CallTimeout: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	h := httptest.NewServer(g.Handler())
	c := client(t, h.URL+"/mcp")
	defer func() {
		close(release)
		_ = g.Close()
		_ = c.Close()
		h.Close()
	}()
	callDone := make(chan struct{})
	go func() {
		defer close(callDone)
		_, _ = c.CallTool(context.Background(), &mcp.CallToolParams{Name: "mcp_7_refresh_slow"})
	}()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("upstream call did not start")
	}
	failList.Store(true)
	refreshDone := make(chan error, 1)
	go func() { refreshDone <- g.Refresh(context.Background()) }()
	select {
	case err := <-refreshDone:
		if err == nil {
			t.Fatal("failed inventory accepted")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("refresh waited on active tool call")
	}
	select {
	case <-callDone:
	case <-time.After(2 * time.Second):
		t.Fatal("withdrawn peer's tool call survived")
	}
}

func TestOversizedUpstreamBodyWithdrawsInventory(t *testing.T) {
	s := mcp.NewServer(&mcp.Implementation{Name: "large", Version: "1"}, nil)
	s.AddTool(&mcp.Tool{Name: "live", InputSchema: map[string]any{"type": "object"}}, func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return &mcp.CallToolResult{}, nil
	})
	normal := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return s }, nil)
	var oversized atomic.Bool
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if oversized.Load() && r.Method == http.MethodPost {
			b, _ := io.ReadAll(io.LimitReader(r.Body, 4096))
			if strings.Contains(string(b), `"method":"tools/list"`) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = fmt.Fprint(w, strings.Repeat("x", 9<<20))
				return
			}
		}
		normal.ServeHTTP(w, r)
	}))
	defer up.Close()
	g, err := proxy.NewGateway(context.Background(), proxy.Config{Upstreams: []proxy.Upstream{{Name: "large", URL: up.URL + "/mcp"}}})
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()
	oversized.Store(true)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := g.Refresh(ctx); err == nil {
		t.Fatal("oversized body accepted")
	}
	h := httptest.NewServer(g.Handler())
	defer h.Close()
	c := client(t, h.URL+"/mcp")
	defer c.Close()
	got, err := c.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Tools) != 0 {
		t.Fatalf("failed inventory remains: %v", got.Tools)
	}
}

func TestTaskRequiredNotAdvertisedAndUnknownModeWithdraws(t *testing.T) {
	s := mcp.NewServer(&mcp.Implementation{Name: "tasks", Version: "1"}, nil)
	for _, name := range []string{"normal", "task"} {
		s.AddTool(&mcp.Tool{Name: name, InputSchema: map[string]any{"type": "object"}}, func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return &mcp.CallToolResult{}, nil
		})
	}
	inner := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return s }, nil)
	var mode atomic.Value
	mode.Store("required")
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			inner.ServeHTTP(w, r)
			return
		}
		rec := httptest.NewRecorder()
		inner.ServeHTTP(rec, r)
		for k, vs := range rec.Header() {
			for _, v := range vs {
				w.Header().Add(k, v)
			}
		}
		w.WriteHeader(rec.Code)
		b := rec.Body.Bytes()
		if strings.Contains(string(b), `"name":"task"`) && strings.Contains(string(b), `"tools"`) {
			b = []byte(strings.Replace(string(b), `"name":"task"`, fmt.Sprintf(`"name":"task","execution":{"taskSupport":%q}`, mode.Load().(string)), 1))
		}
		_, _ = w.Write(b)
	}))
	defer up.Close()
	g, err := proxy.NewGateway(context.Background(), proxy.Config{Upstreams: []proxy.Upstream{{Name: "tasks", URL: up.URL + "/mcp"}}})
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()
	h := httptest.NewServer(g.Handler())
	defer h.Close()
	c := client(t, h.URL+"/mcp")
	defer c.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	got, err := c.ListTools(ctx, nil)
	if err != nil || len(got.Tools) != 1 || got.Tools[0].Name != "mcp_5_tasks_normal" {
		t.Fatalf("required task leaked: %+v %v", got, err)
	}
	mode.Store("unexpected")
	if err := g.Refresh(ctx); err == nil {
		t.Fatal("unknown execution mode accepted")
	}
}

func TestCatalogPublicationAtomic(t *testing.T) {
	s := mcp.NewServer(&mcp.Implementation{Name: "catalog", Version: "1"}, nil)
	add := func(prefix string) {
		for i := 0; i < 40; i++ {
			name := fmt.Sprintf("%s_%d", prefix, i)
			s.AddTool(&mcp.Tool{Name: name, InputSchema: map[string]any{"type": "object"}}, func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				return &mcp.CallToolResult{}, nil
			})
		}
	}
	add("old")
	up := httptest.NewServer(mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return s }, nil))
	defer up.Close()
	g, err := proxy.NewGateway(context.Background(), proxy.Config{Upstreams: []proxy.Upstream{{Name: "catalog", URL: up.URL + "/mcp"}}, RefreshInterval: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()
	h := httptest.NewServer(g.Handler())
	defer h.Close()
	c := client(t, h.URL+"/mcp")
	defer c.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()
	finished := make(chan error, 1)
	go func() {
		s.RemoveTools(func() []string {
			out := make([]string, 40)
			for i := range out {
				out[i] = fmt.Sprintf("old_%d", i)
			}
			return out
		}()...)
		add("new")
		finished <- g.Refresh(ctx)
	}()
	for i := 0; i < 40; i++ {
		list, err := c.ListTools(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		if len(list.Tools) != 40 {
			t.Fatalf("partial inventory: %d", len(list.Tools))
		}
	}
	if err := <-finished; err != nil {
		t.Fatal(err)
	}
}

func TestNoUpstreamMultiRoundTripReplay(t *testing.T) {
	s := mcp.NewServer(&mcp.Implementation{Name: "replay", Version: "1"}, nil)
	var calls atomic.Int32
	s.AddTool(&mcp.Tool{Name: "tool", InputSchema: map[string]any{"type": "object"}}, func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		calls.Add(1)
		return &mcp.CallToolResult{}, nil
	})
	inner := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return s }, &mcp.StreamableHTTPOptions{JSONResponse: true})
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			inner.ServeHTTP(w, r)
			return
		}
		payload, _ := io.ReadAll(r.Body)
		r.Body = io.NopCloser(strings.NewReader(string(payload)))
		rec := httptest.NewRecorder()
		inner.ServeHTTP(rec, r)
		for k, vs := range rec.Header() {
			for _, v := range vs {
				w.Header().Add(k, v)
			}
		}
		w.WriteHeader(rec.Code)
		body := rec.Body.String()
		if strings.Contains(string(payload), `"method":"tools/call"`) {
			var message map[string]json.RawMessage
			if err := json.Unmarshal([]byte(body), &message); err != nil {
				t.Errorf("decode call response: %v", err)
				return
			}
			message["result"] = json.RawMessage(`{"content":[],"resultType":"input_required","inputRequests":{},"requestState":"opaque"}`)
			encoded, err := json.Marshal(message)
			if err != nil {
				t.Errorf("encode call response: %v", err)
				return
			}
			body = string(encoded)
		}
		_, _ = io.WriteString(w, body)
	}))
	defer up.Close()
	g, err := proxy.NewGateway(context.Background(), proxy.Config{Upstreams: []proxy.Upstream{{Name: "replay", URL: up.URL + "/mcp"}}})
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()
	h := httptest.NewServer(g.Handler())
	defer h.Close()
	c := client(t, h.URL+"/mcp")
	defer c.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, err = c.CallTool(ctx, &mcp.CallToolParams{Name: "mcp_6_replay_tool"})
	if err == nil {
		t.Fatal("input-required result accepted")
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("upstream invocations = %d, want 1", got)
	}
}

func TestCredentialParseErrorRedacted(t *testing.T) {
	_, err := proxy.NewGateway(context.Background(), proxy.Config{Upstreams: []proxy.Upstream{{Name: "secret", URL: "http://user:very-secret@host:%zz/mcp"}}})
	if err == nil || strings.Contains(err.Error(), "very-secret") {
		t.Fatalf("URL error leaked credential: %v", err)
	}
}
