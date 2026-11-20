package main

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/dudo/log-ingester/internal/clickhouse"
	"github.com/dudo/log-ingester/internal/httpapi"
	"github.com/dudo/log-ingester/internal/logs"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx); err != nil {
		slog.Error("ingester stopped", "error", err)
		os.Exit(1)
	}
}

func run(ctx context.Context) error {
	store, err := clickhouse.OpenFromEnv()
	if err != nil {
		return err
	}
	if err := store.Init(ctx); err != nil {
		return err
	}

	address := os.Getenv("LISTEN_ADDR")
	if address == "" {
		address = "127.0.0.1:8080"
	}
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return err
	}
	batch := logs.NewBatcher(store, logs.BatchConfig{
		QueueDepth: 64, FlushAt: 256, FlushEvery: 200 * time.Millisecond,
	}, func(rows int, err error) {
		slog.Error("ClickHouse flush failed", "rows", rows, "error", err)
	})
	defer batch.Close()

	server := &http.Server{
		Handler:           httpapi.New(batch, store),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       30 * time.Second,
	}
	slog.Info("listening", "address", listener.Addr())
	stopped := make(chan error, 1)
	go func() { stopped <- server.Serve(listener) }()

	select {
	case err := <-stopped:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		deadline, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := server.Shutdown(deadline); err != nil {
			_ = server.Close()
			return err
		}
		return nil
	}
}
