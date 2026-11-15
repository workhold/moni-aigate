package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/moni-ai/moni-aigate/internal/testsupport/mockupstream"
)

func main() {
	if os.Getenv("MOCK_UPSTREAM_ENABLE") != "1" {
		fmt.Fprintln(os.Stderr, "MOCK_UPSTREAM_ENABLE=1 is required")
		os.Exit(2)
	}
	configPath := flag.String("config", os.Getenv("MOCK_UPSTREAM_CONFIG"), "candidate JSON configuration")
	addr := flag.String("addr", getenv("MOCK_UPSTREAM_ADDR", ":8090"), "listen address")
	allowDirectives := flag.Bool("allow-directives", os.Getenv("MOCK_UPSTREAM_ALLOW_DIRECTIVES") == "1", "enable request directives and receipts")
	flag.Parse()
	if *configPath == "" {
		fmt.Fprintln(os.Stderr, "mock upstream config is required")
		os.Exit(2)
	}
	cfg, err := mockupstream.LoadConfig(*configPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	service, err := mockupstream.New(cfg, mockupstream.Options{AllowDirectives: *allowDirectives})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	server := &http.Server{Addr: *addr, Handler: service.Handler(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 30 * time.Second, WriteTimeout: 0, IdleTimeout: 2 * time.Minute}
	stopped := make(chan os.Signal, 1)
	signal.Notify(stopped, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-stopped
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = server.Shutdown(ctx)
	}()
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func getenv(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
