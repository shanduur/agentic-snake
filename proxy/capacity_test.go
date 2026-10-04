// Copyright 2026 Mateusz Urbanek.

package proxy_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/shanduur/agentic-snake/proxy"
)

func TestHTTPAdmissionDoesNotConsumeToolCallSlots(t *testing.T) {
	const count = 36
	started := make(chan struct{}, count)
	release := make(chan struct{})
	server := mcp.NewServer(&mcp.Implementation{
		Name:    "capacity",
		Version: "1",
	}, nil)
	server.AddTool(&mcp.Tool{
		Name:        "hold",
		InputSchema: map[string]any{"type": "object"},
	}, func(ctx context.Context, _ *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		started <- struct{}{}
		select {
		case <-release:
			return &mcp.CallToolResult{}, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	})
	upstream := httptest.NewServer(mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil))
	defer upstream.Close()
	gateway, err := proxy.NewGateway(context.Background(), proxy.Config{
		Upstreams: []proxy.Upstream{
			{Name: "capacity", URL: upstream.URL + "/mcp"},
		},
		RefreshInterval: time.Hour,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer gateway.Close()
	downstream := httptest.NewServer(gateway.Handler())
	defer downstream.Close()
	caller := client(t, downstream.URL+"/mcp")
	defer caller.Close()
	defer close(release)
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	var calls sync.WaitGroup
	failures := make(chan error, count)
	for i := 0; i < count; i++ {
		calls.Add(1)
		go func() {
			defer calls.Done()
			_, err := caller.CallTool(ctx, &mcp.CallToolParams{
				Name: "mcp_8_capacity_hold",
			})
			if err != nil {
				failures <- err
			}
		}()
	}
	for i := 0; i < count; i++ {
		select {
		case <-started:
		case err := <-failures:
			t.Fatalf("HTTP and invocation limits counted the same call twice: %v", err)
		case <-ctx.Done():
			t.Fatal("calls below each independent limit were not admitted")
		}
	}
	// Closing the release channel is deferred until all calls were admitted.
}
