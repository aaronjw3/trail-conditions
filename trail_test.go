package main

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"html"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/aaronjw3/trail-conditions/internal/adapters/geoimport"
	"github.com/aaronjw3/trail-conditions/internal/adapters/httpui"
	"github.com/aaronjw3/trail-conditions/internal/adapters/openmeteo"
	"github.com/aaronjw3/trail-conditions/internal/adapters/snapshotfile"
	"github.com/aaronjw3/trail-conditions/internal/application"
	"github.com/aaronjw3/trail-conditions/internal/domain"
)

const twoSegments = `<gpx><trk><trkseg><trkpt lat="45" lon="-111"/><trkpt lat="45.1" lon="-111.1"/></trkseg><trkseg><trkpt lat="46" lon="-112"/><trkpt lat="46.1" lon="-112.1"/></trkseg></trk></gpx>`

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
			result, err := (openmeteo.Client{HTTP: client, Endpoint: "https://example.test"}).Forecast(context.Background(), domain.Point{-111, 45})
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

func newService(t *testing.T, path string, transport transportFunc) *application.Service {
	t.Helper()
	service, err := application.New(context.Background(), geoimport.Decoder{}, openmeteo.Client{HTTP: &http.Client{Transport: transport}, Endpoint: "https://example.test"}, snapshotfile.Store{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	return service
}
func TestFailedRefreshPreservesPublishedSnapshotAndReloads(t *testing.T) {
	valid := forecastJSON(t, false)
	calls := 0
	fail := false
	path := filepath.Join(t.TempDir(), "snapshot.json")
	transport := transportFunc(func(*http.Request) (*http.Response, error) {
		calls++
		if fail && calls%2 == 0 {
			return response(200, forecastJSON(t, true)), nil
		}
		return response(200, valid), nil
	})
	service := newService(t, path, transport)
	if err := service.Import(context.Background(), "route.gpx", []byte(twoSegments)); err != nil {
		t.Fatal(err)
	}
	old := service.Snapshot()
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	fail = true
	if err := service.Refresh(context.Background()); err == nil {
		t.Fatal("published partial result")
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(old, service.Snapshot()) || !bytes.Equal(before, after) {
		t.Fatal("failed refresh changed snapshot")
	}
	reloaded := newService(t, path, transport)
	if !reflect.DeepEqual(old, reloaded.Snapshot()) {
		t.Fatal("restart lost snapshot")
	}
	copy := service.Snapshot()
	copy.Forecasts[0].Temperature[0] = 999
	copy.Route.Coordinates[0][0][0] = 0
	if !reflect.DeepEqual(old, service.Snapshot()) {
		t.Fatal("caller can mutate publication")
	}
}
func TestHTTPUploadsRenderHTMLForAllFormats(t *testing.T) {
	valid := forecastJSON(t, false)
	service := newService(t, filepath.Join(t.TempDir(), "snapshot.json"), func(*http.Request) (*http.Response, error) { return response(200, valid), nil })
	ui, err := httpui.New(service, assets, "")
	if err != nil {
		t.Fatal(err)
	}
	handler := ui.Routes()
	denied := httptest.NewRecorder()
	handler.ServeHTTP(denied, httptest.NewRequest("POST", "/example", nil))
	if denied.Code != 403 {
		t.Fatal("accepted unmarked write")
	}

	kml := `<kml><Document><Placemark><MultiGeometry><LineString><coordinates>-111,45 -111.1,45.1</coordinates></LineString><LineString><coordinates>-112,46 -112.1,46.1</coordinates></LineString></MultiGeometry></Placemark></Document></kml>`
	var archive bytes.Buffer
	zw := zip.NewWriter(&archive)
	member, err := zw.Create("doc.kml")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = io.WriteString(member, kml); err != nil {
		t.Fatal(err)
	}
	if err = zw.Close(); err != nil {
		t.Fatal(err)
	}
	for filename, input := range map[string][]byte{"route.gpx": []byte(twoSegments), "route.kml": []byte(kml), "route.kmz": archive.Bytes()} {

		var body bytes.Buffer
		writer := multipart.NewWriter(&body)
		part, err := writer.CreateFormFile("route", filename)
		if err != nil {
			t.Fatal(err)
		}
		part.Write(input)
		writer.Close()
		req := httptest.NewRequest("POST", "/forecast", &body)
		req.Header.Set("Content-Type", writer.FormDataContentType())
		req.Header.Set("HX-Request", "true")
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, req)
		if recorder.Code != 200 || !strings.Contains(recorder.Body.String(), `hx-get="/panel"`) || !strings.Contains(recorder.Body.String(), "SAMPLE 4") {
			t.Fatalf("%s: %d %s", filename, recorder.Code, recorder.Body.String())
		}
	}
	panel := httptest.NewRecorder()
	handler.ServeHTTP(panel, httptest.NewRequest("GET", "/panel?hour=71&metric=wind", nil))
	if panel.Code != 200 || !strings.Contains(panel.Body.String(), `value="wind" selected`) {
		t.Fatal("selection not rendered")
	}
	invalid := httptest.NewRecorder()
	handler.ServeHTTP(invalid, httptest.NewRequest("GET", "/panel?hour=72", nil))
	if invalid.Code != 400 {
		t.Fatal("accepted invalid hour")
	}
}
func TestRetriesBoundedAndCancellationPreventsPublication(t *testing.T) {
	calls := 0
	client := &http.Client{Transport: transportFunc(func(*http.Request) (*http.Response, error) {
		calls++
		resp := response(503, "")
		resp.Header.Set("Retry-After", "0")
		return resp, nil
	})}
	provider := openmeteo.Client{HTTP: client, Endpoint: "https://example.test"}
	if _, err := provider.Forecast(context.Background(), domain.Point{0, 0}); err == nil || calls != 3 {
		t.Fatal("retry budget", calls, err)
	}
	calls = 0
	client.Transport = transportFunc(func(*http.Request) (*http.Response, error) { calls++; return response(429, ""), nil })
	if _, err := provider.Forecast(context.Background(), domain.Point{0, 0}); err == nil || calls != 1 {
		t.Fatal("retried rate limit")
	}
	ctx, cancel := context.WithCancel(context.Background())
	valid := forecastJSON(t, false)
	path := filepath.Join(t.TempDir(), "snapshot.json")
	service := newService(t, path, func(*http.Request) (*http.Response, error) { cancel(); return response(200, valid), nil })
	if err := service.Import(ctx, "route.gpx", []byte(twoSegments)); !errors.Is(err, context.Canceled) {
		t.Fatal("lost cancellation", err)
	}
	if service.Snapshot() != nil {
		t.Fatal("published canceled request")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("persisted canceled request")
	}
}
func TestPersistenceFailureAndConcurrentRefresh(t *testing.T) {
	valid := forecastJSON(t, false)
	dir := t.TempDir()
	path := filepath.Join(dir, "snapshot.json")
	entered := make(chan struct{})
	release := make(chan struct{})
	first := true
	service := newService(t, path, func(*http.Request) (*http.Response, error) {
		if first {
			first = false
			close(entered)
			<-release
		}
		return response(200, valid), nil
	})
	done := make(chan error, 1)
	go func() { done <- service.Import(context.Background(), "route.gpx", []byte(twoSegments)) }()
	<-entered
	if err := service.Refresh(context.Background()); !errors.Is(err, application.ErrBusy) {
		t.Fatal("concurrent refresh was accepted", err)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	old := service.Snapshot()
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	// A directory at the destination makes atomic replacement fail after writing.
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	if err := service.Refresh(context.Background()); err == nil {
		t.Fatal("ignored storage failure")
	}
	if !reflect.DeepEqual(old, service.Snapshot()) {
		t.Fatal("published before persistence")
	}
	files, err := os.ReadDir(dir)
	if err != nil || len(files) != 1 {
		t.Fatal("temporary file left behind")
	}
}
func TestStoredSnapshotValidation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "snapshot.json")
	if err := os.WriteFile(path, []byte(`{"route":{"type":"MultiLineString","coordinates":[]},"forecasts":[]}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := (snapshotfile.Store{Path: path}).Load(context.Background()); err == nil {
		t.Fatal("accepted broken snapshot")
	}
}
func TestMapConfigExposesOnlyPublicTokens(t *testing.T) {
	for _, token := range []string{"", "sk.test-secret", "pk.test-public"} {
		ui, err := httpui.New(nil, assets, token)
		if err != nil {
			t.Fatal(err)
		}
		recorder := httptest.NewRecorder()
		ui.Routes().ServeHTTP(recorder, httptest.NewRequest("GET", "/api/map-config", nil))
		if token == "pk.test-public" {
			if recorder.Code != 200 || !strings.Contains(recorder.Body.String(), token) {
				t.Fatal("public token unavailable")
			}
		} else if recorder.Code != 503 || strings.Contains(recorder.Body.String(), "sk.test-secret") {
			t.Fatal("invalid token exposed")
		}
		if recorder.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("config cached")
		}
	}
}

func TestKMZUploadPublishesGeometryAndMapPayload(t *testing.T) {
	var archive bytes.Buffer
	writer := zip.NewWriter(&archive)
	for _, member := range []struct{ name, content string }{
		{"nested/doc.kml", `<kml xmlns="http://www.opengis.net/kml/2.2"><Placemark><LineString><coordinates>-121.735,46.785,1600 -121.730,46.790,1700</coordinates></LineString></Placemark></kml>`},
		{"second.kml", `<kml><Placemark><LineString><coordinates>-121.725,46.800 -121.720,46.805</coordinates></LineString></Placemark></kml>`},
		{"images/icon.png", "unused asset"},
	} {
		entry, err := writer.Create(member.name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = io.WriteString(entry, member.content); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	valid := forecastJSON(t, false)
	var requested []string
	path := filepath.Join(t.TempDir(), "snapshot.json")
	service := newService(t, path, func(req *http.Request) (*http.Response, error) {
		requested = append(requested, req.URL.Query().Get("longitude")+","+req.URL.Query().Get("latitude"))
		return response(200, valid), nil
	})
	ui, err := httpui.New(service, assets, "")
	if err != nil {
		t.Fatal(err)
	}
	handler := ui.Routes()
	var body bytes.Buffer
	multipartWriter := multipart.NewWriter(&body)
	part, err := multipartWriter.CreateFormFile("route", "test-route.KMZ")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = part.Write(archive.Bytes()); err != nil {
		t.Fatal(err)
	}
	if err = multipartWriter.Close(); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("POST", "/forecast", &body)
	req.Header.Set("Content-Type", multipartWriter.FormDataContentType())
	req.Header.Set("HX-Request", "true")
	result := httptest.NewRecorder()
	handler.ServeHTTP(result, req)
	if result.Code != 200 {
		t.Fatalf("upload: %d %s", result.Code, result.Body.String())
	}
	want := [][]domain.Point{{{-121.735, 46.785}, {-121.730, 46.790}}, {{-121.725, 46.800}, {-121.720, 46.805}}}
	snapshot := service.Snapshot()
	if snapshot == nil || !reflect.DeepEqual(snapshot.Route.Coordinates, want) {
		t.Fatal("KMZ segments changed during upload")
	}
	if len(requested) != 4 || requested[0] != "-121.735000,46.785000" || requested[3] != "-121.720000,46.805000" {
		t.Fatal("wrong forecast locations", requested)
	}
	stored, err := (snapshotfile.Store{Path: path}).Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(snapshot, stored) {
		t.Fatal("published state differs from stored state")
	}
	// The browser reads this element's textContent after HTMX swaps the panel.
	start := strings.Index(result.Body.String(), `<div id="map-data" hidden>`)
	if start < 0 {
		t.Fatal("missing map payload")
	}
	payload := strings.SplitN(result.Body.String()[start+len(`<div id="map-data" hidden>`):], "</div>", 2)[0]
	var view struct {
		Snapshot *domain.Snapshot `json:"snapshot"`
		Hour     int              `json:"hour"`
		Metric   string           `json:"metric"`
	}
	if err = json.Unmarshal([]byte(html.UnescapeString(payload)), &view); err != nil {
		t.Fatal("invalid embedded map data", err)
	}
	if !reflect.DeepEqual(view.Snapshot, stored) || view.Hour < 0 || view.Hour >= 72 || view.Metric != "temperature" {
		t.Fatal("map received a different snapshot")
	}
	files, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || files[0].Name() != "snapshot.json" {
		t.Fatal("archive members were extracted or temporary files retained")
	}
}
