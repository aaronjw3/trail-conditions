package httpui

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/aaronjw3/trail-conditions/internal/domain"
)

type reading struct {
	Index                            int
	Temperature, Precipitation, Wind float64
	Point                            domain.Point
}

func (a *Handler) panel(w http.ResponseWriter, r *http.Request) {
	snapshot := a.service.Snapshot()
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if snapshot == nil {
		w.Write([]byte(`<p class="empty">Upload a GPX, KML, or KMZ route to see its forecast.</p>`))
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
	if hour < 0 || hour >= 72 {
		http.Error(w, "Invalid forecast hour.", 400)
		return
	}
	metric := r.URL.Query().Get("metric")
	if metric != "precipitation" && metric != "wind" {
		metric = "temperature"
	}
	rows := make([]reading, 0, len(snapshot.Forecasts))
	for i, f := range snapshot.Forecasts {
		rows = append(rows, reading{i + 1, f.Temperature[hour], f.Precipitation[hour], f.Wind[hour], f.Point})
	}
	data, _ := json.Marshal(struct {
		Snapshot *domain.Snapshot `json:"snapshot"`
		Hour     int              `json:"hour"`
		Metric   string           `json:"metric"`
	}{snapshot, hour, metric})
	view := struct {
		Hour                          int
		Metric, Time, Retrieved, Data string
		Rows                          []reading
	}{hour, metric, time.Unix(snapshot.Forecasts[0].Times[hour], 0).UTC().Format("Mon Jan 2 · 15:04 UTC"), snapshot.RetrievedAt.Format(time.RFC3339), string(data), rows}
	var out bytes.Buffer
	if err := a.panelTemplate.Execute(&out, view); err != nil {
		http.Error(w, "Could not render forecast.", 500)
		return
	}
	w.Write(out.Bytes())
}
