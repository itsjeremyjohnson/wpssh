package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/builtbyrobben/wpssh/internal/mcpserver"
)

var version = "dev"

var (
	errTokenLength = errors.New("WPSMCP_TOKEN must contain at least 32 characters")
	errTLSConfig   = errors.New("WPSMCP_TLS_CERT_FILE and WPSMCP_TLS_KEY_FILE must be set together")
	errPublicHTTP  = errors.New("non-loopback listener requires TLS certificate and key")
)

func main() {
	if len(os.Args) == 2 && os.Args[1] == "--version" {
		fmt.Println(version)
		return
	}
	if err := run(); err != nil {
		slog.Error("wpssh MCP stopped", "error", err)
		os.Exit(1)
	}
}

func run() error {
	token, err := loadToken()
	if err != nil {
		return err
	}
	if len(token) < 32 {
		return errTokenLength
	}
	addr := os.Getenv("WPSMCP_LISTEN_ADDR")
	if addr == "" {
		addr = "127.0.0.1:8080"
	}
	cert, key := os.Getenv("WPSMCP_TLS_CERT_FILE"), os.Getenv("WPSMCP_TLS_KEY_FILE")
	if (cert == "") != (key == "") {
		return errTLSConfig
	}
	if cert == "" && !loopbackAddress(addr) && os.Getenv("WPSMCP_BEHIND_PROXY") != "1" {
		return errPublicHTTP
	}
	service, err := mcpserver.NewService()
	if err != nil {
		return fmt.Errorf("initialize WordPress service: %w", err)
	}
	defer service.Close()

	mux := http.NewServeMux()
	mux.Handle("/mcp", mcpserver.BearerAuth(token, mcpserver.Handler(service, os.Getenv("WPSMCP_ALLOW_WRITES") == "1")))
	server := &http.Server{Addr: addr, Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	shutdownDone := make(chan struct{})
	go func() {
		defer close(shutdownDone)
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
	}()
	slog.Info("wpssh MCP listening", "address", addr)
	if cert != "" {
		err = server.ListenAndServeTLS(cert, key)
	} else {
		err = server.ListenAndServe()
	}
	if errors.Is(err, http.ErrServerClosed) {
		<-shutdownDone
		return nil
	}
	stop()
	<-shutdownDone
	return fmt.Errorf("serve MCP: %w", err)
}

func loadToken() (string, error) {
	if path := os.Getenv("WPSMCP_TOKEN_FILE"); path != "" {
		data, err := os.ReadFile(path) // #nosec G304 -- the operator supplies the secret file path.
		if err != nil {
			return "", fmt.Errorf("read WPSMCP_TOKEN_FILE: %w", err)
		}
		return string(bytes.TrimSpace(data)), nil
	}
	return os.Getenv("WPSMCP_TOKEN"), nil
}

func loopbackAddress(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return false
	}
	return net.ParseIP(host) != nil && net.ParseIP(host).IsLoopback()
}
