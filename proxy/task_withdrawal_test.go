// Copyright 2026 Mateusz Urbanek.

package proxy_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/shanduur/agentic-snake/proxy"
)

func TestUnknownTaskModeWithdrawsPreviouslyCallableInventory(t *testing.T) {
	server := mcp.NewServer(&mcp.Implementation{
		Name:    "execution",
		Version: "1",
	}, nil)
	var calls atomic.Int32
	server.AddTool(&mcp.Tool{
		Name:        "inspect",
		InputSchema: map[string]any{"type": "object"},
	}, func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		calls.Add(1)
		return &mcp.CallToolResult{}, nil
	})
	inner := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, &mcp.StreamableHTTPOptions{
		JSONResponse: true,
	})
	var invalid atomic.Bool
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			inner.ServeHTTP(w, r)
			return
		}
		recorder := httptest.NewRecorder()
		inner.ServeHTTP(recorder, r)
		for key, values := range recorder.Header() {
			for _, value := range values {
				w.Header().Add(key, value)
			}
		}
		body := recorder.Body.String()
		if strings.Contains(body, `"tools"`) {
			descriptor := `"name":"inspect","execution":{}`
			if invalid.Load() {
				descriptor = `"name":"inspect","execution":{"taskSupport":"unexpected"}`
			}
			body = strings.Replace(body, `"name":"inspect"`, descriptor, 1)
		}
		w.WriteHeader(recorder.Code)
		_, _ = fmt.Fprint(w, body)
	}))
	defer upstream.Close()
	gateway, err := proxy.NewGateway(context.Background(), proxy.Config{
		Upstreams: []proxy.Upstream{
			{Name: "execution", URL: upstream.URL + "/mcp"},
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
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	params := &mcp.CallToolParams{Name: "mcp_9_execution_inspect"}
	if _, err := caller.CallTool(ctx, params); err != nil {
		t.Fatal(err)
	}
	invalid.Store(true)
	if err := gateway.Refresh(ctx); err == nil {
		t.Fatal("unknown task mode accepted")
	}
	catalog, err := caller.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(catalog.Tools) != 0 {
		t.Fatalf("invalid peer retains tools: %v", catalog.Tools)
	}
	if _, err := caller.CallTool(ctx, params); err == nil {
		t.Fatal("withdrawn tool remains callable")
	}
	if calls.Load() != 1 {
		t.Fatalf("withdrawn route reached upstream; calls=%d", calls.Load())
	}
}
