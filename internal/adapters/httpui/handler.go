package httpui

import (
	"context"
	"encoding/json"
	"errors"
	"html/template"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/aaronjw3/trail-conditions/internal/application"
)

type Handler struct {
	service       *application.Service
	assets        fs.FS
	mapboxToken   string
	panelTemplate *template.Template
}

func New(service *application.Service, assets fs.FS, token string) (*Handler, error) {
	panel, err := template.ParseFS(assets, "web/panel.html")
	if err != nil {
		return nil, err
	}
	return &Handler{service: service, assets: assets, mapboxToken: token, panelTemplate: panel}, nil
}
func (a *Handler) Routes() http.Handler {
	mux := http.NewServeMux()
	web, _ := fs.Sub(a.assets, "web")
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
		data, _ := fs.ReadFile(a.assets, "examples/foothills.gpx")
		w.Header().Set("Content-Type", "application/gpx+xml")
		w.Write(data)
	})
	mux.HandleFunc("GET /api/snapshot", func(w http.ResponseWriter, r *http.Request) {
		snapshot := a.service.Snapshot()
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

func (a *Handler) refresh(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("HX-Request") != "true" {
		http.Error(w, "missing request header", http.StatusForbidden)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
	defer cancel()
	start := time.Now()
	var err error
	switch r.URL.Path {
	case "/refresh":
		err = a.service.Refresh(ctx)
	case "/example":
		data, _ := fs.ReadFile(a.assets, "examples/foothills.gpx")
		err = a.service.Import(ctx, "foothills.gpx", data)
	default:
		r.Body = http.MaxBytesReader(w, r.Body, (2<<20)+65536)
		if err = r.ParseMultipartForm(2 << 20); err != nil {
			http.Error(w, "Upload a GPX, KML, or KMZ file no larger than 2 MB.", 400)
			return
		}
		defer r.MultipartForm.RemoveAll()
		file, header, e := r.FormFile("route")
		if e != nil {
			http.Error(w, "Choose a route file.", 400)
			return
		}
		defer file.Close()
		data, e := io.ReadAll(io.LimitReader(file, (2<<20)+1))
		if e != nil || len(data) > 2<<20 {
			http.Error(w, "Could not read route or file exceeds 2 MB.", 400)
			return
		}
		err = a.service.Import(ctx, header.Filename, data)
	}
	slog.Info("forecast refresh", "elapsed", time.Since(start), "success", err == nil)
	var invalid *application.InputError
	if errors.As(err, &invalid) {
		http.Error(w, invalid.Error(), 400)
		return
	}
	if errors.Is(err, application.ErrBusy) {
		http.Error(w, err.Error(), 409)
		return
	}
	if err != nil {
		slog.Error("refresh failed", "error", err)
		http.Error(w, "Refresh failed. The previous complete forecast is unchanged. Check the server log and retry.", 502)
		return
	}
	a.panel(w, r)
}
