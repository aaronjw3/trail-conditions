//go:build integration

package integration

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
	"reflect"
	"strings"
	"testing"

	"github.com/aaronjw3/trail-conditions/internal/adapters/httpui"
	"github.com/aaronjw3/trail-conditions/internal/domain"
)

func TestHTTPUploadsRenderHTMLForAllFormats(t *testing.T) {
	valid := forecastJSON(t, false)
	service, _, _ := newService(t, func(*http.Request) (*http.Response, error) { return response(200, valid), nil })
	ui, err := httpui.New(service, os.DirFS("../.."), "")
	if err != nil {
		t.Fatal(err)
	}
	handler := ui.Routes()
	denied := httptest.NewRecorder()
	handler.ServeHTTP(denied, httptest.NewRequest("POST", "/forecast", nil))
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
	service, store, pool := newService(t, func(req *http.Request) (*http.Response, error) {
		requested = append(requested, req.URL.Query().Get("longitude")+","+req.URL.Query().Get("latitude"))
		return response(200, valid), nil
	})
	ui, err := httpui.New(service, os.DirFS("../.."), "")
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
	stored, err := store.Load(context.Background())
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
	var count int
	if err := pool.QueryRow(context.Background(), "SELECT count(*) FROM current_snapshot").Scan(&count); err != nil || count != 1 {
		t.Fatal("expected one complete snapshot row", count, err)
	}
}
func TestWeatherFailureExplainsUploadAndPreservesSnapshot(t *testing.T) {
	fail := false
	service, store, _ := newService(t, func(*http.Request) (*http.Response, error) {
		if fail {
			return nil, errors.New("private diagnostic detail")
		}
		return response(200, forecastJSON(t, false)), nil
	})
	if err := service.Import(context.Background(), "route.gpx", []byte(twoSegments)); err != nil {
		t.Fatal(err)
	}
	before := service.Snapshot()
	fail = true
	ui, err := httpui.New(service, os.DirFS("../.."), "")
	if err != nil {
		t.Fatal(err)
	}
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("route", "route.gpx")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = part.Write([]byte(twoSegments)); err != nil {
		t.Fatal(err)
	}
	if err = writer.Close(); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/forecast", &body)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	request.Header.Set("HX-Request", "true")
	recorder := httptest.NewRecorder()
	ui.Routes().ServeHTTP(recorder, request)
	if recorder.Code != 502 || !strings.Contains(recorder.Body.String(), "Open-Meteo") || strings.Contains(recorder.Body.String(), "private diagnostic detail") {
		t.Fatalf("unexpected response: %d %s", recorder.Code, recorder.Body.String())
	}
	stored, err := store.Load(context.Background())
	if err != nil || !reflect.DeepEqual(before, stored) || !reflect.DeepEqual(before, service.Snapshot()) {
		t.Fatalf("failed weather request changed snapshot: %v", err)
	}
}
