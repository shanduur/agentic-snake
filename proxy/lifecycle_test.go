// Copyright 2026 Mateusz Urbanek.

package proxy_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/shanduur/agentic-snake/proxy"
)

func TestPeriodicRemovalWithoutNotification(t *testing.T) {
	s, up := upstreamWithNotifications(t, "mutable", false, "stale")
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	g, err := proxy.NewGateway(ctx, proxy.Config{Upstreams: []proxy.Upstream{{Name: "mutable", URL: up.URL + "/mcp"}}, RefreshInterval: 40 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = g.Close() })
	h := httptest.NewServer(g.Handler())
	t.Cleanup(h.Close)
	s.RemoveTools("stale")
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		c := client(t, h.URL+"/mcp")
		result, err := c.ListTools(ctx, nil)
		_ = c.Close()
		if err != nil {
			t.Fatal(err)
		}
		if len(result.Tools) == 0 {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatal("periodic sweep did not remove stale tool")
}

func TestFailedDiscoveryClearsInventoryAndRecovers(t *testing.T) {
	s := mcp.NewServer(&mcp.Implementation{Name: "unstable", Version: "1"}, &mcp.ServerOptions{Capabilities: &mcp.ServerCapabilities{Tools: &mcp.ToolCapabilities{ListChanged: true}}})
	s.AddTool(&mcp.Tool{Name: "alive", InputSchema: map[string]any{"type": "object"}}, func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return &mcp.CallToolResult{}, nil
	})
	realHandler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return s }, nil)
	var broken atomic.Bool
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if broken.Load() && r.Method == http.MethodPost {
			body := make([]byte, 1024)
			n, _ := r.Body.Read(body)
			if n > 0 && (string(body[:n]) != "") {
				http.Error(w, "offline", 500)
				return
			}
		}
		realHandler.ServeHTTP(w, r)
	}))
	t.Cleanup(up.Close)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	g, err := proxy.NewGateway(ctx, proxy.Config{Upstreams: []proxy.Upstream{{Name: "unstable", URL: up.URL + "/mcp"}}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = g.Close() })
	h := httptest.NewServer(g.Handler())
	t.Cleanup(h.Close)
	c := client(t, h.URL+"/mcp")
	list, err := c.ListTools(ctx, nil)
	if err != nil || len(list.Tools) != 1 {
		t.Fatalf("initial: %v %v", list, err)
	}
	broken.Store(true)
	if err := g.Refresh(ctx); err == nil {
		t.Fatal("expected failed refresh")
	}
	fresh := client(t, h.URL+"/mcp")
	list, err = fresh.ListTools(ctx, nil)
	if err != nil || len(list.Tools) != 0 {
		t.Fatalf("after failure: %v %v", list, err)
	}
	if _, err := fresh.CallTool(ctx, &mcp.CallToolParams{Name: "mcp_8_unstable_alive"}); err == nil {
		t.Fatal("stale tool callable")
	}
	broken.Store(false)
	if err := g.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	recovered := client(t, h.URL+"/mcp")
	list, err = recovered.ListTools(ctx, nil)
	if err != nil || len(list.Tools) != 1 {
		t.Fatalf("recovery: %v %v", list, err)
	}
}

func TestCloseTerminatesDownstreamSSE(t *testing.T) {
	_, up := upstream(t, "one", "tool")
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	g, err := proxy.NewGateway(ctx, proxy.Config{Upstreams: []proxy.Upstream{{Name: "one", URL: up.URL + "/mcp"}}})
	if err != nil {
		t.Fatal(err)
	}
	h := httptest.NewServer(g.Handler())
	t.Cleanup(h.Close)
	c := client(t, h.URL+"/mcp")
	done := make(chan struct{})
	go func() { _ = c.Wait(); close(done) }()
	if err := g.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("downstream SSE/session survived gateway Close")
	}
}

func TestNotificationRefreshesBeforeSweep(t *testing.T) {
	s, up := upstream(t, "notify")
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	g, err := proxy.NewGateway(ctx, proxy.Config{Upstreams: []proxy.Upstream{{Name: "notify", URL: up.URL + "/mcp"}}, RefreshInterval: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = g.Close() })
	h := httptest.NewServer(g.Handler())
	t.Cleanup(h.Close)
	s.AddTool(&mcp.Tool{Name: "fresh", InputSchema: map[string]any{"type": "object"}}, func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return &mcp.CallToolResult{}, nil
	})
	deadline := time.Now().Add(1500 * time.Millisecond)
	for time.Now().Before(deadline) {
		c := client(t, h.URL+"/mcp")
		result, err := c.ListTools(ctx, nil)
		_ = c.Close()
		if err != nil {
			t.Fatal(err)
		}
		if len(result.Tools) == 1 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("listChanged did not trigger refresh")
}

func TestCancellationDoesNotReplayCall(t *testing.T) {
	s := mcp.NewServer(&mcp.Implementation{Name: "slow", Version: "1"}, &mcp.ServerOptions{Capabilities: &mcp.ServerCapabilities{Tools: &mcp.ToolCapabilities{}}})
	var count atomic.Int32
	s.AddTool(&mcp.Tool{Name: "slow", InputSchema: map[string]any{"type": "object"}}, func(ctx context.Context, _ *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		count.Add(1)
		<-ctx.Done()
		return nil, ctx.Err()
	})
	up := httptest.NewServer(mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return s }, nil))
	t.Cleanup(up.Close)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	g, err := proxy.NewGateway(ctx, proxy.Config{Upstreams: []proxy.Upstream{{Name: "slow", URL: up.URL + "/mcp"}}, CallTimeout: 80 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = g.Close() })
	h := httptest.NewServer(g.Handler())
	t.Cleanup(h.Close)
	c := client(t, h.URL+"/mcp")
	_, err = c.CallTool(ctx, &mcp.CallToolParams{Name: "mcp_4_slow_slow"})
	if err == nil {
		t.Fatal("call should time out")
	}
	if got := count.Load(); got != 1 {
		t.Fatalf("upstream called %d times, want once", got)
	}
}

func TestInvalidConfig(t *testing.T) {
	for _, u := range []string{"", "ftp://host/mcp", "http://user:pass@host/mcp", "http://host/mcp?x=1", "http://host/mcp#f", "http://host", "http://host/a/../mcp", "http://host:0/mcp", "http://host:99999/mcp"} {
		t.Run(u, func(t *testing.T) {
			if _, err := proxy.NewGateway(context.Background(), proxy.Config{Upstreams: []proxy.Upstream{{Name: "x", URL: u}}}); err == nil {
				t.Fatal("accepted invalid URL")
			}
		})
	}
	if _, err := proxy.NewGateway(context.Background(), proxy.Config{Upstreams: []proxy.Upstream{{Name: "x", URL: "http://host/mcp"}, {Name: "x", URL: "http://host/mcp"}}}); err == nil {
		t.Fatal("duplicate name accepted")
	}
}
