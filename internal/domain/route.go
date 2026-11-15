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
	MaxForecastLocations = 12
)

type Point [2]float64 // GeoJSON order: longitude, latitude.
type Route struct {
	Points   []Point     `json:"points,omitempty"`
	Lines    [][]Point   `json:"lines,omitempty"`
	Polygons [][][]Point `json:"polygons,omitempty"`
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
	// Sampling paths never connect separate features. Polygon samples follow the
	// exterior boundary; interior rings are retained for rendering and validation.
	paths := make([][]Point, 0, len(route.Lines)+len(route.Points)+len(route.Polygons))
	count := 0
	for _, point := range route.Points {
		paths = append(paths, []Point{point})
		count++
	}
	for _, line := range route.Lines {
		if len(line) < 2 {
			return nil, fmt.Errorf("each line needs at least two points")
		}
		paths = append(paths, line)
		count += len(line)
	}
	for _, polygon := range route.Polygons {
		if len(polygon) == 0 {
			return nil, fmt.Errorf("polygon needs an exterior ring")
		}
		for _, ring := range polygon {
			if len(ring) < 4 || ring[0] != ring[len(ring)-1] {
				return nil, fmt.Errorf("polygon rings need at least four coordinates and must be closed")
			}
			unique := make(map[Point]bool)
			for _, point := range ring {
				unique[point] = true
			}
			if len(unique) < 3 {
				return nil, fmt.Errorf("polygon rings need at least three distinct vertices")
			}
			count += len(ring)
		}
		paths = append(paths, polygon[0])
	}
	if len(paths) == 0 {
		return nil, fmt.Errorf("file contains no supported geometry")
	}
	if count > MaxRoutePoints {
		return nil, fmt.Errorf("geometry exceeds %d points", MaxRoutePoints)
	}
	// Validate every coordinate, including unsampled geometries and polygon holes.
	all := append([][]Point{route.Points}, route.Lines...)
	for _, polygon := range route.Polygons {
		all = append(all, polygon...)
	}
	for _, points := range all {
		for _, point := range points {
			if math.IsNaN(point[0]) || math.IsNaN(point[1]) || math.Abs(point[0]) > 180 || math.Abs(point[1]) > 90 {
				return nil, fmt.Errorf("coordinates must be finite WGS84 longitude/latitude")
			}
		}
	}
	for _, line := range route.Lines {
		nonzero := false
		for _, point := range line[1:] {
			if point != line[0] {
				nonzero = true
				break
			}
		}
		if !nonzero {
			return nil, fmt.Errorf("line has no length")
		}
	}
	// Large collections retain every geometry, but use evenly spaced components
	// for weather requests so the provider budget does not grow with file size.
	if len(paths) > MaxForecastLocations {
		selected := make([][]Point, MaxForecastLocations)
		for i := range selected {
			selected[i] = paths[i*(len(paths)-1)/(MaxForecastLocations-1)]
		}
		paths = selected
	}
	samples := []Point{}
	budget := MaxForecastLocations / len(paths)
	for _, points := range paths {
		if len(points) == 1 || budget == 1 {
			samples = append(samples, points[0])
			continue
		}
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
