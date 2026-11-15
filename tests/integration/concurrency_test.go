//go:build integration

package integration

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aaronjw3/trail-conditions/internal/adapters/geoimport"
	"github.com/aaronjw3/trail-conditions/internal/adapters/openmeteo"
	"github.com/aaronjw3/trail-conditions/internal/adapters/postgres"
	"github.com/aaronjw3/trail-conditions/internal/application"
	"github.com/aaronjw3/trail-conditions/internal/domain"
)

func TestConcurrentUploadsShareWeatherLimitAndSaveSeparateRoutes(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	valid := forecastJSON(t, false)
	entered := make(chan struct{}, 16)
	release := make(chan struct{})
	var active, peak, calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		current := active.Add(1)
		defer active.Add(-1)
		for old := peak.Load(); current > old; old = peak.Load() {
			if peak.CompareAndSwap(old, current) {
				break
			}
		}
		calls.Add(1)
		entered <- struct{}{}
		select {
		case <-release:
		case <-r.Context().Done():
			return
		}
		w.Write([]byte(valid))
	}))
	defer server.Close()
	// Cancel outstanding HTTP calls before closing the test server on failure.
	defer cancel()
	pool := testDatabase(t)
	store, err := postgres.New(ctx, pool)
	if err != nil {
		t.Fatal(err)
	}
	service, err := application.New(geoimport.Decoder{}, openmeteo.Client{HTTP: server.Client(), Endpoint: server.URL}, store)
	if err != nil {
		t.Fatal(err)
	}
	type result struct {
		snapshot *domain.Snapshot
		err      error
	}
	done := make(chan result, 2)
	for _, name := range []string{"ridge.gpx", "valley.gpx"} {
		go func() {
			snapshot, err := service.Import(ctx, name, []byte(twoSegments))
			done <- result{snapshot, err}
		}()
	}
	// Sequential collection cannot reach this barrier before responses are released.
	for range 3 {
		select {
		case <-entered:
		case <-ctx.Done():
			t.Fatal("weather requests did not overlap", ctx.Err())
		}
	}
	close(release)
	ids := map[string]bool{}
	for range 2 {
		select {
		case result := <-done:
			if result.err != nil {
				t.Fatal(result.err)
			}
			if ids[result.snapshot.ID] {
				t.Fatal("uploads shared a route ID")
			}
			ids[result.snapshot.ID] = true
			if err := result.snapshot.Validate(); err != nil {
				t.Fatal("forecast order changed", err)
			}
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
	if peak.Load() != 3 || calls.Load() != 8 {
		t.Fatalf("peak requests=%d total=%d", peak.Load(), calls.Load())
	}
	routes, err := store.List(ctx)
	if err != nil || len(routes) != 2 {
		t.Fatalf("routes=%v err=%v", routes, err)
	}
	for _, route := range routes {
		if !ids[route.ID] {
			t.Fatal("wrong saved route", route)
		}
	}
}

func TestForecastFailureCancelsSiblingRequests(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var calls atomic.Int32
	anotherStarted := make(chan struct{})
	canceled := make(chan struct{}, 2)
	var startedOnce sync.Once
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			select {
			case <-anotherStarted:
			case <-r.Context().Done():
				return
			}
			w.Write([]byte(`{"hourly":{}}`))
			return
		}
		startedOnce.Do(func() { close(anotherStarted) })
		<-r.Context().Done()
		canceled <- struct{}{}
	}))
	defer server.Close()
	defer cancel()
	store, err := postgres.New(ctx, testDatabase(t))
	if err != nil {
		t.Fatal(err)
	}
	service, err := application.New(geoimport.Decoder{}, openmeteo.Client{HTTP: server.Client(), Endpoint: server.URL}, store)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Import(ctx, "route.gpx", []byte(twoSegments)); err == nil || !strings.Contains(err.Error(), "incomplete forecast") {
		t.Fatal("lost provider failure", err)
	}
	select {
	case <-canceled:
	case <-ctx.Done():
		t.Fatal("sibling request was not canceled")
	}
	routes, err := store.List(ctx)
	if err != nil || len(routes) != 0 {
		t.Fatal("saved an incomplete upload", err)
	}
}
