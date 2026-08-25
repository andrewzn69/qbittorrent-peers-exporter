package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

func main() {
	if err := run(); err != nil {
		slog.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func run() error {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: logLevel(env("LOG_LEVEL", "info")),
	})))

	var (
		base   = env("QBIT_URL", "http://localhost:8080")
		user   = env("QBIT_USERNAME", "admin")
		pass   = os.Getenv("QBIT_PASSWORD")
		addr   = env("LISTEN_ADDR", ":9714")
		filter = env("TORRENT_FILTER", "active")
	)
	if pass == "" {
		return errors.New("QBIT_PASSWORD is required")
	}

	interval, err := time.ParseDuration(env("REFRESH_INTERVAL", "30s"))
	if err != nil {
		return fmt.Errorf("REFRESH_INTERVAL: %w", err)
	}
	timeout, err := time.ParseDuration(env("HTTP_TIMEOUT", "15s"))
	if err != nil {
		return fmt.Errorf("HTTP_TIMEOUT: %w", err)
	}
	workers, err := strconv.Atoi(env("WORKERS", "8"))
	if err != nil || workers < 1 {
		return errors.New("WORKERS must be a positive integer")
	}

	client, err := NewClient(base, user, pass, timeout)
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// fail fast on bad creds rather than looking healthy and serving nothing
	if err := client.Login(ctx); err != nil {
		return fmt.Errorf("login: %w", err)
	}
	slog.Info("logged in", "url", base, "user", user)

	col := NewCollector(client, filter, workers)
	go col.Run(ctx, interval)

	reg := prometheus.NewRegistry()
	reg.MustRegister(
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
		col,
	)

	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.HandlerFor(reg, promhttp.HandlerOpts{}))
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprintln(w, "ok")
	})

	srv := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	go func() {
		<-ctx.Done()
		slog.Info("shutting down")
		sc, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		srv.Shutdown(sc)
	}()

	slog.Info("listening", "addr", addr, "interval", interval.String(), "filter", filter)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func logLevel(s string) slog.Level {
	var l slog.Level
	if err := l.UnmarshalText([]byte(s)); err != nil {
		return slog.LevelInfo
	}
	return l
}
