// Copyright 2026 Mateusz Urbanek.

package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/shanduur/agentic-snake/proxy"
)

type upstreamFlags []proxy.Upstream

func (u *upstreamFlags) String() string {
	return fmt.Sprint([]proxy.Upstream(*u))
}
func (u *upstreamFlags) Set(value string) error {
	name, endpoint, ok := strings.Cut(value, "=")
	if !ok || name == "" || endpoint == "" {
		return errors.New("upstream must be NAME=URL")
	}
	*u = append(*u, proxy.Upstream{
		Name: name,
		URL:  endpoint,
	})
	return nil
}

func main() {
	if err := run(); err != nil {
		log.Print(err)
		os.Exit(1)
	}
}

func run() error {
	var upstreams upstreamFlags
	var listen string
	var interval time.Duration
	var discoveryTimeout time.Duration
	var callTimeout time.Duration
	flag.Var(&upstreams, "upstream", "trusted upstream NAME=URL; repeat for each endpoint")
	flag.StringVar(&listen, "listen", "127.0.0.1:8080", "HTTP listen address (loopback by default)")
	flag.DurationVar(&interval, "refresh-interval", 30*time.Second, "periodic complete tool inventory refresh")
	flag.DurationVar(&discoveryTimeout, "discovery-timeout", 10*time.Second, "deadline per upstream discovery")
	flag.DurationVar(&callTimeout, "call-timeout", 30*time.Second, "deadline per tool call")
	flag.Parse()
	if flag.NArg() != 0 {
		return errors.New("unexpected positional arguments")
	}
	if listen == "" || interval <= 0 || discoveryTimeout <= 0 || callTimeout <= 0 {
		return errors.New("listen must be nonempty and refresh interval and operation timeouts must be positive")
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	gateway, err := proxy.NewGateway(ctx, proxy.Config{
		Upstreams:        upstreams,
		RefreshInterval:  interval,
		DiscoveryTimeout: discoveryTimeout,
		CallTimeout:      callTimeout,
	})
	if err != nil {
		return fmt.Errorf("upstream configuration: %w", err)
	}
	defer gateway.Close()
	listener, err := net.Listen("tcp", listen)
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}
	server := &http.Server{
		Handler:           gateway.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		MaxHeaderBytes:    16 << 10,
		IdleTimeout:       60 * time.Second,
	}
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	log.Printf("MCP tools gateway listening on %s", listener.Addr())
	select {
	case err := <-done:
		if !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("serve: %w", err)
		}
		return nil
	case <-ctx.Done():
		// Close protocol sessions before waiting for HTTP handlers, including
		// long-lived SSE requests, to return.
		if err := gateway.Close(); err != nil {
			_ = server.Close()
			return fmt.Errorf("gateway shutdown: %w", err)
		}
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			_ = server.Close()
			return fmt.Errorf("shutdown: %w", err)
		}
		return nil
	}
}
