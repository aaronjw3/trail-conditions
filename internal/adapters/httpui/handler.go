package httpui

import (
	"bytes"
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
	"github.com/aaronjw3/trail-conditions/internal/domain"
)

type Handler struct {
	service     *application.Service
	assets      fs.FS
	mapboxToken string
	templates   *template.Template
}

func New(service *application.Service, assets fs.FS, token string) (*Handler, error) {
	templates, err := template.ParseFS(assets, "web/*.html")
	if err != nil {
		return nil, err
	}
	return &Handler{service: service, assets: assets, mapboxToken: token, templates: templates}, nil
}
func (a *Handler) Routes() http.Handler {
	mux := http.NewServeMux()
	web, _ := fs.Sub(a.assets, "web")
	mux.Handle("GET /app.js", http.FileServer(http.FS(web)))
	mux.Handle("GET /style.css", http.FileServer(http.FS(web)))
	mux.HandleFunc("GET /{$}", a.page)
	mux.HandleFunc("GET /routes/{id}", a.page)
	mux.HandleFunc("GET /routes", a.savedRoutes)
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

	mux.HandleFunc("GET /api/routes/{id}", func(w http.ResponseWriter, r *http.Request) {
		snapshot, err := a.service.Snapshot(r.Context(), r.PathValue("id"))
		if err != nil {
			if errors.Is(err, application.ErrNotFound) {
				http.NotFound(w, r)
			} else {
				http.Error(w, "Could not load route.", 500)
			}
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(snapshot)
	})
	mux.HandleFunc("GET /panel", a.panel)
	mux.HandleFunc("GET /routes/{id}/panel", a.panel)
	for _, path := range []string{"/forecast", "/routes/{id}/refresh"} {
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
	var snapshot *domain.Snapshot
	if id := r.PathValue("id"); id != "" {
		snapshot, err = a.service.Refresh(ctx, id)
	} else {
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
		snapshot, err = a.service.Import(ctx, header.Filename, data)
	}
	slog.Info("forecast refresh", "elapsed", time.Since(start), "success", err == nil)
	var invalid *application.InputError
	if errors.As(err, &invalid) {
		http.Error(w, invalid.Error(), 400)
		return
	}
	if errors.Is(err, application.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if errors.Is(err, application.ErrCapacity) {
		w.Header().Set("Retry-After", "5")
		http.Error(w, err.Error(), http.StatusTooManyRequests)
		return
	}
	if errors.Is(err, application.ErrBusy) {
		http.Error(w, err.Error(), 409)
		return
	}
	if err != nil {
		slog.Error("refresh failed", "error", err)
		message := "Refresh failed. The previous complete forecast is unchanged. Check the server log and retry."
		if errors.Is(err, application.ErrWeatherUnavailable) {
			message = "Could not retrieve weather from Open-Meteo. The route and forecast were not updated. Your previous complete forecast is unchanged. Try again shortly."
		}
		http.Error(w, message, http.StatusBadGateway)
		return
	}
	if r.PathValue("id") == "" {
		w.Header().Set("HX-Redirect", "/routes/"+snapshot.ID)
		return
	}
	w.Header().Set("HX-Trigger-After-Swap", "routesChanged")
	r.SetPathValue("id", snapshot.ID)
	a.panel(w, r)
}

func (a *Handler) page(w http.ResponseWriter, r *http.Request) {
	panelURL := "/panel"
	if id := r.PathValue("id"); id != "" {
		if _, err := a.service.Snapshot(r.Context(), id); err != nil {
			if errors.Is(err, application.ErrNotFound) {
				http.NotFound(w, r)
			} else {
				http.Error(w, "Could not load route.", 500)
			}
			return
		}
		panelURL = "/routes/" + id + "/panel"
	}
	var out bytes.Buffer
	if err := a.templates.ExecuteTemplate(&out, "index.html", struct{ PanelURL string }{panelURL}); err != nil {
		http.Error(w, "Could not render page.", 500)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(out.Bytes())
}

func (a *Handler) savedRoutes(w http.ResponseWriter, r *http.Request) {
	routes, err := a.service.Routes(r.Context())
	if err != nil {
		http.Error(w, "Could not load saved routes.", 500)
		return
	}
	var out bytes.Buffer
	if err := a.templates.ExecuteTemplate(&out, "routes.html", routes); err != nil {
		http.Error(w, "Could not render routes.", 500)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(out.Bytes())
}
