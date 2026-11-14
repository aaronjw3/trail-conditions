package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const twoSegments = `<gpx><trk><trkseg><trkpt lat="45" lon="-111"/><trkpt lat="45.1" lon="-111.1"/></trkseg><trkseg><trkpt lat="46" lon="-112"/><trkpt lat="46.1" lon="-112.1"/></trkseg></trk></gpx>`

func TestGPXPreservesSegmentsAndRejectsInvalidGeometry(t *testing.T) {
	route, points, err := parseRoute(strings.NewReader(twoSegments))
	if err != nil {
		t.Fatal(err)
	}
	if len(route.Coordinates) != 2 || len(points) != 4 || route.Coordinates[0][0] != (Point{-111, 45}) {
		t.Fatalf("geometry lost or reordered: %+v %v", route, points)
	}
	for _, input := range []string{
		strings.Replace(twoSegments, `lat="45"`, `lat="NaN"`, 1),
		strings.Replace(twoSegments, `lon="-111"`, `lon="181"`, 1),
		strings.Replace(twoSegments, `lat="45"`, ``, 1),
		`<gpx><trk><trkseg><trkpt lat="1" lon="1"/></trkseg></trk></gpx>`,
		`<gpx><wpt lat="1" lon="1"/></gpx>`,
		twoSegments + `<gpx/>`,
		strings.Replace(twoSegments, `lat="45.1" lon="-111.1"`, `lat="45" lon="-111"`, 1),
	} {
		if _, _, err := parseRoute(strings.NewReader(input)); err == nil {
			t.Fatalf("accepted invalid GPX: %s", input)
		}
	}
}

// Controlled HTTP responses exercise provider parsing without external API calls.
// They do not establish DNS/TLS behavior or live Open-Meteo compatibility.
type transportFunc func(*http.Request) (*http.Response, error)

func (f transportFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func response(code int, body string) *http.Response {
	return &http.Response{StatusCode: code, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}
}
func forecastJSON(t *testing.T, missing bool) string {
	t.Helper()
	times := make([]int64, 72)
	temp := make([]any, 72)
	rain := make([]float64, 72)
	wind := make([]float64, 72)
	for i := range times {
		times[i] = 1800000000 + int64(i)*3600
		temp[i] = 12.5
		rain[i] = 0.2
		wind[i] = 15
	}
	if missing {
		temp[10] = nil
	}
	body, err := json.Marshal(map[string]any{"hourly_units": map[string]string{"time": "unixtime", "temperature_2m": "°C", "precipitation": "mm", "wind_speed_10m": "km/h"}, "hourly": map[string]any{"time": times, "temperature_2m": temp, "precipitation": rain, "wind_speed_10m": wind}})
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

func TestForecastRejectsMissingValuesUnitsAndTimeGaps(t *testing.T) {
	valid := forecastJSON(t, false)
	for name, body := range map[string]string{"valid": valid, "null": forecastJSON(t, true), "units": strings.Replace(valid, "km/h", "mph", 1), "time gap": strings.Replace(valid, "1800003600", "1800003601", 1), "truncated": valid[:len(valid)-4]} {
		t.Run(name, func(t *testing.T) {
			client := &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
				if r.URL.Query().Get("timezone") != "GMT" || r.URL.Query().Get("forecast_days") != "3" {
					t.Error("forecast contract missing")
				}
				return response(200, body), nil
			})}
			result, err := fetchForecast(context.Background(), client, "https://example.test", Point{-111, 45})
			if name == "valid" {
				if err != nil || len(result.Times) != 72 {
					t.Fatalf("%+v %v", result, err)
				}
			} else if err == nil {
				t.Fatal("accepted invalid provider data")
			}
		})
	}
}

func TestFailedRefreshPreservesPublishedSnapshot(t *testing.T) {
	valid := forecastJSON(t, false)
	calls := 0
	fail := false
	a := &app{file: filepath.Join(t.TempDir(), "snapshot.json"), endpoint: "https://example.test", client: &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		if fail && calls%2 == 0 {
			return response(200, forecastJSON(t, true)), nil
		}
		return response(200, valid), nil
	})}}
	route, points, err := parseRoute(strings.NewReader(twoSegments))
	if err != nil {
		t.Fatal(err)
	}
	if err = a.collect(context.Background(), route, points); err != nil {
		t.Fatal(err)
	}
	old := a.snapshot
	before, err := os.ReadFile(a.file)
	if err != nil {
		t.Fatal(err)
	}
	fail = true
	if err = a.collect(context.Background(), route, points); err == nil {
		t.Fatal("published incomplete refresh")
	}
	after, err := os.ReadFile(a.file)
	if err != nil {
		t.Fatal(err)
	}
	if a.snapshot != old || !bytes.Equal(before, after) {
		t.Fatal("failed refresh replaced published state")
	}
	files, err := os.ReadDir(filepath.Dir(a.file))
	if err != nil || len(files) != 1 {
		t.Fatal("temporary files left behind", err)
	}
}

func TestUploadReturnsHTMXPanelAndRejectsCrossOriginWrites(t *testing.T) {
	valid := forecastJSON(t, false)
	a := &app{busy: make(chan struct{}, 1), file: filepath.Join(t.TempDir(), "snapshot.json"), endpoint: "https://example.test", client: &http.Client{Transport: transportFunc(func(*http.Request) (*http.Response, error) { return response(200, valid), nil })}}
	handler := a.routes()
	denied := httptest.NewRecorder()
	handler.ServeHTTP(denied, httptest.NewRequest("POST", "/example", nil))
	if denied.Code != 403 {
		t.Fatal("accepted unmarked write")
	}
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("route", "route.gpx")
	if err != nil {
		t.Fatal(err)
	}
	io.WriteString(part, twoSegments)
	writer.Close()
	req := httptest.NewRequest("POST", "/forecast", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("HX-Request", "true")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)
	if recorder.Code != 200 || !strings.Contains(recorder.Body.String(), `hx-get="/panel"`) || !strings.Contains(recorder.Body.String(), "SAMPLE 4") {
		t.Fatalf("bad HTML response: %d %s", recorder.Code, recorder.Body.String())
	}
	panel := httptest.NewRecorder()
	handler.ServeHTTP(panel, httptest.NewRequest("GET", "/panel?hour=71&metric=wind", nil))
	if panel.Code != 200 || !strings.Contains(panel.Body.String(), `value="wind" selected`) {
		t.Fatal("selection not rendered", panel.Body.String())
	}
	invalid := httptest.NewRecorder()
	handler.ServeHTTP(invalid, httptest.NewRequest("GET", "/panel?hour=72", nil))
	if invalid.Code != 400 {
		t.Fatal("accepted hour beyond forecast")
	}
	a.busy <- struct{}{}
	busy := httptest.NewRecorder()
	req = httptest.NewRequest("POST", "/refresh", nil)
	req.Header.Set("HX-Request", "true")
	handler.ServeHTTP(busy, req)
	if busy.Code != 409 {
		t.Fatal("allowed overlapping refresh")
	}
}

func TestRetriesAreBoundedAndCancellationStopsPublication(t *testing.T) {
	calls := 0
	client := &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		resp := response(503, "")
		resp.Header.Set("Retry-After", "0")
		return resp, nil
	})}
	if _, err := fetchForecast(context.Background(), client, "https://example.test", Point{0, 0}); err == nil || calls != 3 {
		t.Fatal("retry budget", calls, err)
	}
	calls = 0
	client.Transport = transportFunc(func(r *http.Request) (*http.Response, error) { calls++; return response(429, ""), nil })
	if _, err := fetchForecast(context.Background(), client, "https://example.test", Point{0, 0}); err == nil || calls != 1 {
		t.Fatal("rate limit retried", calls, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	valid := forecastJSON(t, false)
	a := &app{file: filepath.Join(t.TempDir(), "snapshot.json"), endpoint: "https://example.test", client: &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) { cancel(); return response(200, valid), nil })}}
	if err := a.collect(ctx, Route{}, []Point{{0, 0}}); err == nil || a.snapshot != nil {
		t.Fatal("published canceled request")
	}
	if _, err := os.Stat(a.file); !os.IsNotExist(err) {
		t.Fatal("canceled request created publication")
	}
}

func TestSamplingIsBoundedAndKeepsEachSegmentEndpoints(t *testing.T) {
	var xml strings.Builder
	xml.WriteString(`<gpx><trk>`)
	for segment := 0; segment < 3; segment++ {
		xml.WriteString(`<trkseg>`)
		for i := 0; i < 100; i++ {
			fmt.Fprintf(&xml, `<trkpt lat="%f" lon="%f"/>`, 45+float64(segment)+float64(i)/1000, -111.0)
		}
		xml.WriteString(`</trkseg>`)
	}
	xml.WriteString(`</trk></gpx>`)
	route, points, err := parseRoute(strings.NewReader(xml.String()))
	if err != nil {
		t.Fatal(err)
	}
	if len(points) != 12 {
		t.Fatal("sample budget", len(points))
	}
	for i, segment := range route.Coordinates {
		if points[i*4] != segment[0] || points[i*4+3] != segment[len(segment)-1] {
			t.Fatal("lost segment endpoint")
		}
	}
}

func TestPersistenceFailureDoesNotPublish(t *testing.T) {
	valid := forecastJSON(t, false)
	old := &Snapshot{}
	a := &app{snapshot: old, file: filepath.Join(t.TempDir(), "missing", "snapshot.json"), endpoint: "https://example.test", client: &http.Client{Transport: transportFunc(func(*http.Request) (*http.Response, error) { return response(200, valid), nil })}}
	if err := a.collect(context.Background(), Route{}, []Point{{0, 0}}); err == nil || a.snapshot != old {
		t.Fatal("published before persistence")
	}
}
