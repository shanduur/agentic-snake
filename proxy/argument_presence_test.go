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

func TestOmittedArgumentsRemainOmitted(t *testing.T) {
	server := mcp.NewServer(&mcp.Implementation{
		Name:    "presence",
		Version: "1",
	}, nil)
	server.AddTool(&mcp.Tool{
		Name:        "inspect",
		InputSchema: map[string]any{"type": "object"},
	}, func(_ context.Context, request *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if string(request.Params.Arguments) != "{}" {
			t.Errorf("absent arguments did not retain the SDK's empty-object normalization: %s", request.Params.Arguments)
		}
		return &mcp.CallToolResult{}, nil
	})
	upstream := httptest.NewServer(mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil))
	defer upstream.Close()
	gateway, err := proxy.NewGateway(context.Background(), proxy.Config{
		Upstreams: []proxy.Upstream{
			{Name: "presence", URL: upstream.URL + "/mcp"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer gateway.Close()
	downstream := httptest.NewServer(gateway.Handler())
	defer downstream.Close()
	caller := client(t, downstream.URL+"/mcp")
	defer caller.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := caller.CallTool(ctx, &mcp.CallToolParams{
		Name: "mcp_8_presence_inspect",
	}); err != nil {
		t.Fatal(err)
	}
}
