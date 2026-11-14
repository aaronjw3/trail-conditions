package openmeteo_test

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/aaronjw3/trail-conditions/internal/adapters/openmeteo"
	"github.com/aaronjw3/trail-conditions/internal/domain"
)

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

// Every database test owns a disposable schema in the explicitly supplied test DB.
func TestProviderRetriesAreBounded(t *testing.T) {
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
}
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
