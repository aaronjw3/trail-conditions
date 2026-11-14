package main

import (
	"context"
	"crypto/x509"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/aaronjw3/trail-conditions/internal/adapters/httpui"
	"github.com/aaronjw3/trail-conditions/internal/adapters/openmeteo"
	"github.com/aaronjw3/trail-conditions/internal/domain"
)

func TestForecastConnectionRetryBudget(t *testing.T) {
	for _, tc := range []struct {
		name      string
		failures  int
		failure   error
		wantCalls int
		wantWait  time.Duration
	}{
		{"timeout then recovery", 1, os.ErrDeadlineExceeded, 2, time.Second},
		{"persistent timeout", 3, os.ErrDeadlineExceeded, 3, 3 * time.Second},
		{"certificate error", 1, x509.UnknownAuthorityError{}, 1, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				calls := 0
				provider := openmeteo.Client{Endpoint: "https://example.test", HTTP: &http.Client{Transport: transportFunc(func(*http.Request) (*http.Response, error) {
					calls++
					if calls <= tc.failures {
						return nil, tc.failure
					}
					return response(200, forecastJSON(t, false)), nil
				})}}
				start := time.Now()
				result, err := provider.Forecast(context.Background(), domain.Point{-111, 45})
				if calls != tc.wantCalls || time.Since(start) != tc.wantWait {
					t.Fatalf("calls=%d elapsed=%s", calls, time.Since(start))
				}
				if tc.wantCalls > tc.failures {
					if err != nil || len(result.Times) != 72 {
						t.Fatalf("recovery failed: %v", err)
					}
				} else if !errors.Is(err, tc.failure) {
					t.Fatalf("lost original error: %v", err)
				}
			})
		})
	}
}

func TestForecastCancellationDuringConnectionRetry(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		calls := 0
		provider := openmeteo.Client{Endpoint: "https://example.test", HTTP: &http.Client{Transport: transportFunc(func(*http.Request) (*http.Response, error) {
			calls++
			return nil, os.ErrDeadlineExceeded
		})}}
		go func() { time.Sleep(10 * time.Millisecond); cancel() }()
		_, err := provider.Forecast(ctx, domain.Point{-111, 45})
		if !errors.Is(err, context.Canceled) || calls != 1 {
			t.Fatalf("calls=%d err=%v", calls, err)
		}
	})
}

func TestForecastRetriesClosedTLSConnection(t *testing.T) {
	valid := forecastJSON(t, false)
	calls := 0
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			conn, _, err := w.(http.Hijacker).Hijack()
			if err != nil {
				t.Error(err)
				return
			}
			conn.Close()
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(valid))
	}))
	defer server.Close()
	provider := openmeteo.Client{Endpoint: server.URL, HTTP: server.Client()}
	result, err := provider.Forecast(context.Background(), domain.Point{-111, 45})
	if err != nil || len(result.Times) != 72 || calls != 2 {
		t.Fatalf("calls=%d err=%v", calls, err)
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
	ui, err := httpui.New(service, assets, "")
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/example", nil)
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
