package domain

import (
	"fmt"
	"math"
)

// Validate enforces the complete publication contract independently of a provider.
func (s Snapshot) Validate() error {
	points, err := s.Route.Samples()
	if err != nil {
		return err
	}
	if err := s.ForecastSnapshot.Validate(); err != nil {
		return err
	}
	if len(s.Forecasts) != len(points) {
		return fmt.Errorf("snapshot has mismatched forecast locations")
	}
	for i, f := range s.Forecasts {
		if f.Point != points[i] {
			return fmt.Errorf("snapshot has mismatched forecast locations")
		}
	}
	return nil
}

// Forecast-only reads validate the bounded weather data without loading geometry.
func (s ForecastSnapshot) Validate() error {
	if s.ID == "" || s.Name == "" || s.RetrievedAt.IsZero() || len(s.Forecasts) == 0 || len(s.Forecasts) > MaxForecastLocations {
		return fmt.Errorf("snapshot is missing its timestamp or forecast locations")
	}
	for _, f := range s.Forecasts {
		if math.IsNaN(f.Point[0]) || math.IsNaN(f.Point[1]) || math.Abs(f.Point[0]) > 180 || math.Abs(f.Point[1]) > 90 {
			return fmt.Errorf("invalid forecast location")
		}
		if len(f.Times) != ForecastHours || len(f.Temperature) != ForecastHours || len(f.Precipitation) != ForecastHours || len(f.Wind) != ForecastHours {
			return fmt.Errorf("snapshot has incomplete or mismatched forecasts")
		}
		for j, t := range f.Times {
			if t <= 0 || (j > 0 && t-f.Times[j-1] != 3600) || t != s.Forecasts[0].Times[j] {
				return fmt.Errorf("forecast timelines are not aligned")
			}
			for _, value := range []float64{f.Temperature[j], f.Precipitation[j], f.Wind[j]} {
				if math.IsNaN(value) || math.IsInf(value, 0) {
					return fmt.Errorf("forecast values must be finite")
				}
			}
			if f.Precipitation[j] < 0 || f.Wind[j] < 0 {
				return fmt.Errorf("precipitation and wind cannot be negative")
			}
		}
	}
	return nil
}
