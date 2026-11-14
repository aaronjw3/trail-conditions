//go:build integration

package integration

import (
	"context"
	"errors"
	"net/http"
	"reflect"
	"testing"

	"github.com/aaronjw3/trail-conditions/internal/adapters/geoimport"
	"github.com/aaronjw3/trail-conditions/internal/adapters/openmeteo"
	"github.com/aaronjw3/trail-conditions/internal/adapters/postgres"
	"github.com/aaronjw3/trail-conditions/internal/application"
)

func TestFailedRefreshPreservesPublishedSnapshotAndReloads(t *testing.T) {
	valid := forecastJSON(t, false)
	calls := 0
	fail := false
	transport := transportFunc(func(*http.Request) (*http.Response, error) {
		calls++
		if fail && calls%2 == 0 {
			return response(200, forecastJSON(t, true)), nil
		}
		return response(200, valid), nil
	})
	service, store, pool := newService(t, transport)
	if err := service.Import(context.Background(), "route.gpx", []byte(twoSegments)); err != nil {
		t.Fatal(err)
	}
	old := service.Snapshot()
	fail = true
	if err := service.Refresh(context.Background()); err == nil {
		t.Fatal("published partial result")
	}
	stored, err := store.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(old, service.Snapshot()) || !reflect.DeepEqual(old, stored) {
		t.Fatal("failed refresh changed snapshot")
	}
	// Reopen the adapter and application to exercise loading an existing table.
	reopened, err := postgres.New(context.Background(), pool)
	if err != nil {
		t.Fatal(err)
	}
	reloaded, err := application.New(context.Background(), geoimport.Decoder{}, openmeteo.Client{}, reopened)
	if err != nil {
		t.Fatal(err)
	}
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
func TestCancellationPreventsPublication(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	valid := forecastJSON(t, false)
	service, store, _ := newService(t, func(*http.Request) (*http.Response, error) { cancel(); return response(200, valid), nil })
	if err := service.Import(ctx, "route.gpx", []byte(twoSegments)); !errors.Is(err, context.Canceled) {
		t.Fatal("lost cancellation", err)
	}
	if service.Snapshot() != nil {
		t.Fatal("published canceled request")
	}
	if stored, err := store.Load(context.Background()); err != nil || stored != nil {
		t.Fatal("persisted canceled request", err)
	}
}
func TestPersistenceFailureAndConcurrentRefresh(t *testing.T) {
	valid := forecastJSON(t, false)
	entered := make(chan struct{})
	release := make(chan struct{})
	first := true
	service, store, pool := newService(t, func(*http.Request) (*http.Response, error) {
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
	// A real database constraint rejects the replacement while preserving the row.
	if _, err := pool.Exec(context.Background(), `ALTER TABLE current_snapshot ADD CONSTRAINT reject_refresh CHECK (false) NOT VALID`); err != nil {
		t.Fatal(err)
	}
	if err := service.Refresh(context.Background()); err == nil {
		t.Fatal("ignored database failure")
	}
	stored, err := store.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(old, service.Snapshot()) || !reflect.DeepEqual(old, stored) {
		t.Fatal("failed statement replaced publication")
	}
}
func TestStoredSnapshotValidation(t *testing.T) {
	pool := testDatabase(t)
	store, err := postgres.New(context.Background(), pool)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(context.Background(), `INSERT INTO current_snapshot (retrieved_at,route,forecasts) VALUES (now(),'{"type":"MultiLineString","coordinates":[]}','[]')`); err != nil {
		t.Fatal(err)
	}
	if _, err = store.Load(context.Background()); err == nil {
		t.Fatal("accepted broken stored snapshot")
	}
}
