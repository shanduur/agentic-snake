// Copyright 2026 Mateusz Urbanek.

package proxy_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/shanduur/agentic-snake/proxy"
)

// The raw wire fixture is intentional: the stock SDK's AddTool cannot encode
// descriptors that should have been rejected before its ListTools filtering.
func TestToolSchemasValidatedBeforePublication(t *testing.T) {
	cases := []struct {
		name   string
		input  any
		output any
		valid  bool
	}{
		{"input properties number", map[string]any{"type": "object", "properties": 42}, nil, false},
		{"output number", map[string]any{"type": "object"}, 42, false},
		{"output properties number", map[string]any{"type": "object"}, map[string]any{"type": "object", "properties": 42}, false},
		{"output nonobject root", map[string]any{"type": "object"}, map[string]any{"type": "string"}, false},
		{"nested malformed keyword", map[string]any{"type": "object", "properties": map[string]any{"x": map[string]any{"required": 3}}}, nil, false},
		{"external reference", map[string]any{"type": "object", "properties": map[string]any{"x": map[string]any{"$ref": "https://example.invalid/schema"}}}, nil, false},
		{"nested boolean schemas", map[string]any{"type": "object", "properties": map[string]any{"x": true, "y": false}}, map[string]any{"type": "object", "additionalProperties": true}, true},
		{"valid local reference", map[string]any{"type": "object", "$defs": map[string]any{"value": map[string]any{"type": "string"}}, "properties": map[string]any{"x": map[string]any{"$ref": "#/$defs/value"}}}, nil, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var mu sync.Mutex
			current := map[string]any{"type": "object"}
			output := any(nil)
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost {
					w.WriteHeader(http.StatusMethodNotAllowed)
					return
				}
				var req struct {
					ID     json.RawMessage `json:"id"`
					Method string          `json:"method"`
				}
				if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
					t.Errorf("decode request: %v", err)
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				if len(req.ID) == 0 {
					w.WriteHeader(http.StatusAccepted)
					return
				}
				var result any
				switch req.Method {
				case "initialize":
					result = map[string]any{"protocolVersion": "2025-11-25", "capabilities": map[string]any{"tools": map[string]any{}}, "serverInfo": map[string]any{"name": "schema", "version": "1"}}
				case "tools/list":
					mu.Lock()
					tool := map[string]any{"name": "item", "inputSchema": current}
					if output != nil {
						tool["outputSchema"] = output
					}
					mu.Unlock()
					result = map[string]any{"tools": []any{tool}}
				case "tools/call":
					result = map[string]any{"content": []any{map[string]any{"type": "text", "text": "ok"}}}
				default:
					result = map[string]any{}
				}
				w.Header().Set("Content-Type", "application/json")
				if err := json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": result}); err != nil {
					t.Errorf("write response: %v", err)
				}
			}))
			t.Cleanup(upstream.Close)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			gateway, err := proxy.NewGateway(ctx, proxy.Config{Upstreams: []proxy.Upstream{{Name: "schema", URL: upstream.URL + "/mcp"}}, RefreshInterval: time.Hour})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = gateway.Close() })
			downstream := httptest.NewServer(gateway.Handler())
			t.Cleanup(downstream.Close)
			caller := client(t, downstream.URL+"/mcp")
			list, err := caller.ListTools(ctx, nil)
			if err != nil || len(list.Tools) != 1 {
				t.Fatalf("initial tools: %v %+v", err, list)
			}
			mu.Lock()
			current, output = tc.input.(map[string]any), tc.output
			mu.Unlock()
			err = gateway.Refresh(ctx)
			if (err == nil) != tc.valid {
				t.Fatalf("Refresh success = %t, want %t; error: %v", err == nil, tc.valid, err)
			}
			list, err = caller.ListTools(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			want := 0
			if tc.valid {
				want = 1
			}
			if len(list.Tools) != want {
				t.Fatalf("published tools = %+v, want %d", list.Tools, want)
			}
			if !tc.valid {
				if _, err := caller.CallTool(ctx, &mcp.CallToolParams{Name: "mcp_6_schema_item"}); err == nil {
					t.Fatal("withdrawn tool remained callable")
				}
			}
		})
	}
}
