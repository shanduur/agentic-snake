// Copyright 2026 Mateusz Urbanek.

package proxy_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/shanduur/agentic-snake/proxy"
)

func TestMalformedInventoryDoesNotPublishFilteredPartialList(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		var packet struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
		}
		if err := json.NewDecoder(r.Body).Decode(&packet); err != nil {
			t.Errorf("decode fixture request: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if len(packet.ID) == 0 {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		var result any
		switch packet.Method {
		case "initialize":
			result = map[string]any{
				"protocolVersion": "2025-11-25",
				"capabilities":    map[string]any{"tools": map[string]any{}},
				"serverInfo":      map[string]any{"name": "malformed", "version": "1"},
			}
		case "tools/list":
			result = map[string]any{
				"tools": []any{
					map[string]any{
						"name":        "good",
						"inputSchema": map[string]any{"type": "object"},
					},
					nil,
				},
			}
		default:
			result = map[string]any{}
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(map[string]any{
			"jsonrpc": "2.0",
			"id":      packet.ID,
			"result":  result,
		}); err != nil {
			t.Errorf("write fixture response: %v", err)
		}
	}))
	t.Cleanup(upstream.Close)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	gateway, err := proxy.NewGateway(ctx, proxy.Config{
		Upstreams: []proxy.Upstream{
			{Name: "malformed", URL: upstream.URL + "/mcp"},
		},
		RefreshInterval: time.Hour,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = gateway.Close() })
	downstream := httptest.NewServer(gateway.Handler())
	t.Cleanup(downstream.Close)
	caller := client(t, downstream.URL+"/mcp")
	tools, err := caller.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(tools.Tools) != 0 {
		t.Fatalf("published a filtered partial inventory: %+v", tools.Tools)
	}
	if err := gateway.Refresh(ctx); err == nil {
		t.Fatal("malformed raw inventory did not fail discovery")
	}
}
