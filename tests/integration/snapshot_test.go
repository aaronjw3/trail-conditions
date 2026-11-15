//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/aaronjw3/trail-conditions/internal/adapters/geoimport"
	"github.com/aaronjw3/trail-conditions/internal/adapters/openmeteo"
	"github.com/aaronjw3/trail-conditions/internal/adapters/postgres"
	"github.com/aaronjw3/trail-conditions/internal/application"
	"github.com/aaronjw3/trail-conditions/internal/domain"
)

func TestFailedRefreshPreservesPublishedSnapshotAndReloads(t *testing.T) {
	ctx := context.Background()
	valid := forecastJSON(t, false)
	fail := false
	service, store, pool := newService(t, func(*http.Request) (*http.Response, error) {
		if fail {
			return response(200, forecastJSON(t, true)), nil
		}
		return response(200, valid), nil
	})
	old, err := service.Import(ctx, "route.gpx", []byte(twoSegments))
	if err != nil {
		t.Fatal(err)
	}
	fail = true
	if _, err := service.Refresh(ctx, old.ID); err == nil {
		t.Fatal("published partial result")
	}
	stored, err := store.Load(ctx, old.ID)
	if err != nil || !reflect.DeepEqual(old, stored) {
		t.Fatalf("failed refresh changed snapshot: %v", err)
	}
	reopened, err := postgres.New(ctx, pool)
	if err != nil {
		t.Fatal(err)
	}
	reloaded, err := application.New(geoimport.Decoder{}, openmeteo.Client{}, reopened)
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := reloaded.Snapshot(ctx, old.ID)
	if err != nil || !reflect.DeepEqual(old, loaded) {
		t.Fatalf("restart lost snapshot: %v", err)
	}
	loaded.Forecasts[0].Temperature[0] = 999
	loaded.Route.Coordinates[0][0][0] = 0
	loaded, err = reloaded.Snapshot(ctx, old.ID)
	if err != nil || !reflect.DeepEqual(old, loaded) {
		t.Fatalf("caller mutated stored snapshot: %v", err)
	}
}

func TestCancellationPreventsPublication(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	valid := forecastJSON(t, false)
	service, store, _ := newService(t, func(*http.Request) (*http.Response, error) { cancel(); return response(200, valid), nil })
	if _, err := service.Import(ctx, "route.gpx", []byte(twoSegments)); !errors.Is(err, context.Canceled) {
		t.Fatal("lost cancellation", err)
	}
	if routes, err := store.List(context.Background()); err != nil || len(routes) != 0 {
		t.Fatal("persisted canceled request", err)
	}
}

func TestPersistenceFailureAndConcurrentRefresh(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	valid := forecastJSON(t, false)
	entered := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	block := false
	service, store, pool := newService(t, func(r *http.Request) (*http.Response, error) {
		if block {
			once.Do(func() {
				close(entered)
				select {
				case <-release:
				case <-r.Context().Done():
				}
			})
		}
		return response(200, valid), nil
	})
	old, err := service.Import(ctx, "route.gpx", []byte(twoSegments))
	if err != nil {
		t.Fatal(err)
	}
	block = true
	done := make(chan error, 1)
	go func() { _, err := service.Refresh(ctx, old.ID); done <- err }()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if _, err := service.Refresh(ctx, old.ID); !errors.Is(err, application.ErrBusy) {
		t.Fatal("concurrent refresh was accepted", err)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	old, err = service.Snapshot(ctx, old.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `ALTER TABLE route_snapshots ADD CONSTRAINT reject_refresh CHECK (false) NOT VALID`); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Refresh(ctx, old.ID); err == nil {
		t.Fatal("ignored database failure")
	}
	stored, err := store.Load(ctx, old.ID)
	if err != nil || !reflect.DeepEqual(old, stored) {
		t.Fatal("failed statement replaced snapshot", err)
	}
}

func TestStoredSnapshotValidation(t *testing.T) {
	ctx := context.Background()
	pool := testDatabase(t)
	store, err := postgres.New(ctx, pool)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `INSERT INTO route_snapshots (id,name,retrieved_at,route,forecasts) VALUES ('broken','route',now(),'{"type":"MultiLineString","coordinates":[]}','[]')`); err != nil {
		t.Fatal(err)
	}
	if _, err = store.Load(ctx, "broken"); err == nil {
		t.Fatal("accepted broken stored snapshot")
	}
}

func TestExistingSnapshotMigratesOnce(t *testing.T) {
	ctx := context.Background()
	pool := testDatabase(t)
	route, err := (geoimport.Decoder{}).Decode("route.gpx", []byte(twoSegments))
	if err != nil {
		t.Fatal(err)
	}
	provider := openmeteo.Client{Endpoint: "https://example.test", HTTP: &http.Client{Transport: transportFunc(func(*http.Request) (*http.Response, error) { return response(200, forecastJSON(t, false)), nil })}}
	points, err := route.Samples()
	if err != nil {
		t.Fatal(err)
	}
	forecasts := []domain.Forecast{}
	for _, point := range points {
		f, err := provider.Forecast(ctx, point)
		if err != nil {
			t.Fatal(err)
		}
		forecasts = append(forecasts, f)
	}
	routeJSON, _ := json.Marshal(route)
	forecastsJSON, _ := json.Marshal(forecasts)
	if _, err := pool.Exec(ctx, `CREATE TABLE current_snapshot (singleton boolean PRIMARY KEY, retrieved_at timestamptz,route jsonb,forecasts jsonb)`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO current_snapshot VALUES(true,now(),$1,$2)`, routeJSON, forecastsJSON); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		store, err := postgres.New(ctx, pool)
		if err != nil {
			t.Fatal(err)
		}
		loaded, err := store.Load(ctx, "saved-route")
		if err != nil || loaded == nil || loaded.Name != "Saved route" || !reflect.DeepEqual(route, loaded.Route) || !reflect.DeepEqual(forecasts, loaded.Forecasts) {
			t.Fatalf("migration lost snapshot: %v", err)
		}
		routes, err := store.List(ctx)
		if err != nil || len(routes) != 1 {
			t.Fatalf("migration duplicated snapshot: %v", err)
		}
	}
}
