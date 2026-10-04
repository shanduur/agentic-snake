// Copyright 2026 Mateusz Urbanek.

package main_test

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func buildCLI(t *testing.T) string {
	t.Helper()
	binary := filepath.Join(t.TempDir(), "mcp-proxy")
	if output, err := exec.Command("go", "build", "-o", binary, ".").CombinedOutput(); err != nil {
		t.Fatalf("build: %s: %v", output, err)
	}
	return binary
}

func TestCLIStartsAndShutsDownWithActiveSession(t *testing.T) {
	upstream := mcp.NewServer(
		&mcp.Implementation{
			Name:    "up",
			Version: "1",
		},
		&mcp.ServerOptions{
			Capabilities: &mcp.ServerCapabilities{
				Tools: &mcp.ToolCapabilities{},
			},
		},
	)
	upstream.AddTool(
		&mcp.Tool{
			Name:        "ping",
			InputSchema: map[string]any{"type": "object"},
		},
		func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return &mcp.CallToolResult{
				Content: []mcp.Content{&mcp.TextContent{Text: "pong"}},
			}, nil
		},
	)
	endpoint := httptest.NewServer(mcp.NewStreamableHTTPHandler(
		func(*http.Request) *mcp.Server { return upstream },
		nil,
	))
	t.Cleanup(endpoint.Close)
	binary := buildCLI(t)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, binary,
		"--listen", address,
		"--upstream", "up="+endpoint.URL+"/mcp",
		"--refresh-interval", "100ms",
		"--discovery-timeout", "1s",
		"--call-timeout", "1s",
	)
	if err := cmd.Start(); err != nil {
		cancel()
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
		close(done)
	}()
	t.Cleanup(func() {
		cancel()
		<-done
	})
	url := "http://" + address
	healthClient := &http.Client{Timeout: 100 * time.Millisecond}
	deadline := time.Now().Add(3 * time.Second)
	ready := false
	for time.Now().Before(deadline) {
		resp, err := healthClient.Get(url + "/healthz")
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				ready = true
				break
			}
		}
		select {
		case err := <-done:
			t.Fatalf("CLI exited before readiness: %v", err)
		default:
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !ready {
		t.Fatal("CLI did not become ready")
	}
	client := mcp.NewClient(
		&mcp.Implementation{Name: "cli-test", Version: "1"},
		&mcp.ClientOptions{
			ToolListChangedHandler: func(context.Context, *mcp.ToolListChangedRequest) {},
		},
	)
	op, stop := context.WithTimeout(context.Background(), 3*time.Second)
	defer stop()
	session, err := client.Connect(op, &mcp.StreamableClientTransport{
		Endpoint:   url + "/mcp",
		MaxRetries: -1,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	tools, err := session.ListTools(op, nil)
	if err != nil || len(tools.Tools) != 1 || tools.Tools[0].Name != "mcp_2_up_ping" {
		t.Fatalf("CLI catalog: %+v: %v", tools, err)
	}
	result, err := session.CallTool(op, &mcp.CallToolParams{Name: tools.Tools[0].Name})
	if err != nil || len(result.Content) != 1 || result.Content[0].(*mcp.TextContent).Text != "pong" {
		t.Fatalf("CLI call: %+v: %v", result, err)
	}
	if err := cmd.Process.Signal(os.Interrupt); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("CLI shutdown: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("active downstream session blocked CLI shutdown")
	}
}

func TestInvalidCLIConfig(t *testing.T) {
	binary := buildCLI(t)
	for _, args := range [][]string{
		{},
		{"--upstream", "bad"},
		{"--upstream", "a=ftp://host/mcp"},
		{"--upstream", "a=http://host/mcp", "--upstream", "a=http://host/mcp"},
		{"--refresh-interval", "-1s", "--upstream", "a=http://host/mcp"},
		{"--refresh-interval", "0s", "--upstream", "a=http://host/mcp"},
		{"--call-timeout", "0s", "--upstream", "a=http://host/mcp"},
		{"--discovery-timeout", "-1s", "--upstream", "a=http://host/mcp"},
		{"--listen", "", "--upstream", "a=http://host/mcp"},
		{"unexpected"},
	} {
		t.Run(argsString(args), func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			output, err := exec.CommandContext(ctx, binary, args...).CombinedOutput()
			if err == nil || len(output) == 0 {
				t.Fatalf("expected configuration error: %s: %v", output, err)
			}
		})
	}
}

func argsString(args []string) string {
	if len(args) == 0 {
		return "missing_upstream"
	}
	return strings.Join(args, "_")
}
