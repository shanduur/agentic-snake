// Copyright 2026 Mateusz Urbanek.

package proxy_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/shanduur/agentic-snake/proxy"
)

func TestRequestReadDeadlineDoesNotTruncateSSEToolResult(t *testing.T) {
	backend := mcp.NewServer(&mcp.Implementation{
		Name:    "deadline",
		Version: "1",
	}, nil)
	backend.AddTool(&mcp.Tool{
		Name:        "slow",
		InputSchema: map[string]any{"type": "object"},
	}, func(ctx context.Context, _ *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		select {
		case <-time.After(120 * time.Millisecond):
			return &mcp.CallToolResult{
				Content: []mcp.Content{&mcp.TextContent{Text: "completed"}},
			}, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	})
	upstream := httptest.NewServer(mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return backend }, nil))
	defer upstream.Close()
	gateway, err := proxy.NewGateway(context.Background(), proxy.Config{
		Upstreams: []proxy.Upstream{
			{Name: "deadline", URL: upstream.URL + "/mcp"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer gateway.Close()
	downstream := httptest.NewUnstartedServer(gateway.Handler())
	downstream.Config.ReadHeaderTimeout = time.Second
	downstream.Config.ReadTimeout = 20 * time.Millisecond
	downstream.Start()
	defer downstream.Close()
	caller := client(t, downstream.URL+"/mcp")
	defer caller.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	result, err := caller.CallTool(ctx, &mcp.CallToolParams{
		Name: "mcp_8_deadline_slow",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError || len(result.Content) != 1 {
		t.Fatalf("slow SSE call lost result: %#v", result)
	}
}
