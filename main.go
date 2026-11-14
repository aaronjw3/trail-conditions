package main

import (
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
)

//go:embed web/* examples/*
var assets embed.FS

type Snapshot struct {
	Route       Route      `json:"route"`
	Forecasts   []Forecast `json:"forecasts"`
	RetrievedAt time.Time  `json:"retrieved_at"`
}
type app struct {
	mapboxToken string
	mu          sync.RWMutex
	snapshot    *Snapshot
	busy        chan struct{}
	file        string
	client      *http.Client
	endpoint    string
}

func (a *app) collect(ctx context.Context, route Route, points []Point) error {
	next := &Snapshot{Route: route}
	for _, point := range points {
		forecast, err := fetchForecast(ctx, a.client, a.endpoint, point)
		if err != nil {
			return err
		}
		if len(next.Forecasts) > 0 && forecast.Times[0] != next.Forecasts[0].Times[0] {
			return fmt.Errorf("forecast dates changed during collection; try again")
		}
		next.Forecasts = append(next.Forecasts, forecast)
	}
	next.RetrievedAt = time.Now().UTC()
	encoded, err := json.Marshal(next)
	if err != nil {
		return err
	}
	// Persist the whole result before publishing it to readers. Rename is atomic
	// because the temporary file lives in the same directory as the destination.
	f, err := os.CreateTemp(filepath.Dir(a.file), ".snapshot-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(encoded); err != nil {
		f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	if err = os.Rename(f.Name(), a.file); err != nil {
		return err
	}
	a.mu.Lock()
	a.snapshot = next
	a.mu.Unlock()
	return nil
}

func (a *app) routes() http.Handler {
	mux := http.NewServeMux()
	web, _ := fs.Sub(assets, "web")
	mux.Handle("GET /", http.FileServer(http.FS(web)))
	mux.HandleFunc("GET /api/map-config", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if !strings.HasPrefix(a.mapboxToken, "pk.") {
			http.Error(w, "Set MAPBOX_PUBLIC_TOKEN to a public pk. token and restart the server.", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(struct {
			Token string `json:"token"`
		}{a.mapboxToken})
	})

	mux.HandleFunc("GET /example.gpx", func(w http.ResponseWriter, r *http.Request) {
		data, _ := assets.ReadFile("examples/foothills.gpx")
		w.Header().Set("Content-Type", "application/gpx+xml")
		w.Write(data)
	})
	mux.HandleFunc("GET /api/snapshot", func(w http.ResponseWriter, r *http.Request) {
		a.mu.RLock()
		snapshot := a.snapshot
		a.mu.RUnlock()
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(snapshot)
	})
	mux.HandleFunc("GET /panel", a.panel)
	for _, path := range []string{"/forecast", "/refresh", "/example"} {
		mux.HandleFunc("POST "+path, a.refresh)
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
		mux.ServeHTTP(w, r)
	})
}

func (a *app) refresh(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("HX-Request") != "true" {
		http.Error(w, "missing request header", http.StatusForbidden)
		return
	}
	select {
	case a.busy <- struct{}{}:
		defer func() { <-a.busy }()
	default:
		http.Error(w, "A forecast request is already running.", http.StatusConflict)
		return
	}
	var route Route
	var points []Point
	var err error
	switch r.URL.Path {
	case "/refresh":
		a.mu.RLock()
		previous := a.snapshot
		a.mu.RUnlock()
		if previous == nil {
			http.Error(w, "Upload a route first.", 400)
			return
		}
		route = previous.Route
		for _, f := range previous.Forecasts {
			points = append(points, f.Point)
		}
	case "/example":
		data, _ := assets.ReadFile("examples/foothills.gpx")
		route, points, err = parseRoute(bytes.NewReader(data))
	default:
		r.Body = http.MaxBytesReader(w, r.Body, (2<<20)+65536)
		if err = r.ParseMultipartForm(2 << 20); err != nil {
			http.Error(w, "Upload a GPX file no larger than 2 MB.", 400)
			return
		}
		defer r.MultipartForm.RemoveAll()
		file, header, e := r.FormFile("route")
		if e != nil {
			http.Error(w, "Choose a GPX file.", 400)
			return
		}
		defer file.Close()
		if header.Size > 2<<20 {
			http.Error(w, "GPX file exceeds 2 MB.", 400)
			return
		}
		route, points, err = parseRoute(file)
	}
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
	defer cancel()
	start := time.Now()
	err = a.collect(ctx, route, points)
	slog.Info("forecast refresh", "samples", len(points), "elapsed", time.Since(start), "success", err == nil)
	if err != nil {
		slog.Error("refresh failed", "error", err)
		http.Error(w, "Refresh failed. The previous complete forecast is unchanged. Check the server log and retry.", 502)
		return
	}
	a.panel(w, r)
}

func main() {
	dir := os.Getenv("TRAIL_DATA_DIR")
	if dir == "" {
		dir = "data"
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		slog.Error("create data directory", "error", err)
		os.Exit(1)
	}
	a := &app{mapboxToken: strings.TrimSpace(os.Getenv("MAPBOX_PUBLIC_TOKEN")), busy: make(chan struct{}, 1), file: filepath.Join(dir, "snapshot.json"), endpoint: "https://api.open-meteo.com/v1/forecast", client: &http.Client{Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
	f, err := os.Open(a.file)
	if err == nil {
		data, readErr := io.ReadAll(io.LimitReader(f, 8<<20))
		f.Close()
		if readErr != nil || json.Unmarshal(data, &a.snapshot) != nil || a.snapshot == nil || len(a.snapshot.Forecasts) == 0 {
			slog.Error("stored snapshot is invalid; move it aside to start fresh")
			os.Exit(1)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		slog.Error("read snapshot", "error", err)
		os.Exit(1)
	}
	server := &http.Server{Addr: "127.0.0.1:8080", Handler: a.routes(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 20 * time.Second, WriteTimeout: 100 * time.Second, IdleTimeout: 60 * time.Second}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		deadline, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		server.Shutdown(deadline)
	}()
	slog.Info("Trail Conditions ready", "url", "http://127.0.0.1:8080")
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		slog.Error("server stopped", "error", err)
		os.Exit(1)
	}
}
