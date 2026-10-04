// Copyright 2026 Mateusz Urbanek.

package proxy_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/shanduur/agentic-snake/proxy"
)

func TestStreamingToolListWithOpenUpstreamResponse(t *testing.T) {
	s := mcp.NewServer(&mcp.Implementation{Name: "stream", Version: "1"}, nil)
	s.AddTool(&mcp.Tool{Name: "task", InputSchema: map[string]any{"type": "object"}}, func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return &mcp.CallToolResult{}, nil
	})
	s.AddTool(&mcp.Tool{Name: "normal", InputSchema: map[string]any{"type": "object"}}, func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return &mcp.CallToolResult{}, nil
	})
	inner := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return s }, nil)
	held := make(chan struct{}, 1)
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			inner.ServeHTTP(w, r)
			return
		}
		payload, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(payload), `"method":"tools/list"`) {
			r.Body = io.NopCloser(strings.NewReader(string(payload)))
			inner.ServeHTTP(w, r)
			return
		}
		var request struct {
			ID json.RawMessage `json:"id"`
		}
		if err := json.Unmarshal(payload, &request); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, ": heartbeat\r\n\r\nevent: message\r\nid: inventory\r\ndata: {\"jsonrpc\":\"2.0\",\"id\":"+string(request.ID)+",\"result\":{\"tools\":[\r\ndata: {\"name\":\"task\",\"inputSchema\":{\"type\":\"object\"},\"execution\":{\"taskSupport\":\"required\"}},\r\ndata: {\"name\":\"normal\",\"inputSchema\":{\"type\":\"object\"}}]}}\r\n\r\n")
		w.(http.Flusher).Flush()
		select {
		case held <- struct{}{}:
		default:
		}
		<-r.Context().Done()
	}))
	defer up.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	g, err := proxy.NewGateway(ctx, proxy.Config{Upstreams: []proxy.Upstream{{Name: "stream", URL: up.URL + "/mcp"}}})
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()
	select {
	case <-held:
	case <-ctx.Done():
		t.Fatal("fixture did not send an open SSE response")
	}
	h := httptest.NewServer(g.Handler())
	defer h.Close()
	c := client(t, h.URL+"/mcp")
	defer c.Close()
	result, err := c.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Tools) != 1 || result.Tools[0].Name != "mcp_6_stream_normal" {
		t.Fatalf("list from open multiline CRLF SSE = %+v", result.Tools)
	}
}
