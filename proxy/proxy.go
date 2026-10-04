// Copyright 2026 Mateusz Urbanek.

// Package proxy aggregates trusted MCP Streamable HTTP tools into one tools-only endpoint.
package proxy

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	maxUpstreams    = 32
	maxPages        = 32
	maxTools        = 1024
	maxBodyBytes    = 8 << 20
	maxConcurrent   = 64
	maxCatalogBytes = 2 << 20
)

// Upstream is one trusted, unauthenticated MCP Streamable HTTP endpoint.
type Upstream struct {
	Name string
	URL  string
}

// Config describes the bounded catalog and operation deadlines. Zero durations use defaults.
type Config struct {
	Upstreams        []Upstream
	RefreshInterval  time.Duration
	DiscoveryTimeout time.Duration
	CallTimeout      time.Duration
}

type peer struct {
	cfg      Upstream
	session  *mcp.ClientSession
	stop     context.CancelFunc
	lifetime context.Context
	names    map[string]string
	versions map[string]*mcp.Tool
}

// Gateway owns the upstream sessions and its periodic discovery worker.
type Gateway struct {
	peers            []*peer
	server           *mcp.Server
	handler          http.Handler
	cancel           context.CancelFunc
	lifetime         context.Context
	done             chan struct{}
	refreshMu        chan struct{}
	mu               sync.RWMutex
	routes           map[string]*peer
	closed           bool
	closeOnce        sync.Once
	closeDone        chan struct{}
	calls            chan struct{}
	streams          chan struct{}
	toolCalls        chan struct{}
	changed          chan struct{}
	discoveryTimeout time.Duration
	callTimeout      time.Duration
}

// NewGateway validates configuration, discovers each server, and starts periodic sweeps.
// An unavailable server starts empty and can recover at the next sweep.
func NewGateway(ctx context.Context, cfg Config) (*Gateway, error) {
	if len(cfg.Upstreams) == 0 || len(cfg.Upstreams) > maxUpstreams {
		return nil, errors.New("upstreams must contain 1..32 endpoints")
	}
	if cfg.RefreshInterval < 0 || cfg.DiscoveryTimeout < 0 || cfg.CallTimeout < 0 {
		return nil, errors.New("durations cannot be negative")
	}
	if cfg.RefreshInterval == 0 {
		cfg.RefreshInterval = 30 * time.Second
	}
	if cfg.DiscoveryTimeout == 0 {
		cfg.DiscoveryTimeout = 10 * time.Second
	}
	if cfg.CallTimeout == 0 {
		cfg.CallTimeout = 30 * time.Second
	}
	seen := map[string]bool{}
	for _, u := range cfg.Upstreams {
		if u.Name == "" || len(u.Name) > 64 || strings.Trim(u.Name, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_- ") != "" || strings.Contains(u.Name, " ") || seen[u.Name] {
			return nil, fmt.Errorf("invalid or duplicate upstream name %q", u.Name)
		}
		seen[u.Name] = true
		parsed, err := url.Parse(u.URL)
		if err != nil {
			return nil, fmt.Errorf("upstream %s: invalid URL", u.Name)
		}
		if (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Hostname() == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != "" || strings.Contains(u.URL, "#") || parsed.Opaque != "" || parsed.Path == "" || !strings.HasPrefix(parsed.Path, "/") || path.Clean(parsed.Path) != parsed.Path || parsed.EscapedPath() != parsed.Path || strings.HasSuffix(parsed.Path, "/") {
			return nil, fmt.Errorf("upstream %s: invalid canonical http(s) endpoint", u.Name)
		}
		if parsed.Port() != "" {
			port, err := strconv.Atoi(parsed.Port())
			if err != nil || port < 1 || port > 65535 {
				return nil, fmt.Errorf("upstream %s: invalid port", u.Name)
			}
		}
	}
	lifetime, cancel := context.WithCancel(context.Background())
	g := &Gateway{
		server: mcp.NewServer(&mcp.Implementation{
			Name:    "mcp-tools-gateway",
			Version: "1",
		}, &mcp.ServerOptions{
			Capabilities: &mcp.ServerCapabilities{
				Tools: &mcp.ToolCapabilities{ListChanged: true},
			},
		}),
		lifetime:         lifetime,
		cancel:           cancel,
		done:             make(chan struct{}),
		closeDone:        make(chan struct{}),
		refreshMu:        make(chan struct{}, 1),
		routes:           make(map[string]*peer),
		calls:            make(chan struct{}, maxConcurrent),
		streams:          make(chan struct{}, maxConcurrent),
		toolCalls:        make(chan struct{}, maxConcurrent),
		changed:          make(chan struct{}, 1),
		discoveryTimeout: cfg.DiscoveryTimeout,
		callTimeout:      cfg.CallTimeout,
	}
	g.server.AddReceivingMiddleware(func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			if method == "tools/list" || method == "server/discover" {
				g.mu.RLock()
				defer g.mu.RUnlock()
			}
			return next(ctx, method, req)
		}
	})
	for _, u := range cfg.Upstreams {
		g.peers = append(g.peers, &peer{cfg: u, names: make(map[string]string), versions: make(map[string]*mcp.Tool)})
	}
	g.handler = mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return g.server }, &mcp.StreamableHTTPOptions{SessionTimeout: 5 * time.Minute})
	g.handler = g.boundary(g.handler)
	g.refresh(ctx, cfg.DiscoveryTimeout, cfg.CallTimeout)
	if err := ctx.Err(); err != nil {
		cancel()
		for _, p := range g.peers {
			if p.session != nil {
				_ = p.session.Close()
			}
		}
		return nil, err
	}
	go func() {
		defer close(g.done)
		ticker := time.NewTicker(cfg.RefreshInterval)
		defer ticker.Stop()
		for {
			select {
			case <-lifetime.Done():
				return
			case <-ticker.C:
				g.refresh(lifetime, cfg.DiscoveryTimeout, cfg.CallTimeout)
			case <-g.changed:
				g.refresh(lifetime, cfg.DiscoveryTimeout, cfg.CallTimeout)
			}
		}
	}()
	return g, nil
}

// Handler returns /mcp and process-only /healthz routes.
func (g *Gateway) Handler() http.Handler { return g.handler }

// Refresh discovers all upstreams immediately; per-server failure removes its inventory.
func (g *Gateway) Refresh(ctx context.Context) error {
	return g.refresh(ctx, g.discoveryTimeout, g.callTimeout)
}

func (g *Gateway) refresh(ctx context.Context, discoveryTimeout, callTimeout time.Duration) error {
	select {
	case g.refreshMu <- struct{}{}:
		defer func() { <-g.refreshMu }()
	case <-ctx.Done():
		return ctx.Err()
	case <-g.lifetime.Done():
		return errors.New("gateway closed")
	}
	if g.lifetime.Err() != nil {
		return errors.New("gateway closed")
	}
	var failures []error
	for _, p := range g.peers {
		if ctx.Err() != nil {
			return errors.Join(append(failures, ctx.Err())...)
		}
		op, cancel := context.WithTimeout(ctx, discoveryTimeout)
		link := context.AfterFunc(g.lifetime, cancel)
		err := g.discover(op, p, callTimeout)
		link()
		cancel()
		if err != nil {
			failures = append(failures, fmt.Errorf("%s: %w", p.cfg.Name, err))
		}
	}
	return errors.Join(failures...)
}

func exported(alias, name string) string {
	prefix := "mcp_" + strconv.Itoa(len(alias)) + "_" + alias + "_"
	if len(prefix)+len(name) <= 128 {
		return prefix + name
	}
	// Shortened names occupy a disjoint namespace from length-prefixed names.
	// Include the upstream identity so different peers cannot share a hash key.
	sum := sha256.Sum256([]byte(alias + "\x00" + name))
	return "mcp_h_" + hex.EncodeToString(sum[:])
}

func validToolName(name string) bool {
	if len(name) < 1 || len(name) > 128 {
		return false
	}
	for _, c := range name {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '-' || c == '.') {
			return false
		}
	}
	return true
}

// SDK AddTool can panic on malformed peer schemas and header annotations.
// Probe on an isolated server before touching the published catalog.
func validateSDKTool(tool *mcp.Tool) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("invalid tool descriptor %q: %v", tool.Name, r)
		}
	}()
	probe := mcp.NewServer(&mcp.Implementation{Name: "descriptor-check", Version: "1"}, nil)
	probe.AddTool(tool, func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) { return nil, nil })
	return nil
}

func (g *Gateway) discover(ctx context.Context, p *peer, callTimeout time.Duration) error {
	g.mu.RLock()
	session := p.session
	g.mu.RUnlock()
	if session == nil {
		client := mcp.NewClient(&mcp.Implementation{Name: "mcp-tools-gateway", Version: "1"}, &mcp.ClientOptions{Capabilities: &mcp.ClientCapabilities{}, MultiRoundTrip: &mcp.MultiRoundTripOptions{Disabled: true}, ToolListChangedHandler: func(context.Context, *mcp.ToolListChangedRequest) {
			select {
			case g.changed <- struct{}{}:
			default:
			}
		}})
		client.AddSendingMiddleware(inventoryValidationMiddleware)
		var err error
		session, err = client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: p.cfg.URL, HTTPClient: &http.Client{Timeout: 2 * time.Minute, Transport: upstreamTransport{base: http.DefaultTransport}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, MaxRetries: -1}, nil)
		if err != nil {
			g.replace(p, nil, callTimeout)
			return err
		}
		g.mu.Lock()
		if g.closed {
			g.mu.Unlock()
			_ = session.Close()
			return errors.New("gateway closed")
		}
		p.session = session
		p.lifetime, p.stop = context.WithCancel(g.lifetime)
		g.mu.Unlock()
	}
	found := make(map[string]*mcp.Tool)
	seenNames := make(map[string]bool)
	seenExports := make(map[string]bool)
	bytesUsed := 0
	cursors := make(map[string]bool)
	cursor := ""
	for page := 0; page < maxPages; page++ {
		result, err := session.ListTools(ctx, &mcp.ListToolsParams{Cursor: cursor})
		if err != nil {
			g.fail(p)
			return err
		}
		for _, tool := range result.Tools {
			if tool == nil || !validToolName(tool.Name) || seenNames[tool.Name] || len(seenNames) >= maxTools {
				g.fail(p)
				return errors.New("invalid, duplicate or excessive tools")
			}
			schema, ok := tool.InputSchema.(map[string]any)
			if !ok || schema["type"] != "object" {
				g.fail(p)
				return fmt.Errorf("invalid input schema for %s", tool.Name)
			}
			encoded, err := json.Marshal(tool)
			if err != nil {
				g.fail(p)
				return err
			}
			bytesUsed += len(encoded)
			if bytesUsed > maxCatalogBytes || !validToolName(exported(p.cfg.Name, tool.Name)) {
				g.fail(p)
				return errors.New("tool catalog too large or invalid export")
			}
			exportedName := exported(p.cfg.Name, tool.Name)
			g.mu.RLock()
			owner := g.routes[exportedName]
			g.mu.RUnlock()
			if seenExports[exportedName] || owner != nil && owner != p {
				g.fail(p)
				return errors.New("duplicate exported tool name")
			}
			seenExports[exportedName] = true
			seenNames[tool.Name] = true
			if tool.Meta["mcp-gateway/task-required"] == true {
				continue
			}
			if err := validateSDKTool(tool); err != nil {
				g.fail(p)
				return err
			}
			found[tool.Name] = tool
		}
		if result.NextCursor == "" {
			g.replace(p, found, callTimeout)
			return nil
		}
		if cursors[result.NextCursor] {
			g.fail(p)
			return errors.New("repeated pagination cursor")
		}
		cursors[result.NextCursor] = true
		cursor = result.NextCursor
	}
	g.fail(p)
	return errors.New("pagination limit exceeded")
}

func (g *Gateway) fail(p *peer) {
	g.replace(p, nil, 0)
	g.mu.Lock()
	session := p.session
	p.session = nil
	if p.stop != nil {
		p.stop()
		p.stop = nil
	}
	g.mu.Unlock()
	if session != nil {
		_ = session.Close()
	}
}
func (g *Gateway) replace(p *peer, tools map[string]*mcp.Tool, callTimeout time.Duration) {
	g.mu.Lock()
	defer g.mu.Unlock()
	for name := range p.names {
		delete(g.routes, name)
		g.server.RemoveTools(name)
	}
	p.names = make(map[string]string)
	p.versions = make(map[string]*mcp.Tool)
	if g.closed {
		return
	}
	for name, tool := range tools {
		exportedName := exported(p.cfg.Name, name)
		cloned := *tool
		cloned.Name = exportedName
		p.names[exportedName] = name
		p.versions[exportedName] = tool
		g.routes[exportedName] = p
		original := name
		g.server.AddTool(&cloned, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			select {
			case g.toolCalls <- struct{}{}:
				defer func() { <-g.toolCalls }()
			default:
				return nil, errors.New("gateway at capacity")
			}
			if req.Params.InputResponses != nil || req.Params.RequestState != "" {
				return nil, errors.New("input retry not supported")
			}
			base, stop := context.WithCancel(ctx)
			defer stop()
			link := context.AfterFunc(g.lifetime, stop)
			defer link()
			op, cancel := context.WithTimeout(base, callTimeout)
			defer cancel()
			g.mu.RLock()
			active := !g.closed && g.routes[exportedName] == p && p.names[exportedName] == original && p.versions[exportedName] == tool
			session := p.session
			peerLifetime := p.lifetime
			g.mu.RUnlock()
			if !active || session == nil {
				return nil, errors.New("tool unavailable")
			}
			peerLink := context.AfterFunc(peerLifetime, stop)
			defer peerLink()
			meta := make(mcp.Meta)
			for key, value := range req.Params.Meta {
				if key != "progressToken" && !strings.HasPrefix(key, "io.modelcontextprotocol/") {
					meta[key] = value
				}
			}
			params := &mcp.CallToolParams{
				Meta: meta,
				Name: original,
			}
			if len(req.Params.Arguments) != 0 {
				params.Arguments = json.RawMessage(req.Params.Arguments)
			}
			result, err := session.CallTool(op, params)
			if err != nil {
				var rpc *jsonrpc.Error
				if errors.As(err, &rpc) {
					return nil, rpc
				}
				return nil, err
			}
			if result != nil && (result.NeedsInput() || result.InputRequests != nil) {
				return nil, errors.New("upstream input-required result not supported")
			}
			return result, nil
		})
	}
}

// Close stops sweeps, releases upstream sessions and rejects future calls.
func (g *Gateway) Close() error {
	g.closeOnce.Do(func() {
		g.mu.Lock()
		g.closed = true
		g.mu.Unlock()
		g.cancel()
		// Active operations are canceled before closing sessions, which can wait
		// for outstanding handlers to return.
		for session := range g.server.Sessions() {
			_ = session.Close()
		}
		<-g.done
		g.refreshMu <- struct{}{}
		defer func() { <-g.refreshMu }()
		// Withdraw every route before any potentially slow upstream DELETE. The
		// transport bounds each DELETE; parallel cleanup avoids N serial waits.
		sessions := make([]*mcp.ClientSession, 0, len(g.peers))
		for _, p := range g.peers {
			g.replace(p, nil, 0)
			g.mu.Lock()
			if p.session != nil {
				sessions = append(sessions, p.session)
				p.session = nil
			}
			if p.stop != nil {
				p.stop()
				p.stop = nil
			}
			g.mu.Unlock()
		}
		var closing sync.WaitGroup
		for _, session := range sessions {
			closing.Add(1)
			go func() {
				defer closing.Done()
				_ = session.Close()
			}()
		}
		closing.Wait()
		close(g.closeDone)
	})
	<-g.closeDone
	return nil
}

func (g *Gateway) boundary(inner http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		g.mu.RLock()
		closed := g.closed
		g.mu.RUnlock()
		if closed {
			http.Error(w, "gateway closed", http.StatusServiceUnavailable)
			return
		}
		if r.URL.EscapedPath() == "/healthz" {
			if r.Method != http.MethodGet {
				w.Header().Set("Allow", "GET")
				http.Error(w, "method not allowed", 405)
				return
			}
			w.WriteHeader(200)
			return
		}
		if r.URL.EscapedPath() != "/mcp" || r.URL.RawQuery != "" {
			http.NotFound(w, r)
			return
		}
		if r.Method != http.MethodPost && r.Method != http.MethodGet && r.Method != http.MethodDelete {
			w.Header().Set("Allow", "GET, POST, DELETE")
			http.Error(w, "method not allowed", 405)
			return
		}
		if r.Header.Get("Origin") != "" {
			http.Error(w, "origin not allowed", 403)
			return
		}
		if r.ContentLength > maxBodyBytes {
			http.Error(w, "request body too large", 413)
			return
		}
		slots := g.calls
		if r.Method == http.MethodGet {
			slots = g.streams
		}
		select {
		case slots <- struct{}{}:
			defer func() { <-slots }()
		default:
			http.Error(w, "gateway at capacity", 503)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
		inner.ServeHTTP(w, r)
	})
}
