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
	if s.RetrievedAt.IsZero() || len(s.Forecasts) != len(points) {
		return fmt.Errorf("snapshot is missing its timestamp or forecast locations")
	}
	for i, f := range s.Forecasts {
		if f.Point != points[i] || len(f.Times) != 72 || len(f.Temperature) != 72 || len(f.Precipitation) != 72 || len(f.Wind) != 72 {
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
