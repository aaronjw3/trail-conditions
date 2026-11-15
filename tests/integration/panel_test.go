//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/aaronjw3/trail-conditions/internal/adapters/httpui"
	"github.com/aaronjw3/trail-conditions/internal/domain"
)

func TestPanelReadsForecastsWithoutGeometryAndReflectsRefresh(t *testing.T) {
	valid := forecastJSON(t, false)
	var calls atomic.Int32
	var refreshed atomic.Bool
	service, _, pool := newService(t, func(*http.Request) (*http.Response, error) {
		calls.Add(1)
		if refreshed.Load() {
			return response(200, strings.ReplaceAll(valid, "12.5", "22.5")), nil
		}
		return response(200, valid), nil
	})
	var gpx strings.Builder
	gpx.WriteString("<gpx><trk><trkseg>")
	for i := 0; i < domain.MaxRoutePoints; i++ {
		fmt.Fprintf(&gpx, `<trkpt lon="-111" lat="%.6f"/>`, 45+float64(i)/100000)
	}
	gpx.WriteString("</trkseg></trk></gpx>")
	snapshot, err := service.Import(context.Background(), "large.gpx", []byte(gpx.String()))
	if err != nil {
		t.Fatal(err)
	}
	ui, err := httpui.New(service, os.DirFS("../.."), "")
	if err != nil {
		t.Fatal(err)
	}
	handler := ui.Routes()
	page := httptest.NewRecorder()
	handler.ServeHTTP(page, httptest.NewRequest("GET", "/routes/"+snapshot.ID, nil))
	if page.Code != 200 || !strings.Contains(page.Body.String(), `id="route-data"`) || page.Body.Len() < 200000 {
		t.Fatal("initial page missing full geometry")
	}
	// Renaming the geometry column makes any accidental geometry read fail in SQL.
	if _, err = pool.Exec(context.Background(), `ALTER TABLE route_snapshots RENAME COLUMN route TO hidden_geometry`); err != nil {
		t.Fatal(err)
	}
	initialCalls := calls.Load()
	for _, metric := range []string{"temperature", "precipitation", "wind"} {
		result := httptest.NewRecorder()
		handler.ServeHTTP(result, httptest.NewRequest("GET", "/routes/"+snapshot.ID+"/panel?hour=71&metric="+metric, nil))
		if result.Code != 200 || result.Body.Len() > 15000 {
			t.Fatalf("panel response: status=%d bytes=%d body=%s", result.Code, result.Body.Len(), result.Body.String())
		}
		start := strings.Index(result.Body.String(), `<div id="map-data" hidden>`)
		if start < 0 {
			t.Fatal("missing readings")
		}
		payload := strings.Split(strings.TrimPrefix(result.Body.String()[start:], `<div id="map-data" hidden>`), "</div>")[0]
		var data struct {
			Metric  string `json:"metric"`
			Samples []struct {
				Point domain.Point `json:"point"`
				Value float64      `json:"value"`
			} `json:"samples"`
		}
		if err = json.Unmarshal([]byte(html.UnescapeString(payload)), &data); err != nil {
			t.Fatal(err)
		}
		if data.Metric != metric || len(data.Samples) != len(snapshot.Forecasts) {
			t.Fatal("incorrect selection")
		}
		for i, sample := range data.Samples {
			forecast := snapshot.Forecasts[i]
			want := forecast.Temperature[71]
			if metric == "wind" {
				want = forecast.Wind[71]
			}
			if metric == "precipitation" {
				want = forecast.Precipitation[71]
			}
			if sample.Point != forecast.Point || sample.Value != want {
				t.Fatal("incorrect map reading")
			}
		}
		if strings.Contains(payload, "snapshot") || strings.Contains(payload, "forecasts") || strings.Contains(result.Body.String(), `id="route-data"`) {
			t.Fatal("panel resent complete data")
		}
	}
	if calls.Load() != initialCalls {
		t.Fatal("selection requested new weather")
	}
	if _, err = pool.Exec(context.Background(), `ALTER TABLE route_snapshots RENAME COLUMN hidden_geometry TO route`); err != nil {
		t.Fatal(err)
	}
	refreshed.Store(true)
	request := httptest.NewRequest("POST", "/routes/"+snapshot.ID+"/refresh", nil)
	request.Header.Set("HX-Request", "true")
	result := httptest.NewRecorder()
	handler.ServeHTTP(result, request)
	if result.Code != 200 || !strings.Contains(result.Body.String(), "22.5°C") || strings.Contains(result.Body.String(), `id="route-data"`) {
		t.Fatal("refresh did not publish updated readings")
	}
	if _, err = pool.Exec(context.Background(), `UPDATE route_snapshots SET forecasts='[]' WHERE id=$1`, snapshot.ID); err != nil {
		t.Fatal(err)
	}
	invalid := httptest.NewRecorder()
	handler.ServeHTTP(invalid, httptest.NewRequest("GET", "/routes/"+snapshot.ID+"/panel", nil))
	if invalid.Code != 500 {
		t.Fatal("accepted incomplete stored forecasts")
	}
}
