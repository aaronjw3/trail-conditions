package domain

import (
	"math"
	"reflect"
	"testing"
)

func TestLineSamplingKeepsExistingForecastLocations(t *testing.T) {
	// Existing saved snapshots depend on deterministic line-only sampling,
	// including the repeated endpoint of a closed route.
	route := Route{Lines: [][]Point{{{0, 0}, {1, 1}, {2, 0}, {0, 0}}, {{3, 3}, {4, 4}}}}
	samples, err := route.Samples()
	want := []Point{{0, 0}, {1, 1}, {2, 0}, {0, 0}, {3, 3}, {4, 4}}
	if err != nil || !reflect.DeepEqual(samples, want) {
		t.Fatal(samples, err)
	}
}

func TestSamplingValidatesUnsampledCoordinatesAndHoles(t *testing.T) {
	points := make([]Point, 100)
	for i := range points {
		points[i] = Point{float64(i), 45}
	}
	points[1][1] = math.Inf(1)
	if _, err := (Route{Points: points}).Samples(); err == nil {
		t.Fatal("accepted invalid unsampled point")
	}
	route := Route{Polygons: [][][]Point{{{{0, 0}, {2, 0}, {2, 2}, {0, 0}}, {{.5, .5}, {1, .5}, {1, 95}, {.5, .5}}}}}
	if _, err := route.Samples(); err == nil {
		t.Fatal("accepted invalid hole")
	}
	if _, err := (Route{Points: make([]Point, MaxRoutePoints+1)}).Samples(); err == nil {
		t.Fatal("accepted coordinate limit overflow")
	}
}

func TestManyLinesPreservedAndForecastBudgetBounded(t *testing.T) {
	route := Route{}
	for i := 0; i < 20; i++ {
		route.Lines = append(route.Lines, []Point{{float64(i), 0}, {float64(i), 1}})
	}
	samples, err := route.Samples()
	if err != nil || len(samples) != MaxForecastLocations || len(route.Lines) != 20 {
		t.Fatal(samples, err)
	}
	if samples[0] != route.Lines[0][0] || samples[len(samples)-1] != route.Lines[19][0] {
		t.Fatal("sampling does not span collection")
	}
}
