package domain

import (
	"fmt"
	"math"
	"time"
)

const (
	ForecastDays         = 3
	ForecastHours        = ForecastDays * 24
	MaxRoutePoints       = 20_000
	MaxRouteSegments     = 6
	MaxForecastLocations = 12
)

type Point [2]float64 // GeoJSON order: longitude, latitude.
type Route struct {
	Type        string    `json:"type"`
	Coordinates [][]Point `json:"coordinates"`
}
type Forecast struct {
	Point         Point     `json:"point"`
	Times         []int64   `json:"times"`
	Temperature   []float64 `json:"temperature"`
	Precipitation []float64 `json:"precipitation"`
	Wind          []float64 `json:"wind"`
}

type SavedRoute struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	RetrievedAt time.Time `json:"retrieved_at"`
}

type Snapshot struct {
	ID          string     `json:"id"`
	Name        string     `json:"name"`
	Route       Route      `json:"route"`
	Forecasts   []Forecast `json:"forecasts"`
	RetrievedAt time.Time  `json:"retrieved_at"`
}

func (route Route) Samples() ([]Point, error) {
	if route.Type != "MultiLineString" || len(route.Coordinates) == 0 || len(route.Coordinates) > MaxRouteSegments {
		return nil, fmt.Errorf("provide 1–%d route segments", MaxRouteSegments)
	}
	count := 0
	for _, segment := range route.Coordinates {
		if len(segment) < 2 {
			return nil, fmt.Errorf("each segment needs at least two points")
		}
		for _, point := range segment {
			count++
			if count > MaxRoutePoints {
				return nil, fmt.Errorf("route exceeds %d points", MaxRoutePoints)
			}
			if math.IsNaN(point[0]) || math.IsNaN(point[1]) || math.Abs(point[0]) > 180 || math.Abs(point[1]) > 90 {
				return nil, fmt.Errorf("coordinates must be finite WGS84 longitude/latitude")
			}
		}
	}
	// Every segment gets both endpoints. Remaining slots follow distance along segments,
	// never across gaps between tracks. Samples are original GPS points.
	samples := []Point{}
	budget := MaxForecastLocations / len(route.Coordinates)
	for _, points := range route.Coordinates {
		distances := make([]float64, len(points))
		for i := 1; i < len(points); i++ {
			distances[i] = distances[i-1] + distance(points[i-1], points[i])
		}
		total := distances[len(points)-1]
		if total == 0 {
			return nil, fmt.Errorf("segment has no length")
		}
		samples = append(samples, points[0])
		last := 0
		for slot := 1; slot < budget-1; slot++ {
			target := total * float64(slot) / float64(budget-1)
			i := last
			for i < len(points)-1 && distances[i] < target {
				i++
			}
			if i != last && i < len(points)-1 {
				samples = append(samples, points[i])
				last = i
			}
		}
		samples = append(samples, points[len(points)-1])
	}
	return samples, nil
}

func distance(a, b Point) float64 {
	const rad = math.Pi / 180
	dlat := (b[1] - a[1]) * rad
	dlon := (b[0] - a[0]) * rad
	h := math.Sin(dlat/2)*math.Sin(dlat/2) + math.Cos(a[1]*rad)*math.Cos(b[1]*rad)*math.Sin(dlon/2)*math.Sin(dlon/2)
	return 6371 * 2 * math.Asin(math.Sqrt(math.Min(1, h)))
}
