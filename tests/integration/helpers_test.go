//go:build integration

package integration

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/aaronjw3/trail-conditions/internal/adapters/geoimport"
	"github.com/aaronjw3/trail-conditions/internal/adapters/openmeteo"
	"github.com/aaronjw3/trail-conditions/internal/adapters/postgres"
	"github.com/aaronjw3/trail-conditions/internal/application"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const twoSegments = `<gpx><trk><trkseg><trkpt lat="45" lon="-111"/><trkpt lat="45.1" lon="-111.1"/></trkseg><trkseg><trkpt lat="46" lon="-112"/><trkpt lat="46.1" lon="-112.1"/></trkseg></trk></gpx>`

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

func testDatabase(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Fatal("TEST_DATABASE_URL is required for integration tests")
	}
	ctx := context.Background()
	admin, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	schema := "trail_test_" + strings.ToLower(rand.Text())
	name := pgx.Identifier{schema}.Sanitize()
	if _, err = admin.Exec(ctx, "CREATE SCHEMA "+name); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := admin.Exec(context.Background(), "DROP SCHEMA "+name+" CASCADE"); err != nil {
			t.Error(err)
		}
		admin.Close()
	})
	config, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	config.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return pool
}
func newService(t *testing.T, transport transportFunc) (*application.Service, *postgres.Store, *pgxpool.Pool) {
	t.Helper()
	pool := testDatabase(t)
	store, err := postgres.New(context.Background(), pool)
	if err != nil {
		t.Fatal(err)
	}
	service, err := application.New(context.Background(), geoimport.Decoder{}, openmeteo.Client{HTTP: &http.Client{Transport: transport}, Endpoint: "https://example.test"}, store)
	if err != nil {
		t.Fatal(err)
	}
	return service, store, pool
}
