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
	"syscall"
	"time"

	"github.com/shanduur/agentic-snake/proxy"
)

func main() {
	if err := run(); err != nil {
		log.Print(err)
		os.Exit(1)
	}
}

func run() error {
	var upstream, listen string
	flag.StringVar(&upstream, "upstream", "", "required trusted MCP upstream endpoint URL (http or https)")
	flag.StringVar(&listen, "listen", "127.0.0.1:8080", "HTTP listen address (loopback by default)")
	flag.Parse()
	handler, err := proxy.NewHandler(proxy.Config{UpstreamURL: upstream})
	if err != nil {
		return fmt.Errorf("upstream: %w", err)
	}
	listener, err := net.Listen("tcp", listen)
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}
	server := &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		MaxHeaderBytes:    16 << 10,
		IdleTimeout:       60 * time.Second,
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	log.Printf("MCP proxy listening on %s", listener.Addr())
	select {
	case err := <-done:
		if !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("serve: %w", err)
		}
		return nil
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			_ = server.Close() // Force-close long-lived SSE connections at shutdown deadline.
			return fmt.Errorf("shutdown: %w", err)
		}
		return nil
	}
}
