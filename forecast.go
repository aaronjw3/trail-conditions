package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

type Forecast struct {
	Point         Point     `json:"point"`
	Times         []int64   `json:"times"`
	Temperature   []float64 `json:"temperature"`
	Precipitation []float64 `json:"precipitation"`
	Wind          []float64 `json:"wind"`
}

func fetchForecast(ctx context.Context, client *http.Client, endpoint string, point Point) (Forecast, error) {
	q := url.Values{"latitude": {strconv.FormatFloat(point[1], 'f', 6, 64)}, "longitude": {strconv.FormatFloat(point[0], 'f', 6, 64)}, "hourly": {"temperature_2m,precipitation,wind_speed_10m"}, "forecast_days": {"3"}, "timezone": {"GMT"}, "timeformat": {"unixtime"}, "temperature_unit": {"celsius"}, "wind_speed_unit": {"kmh"}, "precipitation_unit": {"mm"}}
	var raw struct {
		Units struct {
			Time          string `json:"time"`
			Temperature   string `json:"temperature_2m"`
			Precipitation string `json:"precipitation"`
			Wind          string `json:"wind_speed_10m"`
		} `json:"hourly_units"`
		Hourly struct {
			Times         []int64    `json:"time"`
			Temperature   []*float64 `json:"temperature_2m"`
			Precipitation []*float64 `json:"precipitation"`
			Wind          []*float64 `json:"wind_speed_10m"`
		} `json:"hourly"`
	}
	for attempt := 0; attempt < 3; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint+"?"+q.Encode(), nil)
		if err != nil {
			return Forecast{}, err
		}
		req.Header.Set("User-Agent", "trail-conditions/1.0")
		resp, err := client.Do(req)
		if err != nil {
			return Forecast{}, fmt.Errorf("forecast connection failed: %w", err)
		}
		body, readErr := io.ReadAll(io.LimitReader(resp.Body, (1<<20)+1))
		resp.Body.Close()
		if readErr != nil || len(body) > 1<<20 {
			return Forecast{}, fmt.Errorf("forecast response interrupted or too large")
		}
		if resp.StatusCode == 200 {
			if json.Unmarshal(body, &raw) != nil {
				return Forecast{}, fmt.Errorf("invalid forecast JSON")
			}
			break
		}
		if attempt == 2 || (resp.StatusCode != 502 && resp.StatusCode != 503 && resp.StatusCode != 504) {
			return Forecast{}, fmt.Errorf("forecast provider returned HTTP %d", resp.StatusCode)
		}
		wait := time.Duration(attempt+1) * time.Second
		if h := resp.Header.Get("Retry-After"); h != "" {
			n, e := strconv.Atoi(h)
			if e != nil || n < 0 || n > 5 {
				return Forecast{}, fmt.Errorf("provider requests delayed retry; try again later")
			}
			wait = time.Duration(n) * time.Second
		}
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return Forecast{}, ctx.Err()
		case <-timer.C:
		}
	}
	n := len(raw.Hourly.Times)
	if n != 72 || len(raw.Hourly.Temperature) != n || len(raw.Hourly.Precipitation) != n || len(raw.Hourly.Wind) != n {
		return Forecast{}, fmt.Errorf("incomplete forecast: expected 72 aligned hourly readings")
	}
	if raw.Units.Time != "unixtime" || raw.Units.Temperature != "°C" || raw.Units.Precipitation != "mm" || raw.Units.Wind != "km/h" {
		return Forecast{}, fmt.Errorf("unexpected forecast units")
	}
	out := Forecast{Point: point, Times: raw.Hourly.Times}
	for i, t := range out.Times {
		if t <= 0 || (i > 0 && t-out.Times[i-1] != 3600) || raw.Hourly.Temperature[i] == nil || raw.Hourly.Precipitation[i] == nil || raw.Hourly.Wind[i] == nil {
			return Forecast{}, fmt.Errorf("missing value or inconsistent forecast times")
		}
		if *raw.Hourly.Precipitation[i] < 0 || *raw.Hourly.Wind[i] < 0 {
			return Forecast{}, fmt.Errorf("negative precipitation or wind")
		}
		out.Temperature = append(out.Temperature, *raw.Hourly.Temperature[i])
		out.Precipitation = append(out.Precipitation, *raw.Hourly.Precipitation[i])
		out.Wind = append(out.Wind, *raw.Hourly.Wind[i])
	}
	return out, nil
}
