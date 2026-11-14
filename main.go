package main

import (
	"context"
	"embed"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/aaronjw3/trail-conditions/internal/adapters/geoimport"
	"github.com/aaronjw3/trail-conditions/internal/adapters/httpui"
	"github.com/aaronjw3/trail-conditions/internal/adapters/openmeteo"
	"github.com/aaronjw3/trail-conditions/internal/adapters/snapshotfile"
	"github.com/aaronjw3/trail-conditions/internal/application"
)

//go:embed web/* examples/*
var assets embed.FS

func main() {
	dir := os.Getenv("TRAIL_DATA_DIR")
	if dir == "" {
		dir = "data"
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		slog.Error("create data directory", "error", err)
		os.Exit(1)
	}

	weather := openmeteo.Client{HTTP: &http.Client{Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, Endpoint: "https://api.open-meteo.com/v1/forecast"}
	store := snapshotfile.Store{Path: filepath.Join(dir, "snapshot.json")}
	service, err := application.New(context.Background(), geoimport.Decoder{}, weather, store)
	if err != nil {
		slog.Error("initialize application", "error", err)
		os.Exit(1)
	}
	handler, err := httpui.New(service, assets, strings.TrimSpace(os.Getenv("MAPBOX_PUBLIC_TOKEN")))
	if err != nil {
		slog.Error("initialize HTTP interface", "error", err)
		os.Exit(1)
	}

	listen := os.Getenv("TRAIL_LISTEN_ADDR")
	if listen == "" {
		listen = "127.0.0.1:8080"
	}
	server := &http.Server{Addr: listen, Handler: handler.Routes(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 20 * time.Second, WriteTimeout: 100 * time.Second, IdleTimeout: 60 * time.Second}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	shutdownDone := make(chan struct{})
	go func() {
		defer close(shutdownDone)
		<-ctx.Done()
		deadline, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := server.Shutdown(deadline); err != nil {
			server.Close()
		}
	}()
	slog.Info("Trail Conditions starting", "address", listen)
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		slog.Error("server stopped", "error", err)
		os.Exit(1)
	}
	<-shutdownDone
}
