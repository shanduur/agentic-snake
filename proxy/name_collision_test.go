// Copyright 2026 Mateusz Urbanek.

package proxy_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/shanduur/agentic-snake/proxy"
)

func TestShortenedNameCannotShadowAnUnchangedToolName(t *testing.T) {
	originals := []string{
		strings.Repeat("a", 128),
		strings.Repeat("a", 87) + "_6836cf13bac400e9105071cd6af47084",
	}
	backend := mcp.NewServer(&mcp.Implementation{
		Name:    "collision",
		Version: "1",
	}, nil)
	for _, original := range originals {
		backend.AddTool(&mcp.Tool{
			Name:        original,
			Description: original,
			InputSchema: map[string]any{"type": "object"},
		}, func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return &mcp.CallToolResult{
				Content: []mcp.Content{&mcp.TextContent{Text: original}},
			}, nil
		})
	}
	upstream := httptest.NewServer(mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return backend }, nil))
	defer upstream.Close()
	gateway, err := proxy.NewGateway(context.Background(), proxy.Config{
		Upstreams: []proxy.Upstream{{Name: "x", URL: upstream.URL + "/mcp"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer gateway.Close()
	downstream := httptest.NewServer(gateway.Handler())
	defer downstream.Close()
	caller := client(t, downstream.URL+"/mcp")
	defer caller.Close()
	catalog, err := caller.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(catalog.Tools) != len(originals) {
		t.Fatalf("distinct original tools were lost: %#v", catalog.Tools)
	}
	seen := make(map[string]bool)
	for _, tool := range catalog.Tools {
		if seen[tool.Name] || len(tool.Name) > 128 {
			t.Fatalf("invalid or duplicate exported name %q", tool.Name)
		}
		seen[tool.Name] = true
		result, err := caller.CallTool(context.Background(), &mcp.CallToolParams{Name: tool.Name})
		if err != nil {
			t.Fatal(err)
		}
		if len(result.Content) != 1 || result.Content[0].(*mcp.TextContent).Text != tool.Description {
			t.Fatalf("tool %q reached wrong original: %#v", tool.Name, result)
		}
	}
}
