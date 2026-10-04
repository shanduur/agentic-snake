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

func upstream(t *testing.T, name string, tools ...string) (*mcp.Server, *httptest.Server) {
	return upstreamWithNotifications(t, name, true, tools...)
}

func upstreamWithNotifications(t *testing.T, name string, notifications bool, tools ...string) (*mcp.Server, *httptest.Server) {
	t.Helper()
	s := mcp.NewServer(&mcp.Implementation{Name: name, Version: "1"}, &mcp.ServerOptions{Capabilities: &mcp.ServerCapabilities{Tools: &mcp.ToolCapabilities{ListChanged: notifications}}, PageSize: 1})
	for _, tool := range tools {
		tool := tool
		s.AddTool(&mcp.Tool{Name: tool, InputSchema: map[string]any{"type": "object"}, OutputSchema: map[string]any{"type": "object"}, Annotations: &mcp.ToolAnnotations{Title: "annotation"}}, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: name}}, StructuredContent: map[string]any{"from": name}}, nil
		})
	}
	h := httptest.NewServer(mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return s }, &mcp.StreamableHTTPOptions{SessionTimeout: time.Minute}))
	t.Cleanup(h.Close)
	return s, h
}

func client(t *testing.T, url string) *mcp.ClientSession {
	t.Helper()
	c := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, &mcp.ClientOptions{Capabilities: &mcp.ClientCapabilities{}})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	session, err := c.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: url, MaxRetries: -1}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return session
}

func TestAggregatesPaginatedCollisionsAndRoutes(t *testing.T) {
	_, a := upstream(t, "alpha", "same", "other")
	_, b := upstream(t, "beta", "same")
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	g, err := proxy.NewGateway(ctx, proxy.Config{Upstreams: []proxy.Upstream{{Name: "alpha", URL: a.URL + "/mcp"}, {Name: "beta", URL: b.URL + "/mcp"}}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = g.Close() })
	downstream := httptest.NewServer(g.Handler())
	t.Cleanup(downstream.Close)
	c := client(t, downstream.URL+"/mcp")
	list, err := c.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(list.Tools) != 3 {
		t.Fatalf("tools = %v", list.Tools)
	}
	for _, name := range []string{"mcp_5_alpha_same", "mcp_5_alpha_other", "mcp_4_beta_same"} {
		var found *mcp.Tool
		for _, item := range list.Tools {
			if item.Name == name {
				found = item
			}
		}
		if found == nil {
			t.Errorf("missing %s: %v", name, list.Tools)
			continue
		}
		if found.Annotations == nil || found.Annotations.Title != "annotation" || found.OutputSchema == nil {
			t.Errorf("lost metadata: %+v", found)
		}
		result, err := c.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: map[string]any{}})
		if err != nil {
			t.Fatal(err)
		}
		want := "alpha"
		if name == "mcp_4_beta_same" {
			want = "beta"
		}
		if result.StructuredContent.(map[string]any)["from"] != want {
			t.Errorf("%s result: %+v", name, result)
		}
	}
}
