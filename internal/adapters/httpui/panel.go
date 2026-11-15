package httpui

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/aaronjw3/trail-conditions/internal/application"
	"github.com/aaronjw3/trail-conditions/internal/domain"
)

type reading struct {
	Index                            int
	Temperature, Precipitation, Wind float64
	Point                            domain.Point
}

func (a *Handler) panel(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if r.PathValue("id") == "" {
		w.Write([]byte(`<p class="empty">Upload a GPX, KML, or KMZ route to see its forecast.</p>`))
		return
	}
	snapshot, err := a.service.Snapshot(r.Context(), r.PathValue("id"))
	if err != nil {
		if errors.Is(err, application.ErrNotFound) {
			http.NotFound(w, r)
		} else {
			http.Error(w, "Could not load route.", 500)
		}
		return
	}
	hour, err := strconv.Atoi(r.URL.Query().Get("hour"))
	if err != nil {
		for i, t := range snapshot.Forecasts[0].Times {
			if t >= time.Now().Unix() {
				hour = i
				break
			}
		}
	}
	if hour < 0 || hour >= domain.ForecastHours {
		http.Error(w, "Invalid forecast hour.", 400)
		return
	}
	metric := r.URL.Query().Get("metric")
	if metric != "precipitation" && metric != "wind" {
		metric = "temperature"
	}
	rows := make([]reading, 0, len(snapshot.Forecasts))
	for i, f := range snapshot.Forecasts {
		rows = append(rows, reading{
			Index:         i + 1,
			Temperature:   f.Temperature[hour],
			Precipitation: f.Precipitation[hour],
			Wind:          f.Wind[hour],
			Point:         f.Point,
		})
	}
	data, _ := json.Marshal(struct {
		Snapshot *domain.Snapshot `json:"snapshot"`
		Hour     int              `json:"hour"`
		Metric   string           `json:"metric"`
	}{Snapshot: snapshot, Hour: hour, Metric: metric})
	view := struct {
		Hour, MaxHour, ForecastHours            int
		ID, Name, Metric, Time, Retrieved, Data string
		Rows                                    []reading
	}{
		ID:            snapshot.ID,
		Name:          snapshot.Name,
		Hour:          hour,
		MaxHour:       domain.ForecastHours - 1,
		ForecastHours: domain.ForecastHours,
		Metric:        metric,
		Time:          time.Unix(snapshot.Forecasts[0].Times[hour], 0).UTC().Format("Mon Jan 2 · 15:04 UTC"),
		Retrieved:     snapshot.RetrievedAt.Format(time.RFC3339),
		Data:          string(data),
		Rows:          rows,
	}
	var out bytes.Buffer
	if err := a.templates.ExecuteTemplate(&out, "panel.html", view); err != nil {
		http.Error(w, "Could not render forecast.", 500)
		return
	}
	w.Write(out.Bytes())
}
