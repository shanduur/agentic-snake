// Copyright 2026 Mateusz Urbanek.

package proxy_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/shanduur/agentic-snake/proxy"
)

func routedTool(t *testing.T, handler mcp.ToolHandler) *mcp.ClientSession {
	t.Helper()
	server := mcp.NewServer(
		&mcp.Implementation{Name: "alpha", Version: "1"},
		&mcp.ServerOptions{
			Capabilities: &mcp.ServerCapabilities{Tools: &mcp.ToolCapabilities{}},
		},
	)
	server.AddTool(&mcp.Tool{
		Name:        "result",
		InputSchema: map[string]any{"type": "object"},
	}, handler)
	upstream := httptest.NewServer(mcp.NewStreamableHTTPHandler(
		func(*http.Request) *mcp.Server { return server },
		nil,
	))
	t.Cleanup(upstream.Close)
	gateway, err := proxy.NewGateway(context.Background(), proxy.Config{
		Upstreams: []proxy.Upstream{
			{Name: "alpha", URL: upstream.URL + "/mcp"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = gateway.Close() })
	downstream := httptest.NewServer(gateway.Handler())
	t.Cleanup(downstream.Close)
	return client(t, downstream.URL+"/mcp")
}

func TestForwardsApplicationMetadataAndBinaryErrorResult(t *testing.T) {
	binary := []byte{0, 255, 17, 128}
	caller := routedTool(t, func(_ context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if req.Params.Meta["fixture"] != "caller" {
			return nil, &jsonrpc.Error{
				Code:    jsonrpc.CodeInvalidParams,
				Message: "application metadata was not forwarded",
			}
		}
		return &mcp.CallToolResult{
			Meta: mcp.Meta{"fixture": "result"},
			Content: []mcp.Content{
				&mcp.TextContent{Text: "execution failed"},
				&mcp.ImageContent{
					Data:     binary,
					MIMEType: "image/png",
				},
			},
			StructuredContent: map[string]any{"fixture": "structured"},
			IsError:           true,
		}, nil
	})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	result, err := caller.CallTool(ctx, &mcp.CallToolParams{
		Meta:      mcp.Meta{"fixture": "caller"},
		Name:      "mcp_5_alpha_result",
		Arguments: json.RawMessage(`{"value":9007199254740993}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError || result.Meta["fixture"] != "result" || len(result.Content) != 2 {
		t.Fatalf("changed execution-error result: %+v", result)
	}
	image, ok := result.Content[1].(*mcp.ImageContent)
	if !ok || !bytes.Equal(image.Data, binary) || image.MIMEType != "image/png" {
		t.Fatalf("changed binary content: %+v", result.Content)
	}
	structured, ok := result.StructuredContent.(map[string]any)
	if !ok || structured["fixture"] != "structured" {
		t.Fatalf("changed structured content: %+v", result.StructuredContent)
	}
}

func TestPreservesUpstreamProtocolError(t *testing.T) {
	data := json.RawMessage(`{"fixture":"denied"}`)
	caller := routedTool(t, func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return nil, &jsonrpc.Error{
			Code:    jsonrpc.CodeInvalidParams,
			Message: "denied",
			Data:    data,
		}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, err := caller.CallTool(ctx, &mcp.CallToolParams{Name: "mcp_5_alpha_result"})
	var protocolError *jsonrpc.Error
	if !errors.As(err, &protocolError) {
		t.Fatalf("expected JSON-RPC error: %v", err)
	}
	if protocolError.Code != jsonrpc.CodeInvalidParams || protocolError.Message != "denied" || !bytes.Equal(protocolError.Data, data) {
		t.Fatalf("changed protocol error: %+v", protocolError)
	}
}
