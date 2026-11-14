package main

import (
	"encoding/xml"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"
)

type Point [2]float64 // GeoJSON order: longitude, latitude.
type Route struct {
	Type        string    `json:"type"`
	Coordinates [][]Point `json:"coordinates"`
}
type gpxPoint struct {
	Lat string `xml:"lat,attr"`
	Lon string `xml:"lon,attr"`
}

func parseRoute(r io.Reader) (Route, []Point, error) {
	var doc struct {
		XMLName xml.Name `xml:"gpx"`
		Tracks  []struct {
			Segments []struct {
				Points []gpxPoint `xml:"trkpt"`
			} `xml:"trkseg"`
		} `xml:"trk"`
		Routes []struct {
			Points []gpxPoint `xml:"rtept"`
		} `xml:"rte"`
	}
	decoder := xml.NewDecoder(r)
	if err := decoder.Decode(&doc); err != nil {
		return Route{}, nil, fmt.Errorf("invalid GPX XML")
	}
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return Route{}, nil, fmt.Errorf("invalid trailing XML")
		}
		if text, ok := token.(xml.CharData); ok && strings.TrimSpace(string(text)) != "" {
			return Route{}, nil, fmt.Errorf("text after GPX document")
		}
		if _, ok := token.(xml.StartElement); ok {
			return Route{}, nil, fmt.Errorf("multiple XML documents")
		}
	}
	var groups [][]gpxPoint
	for _, track := range doc.Tracks {
		for _, segment := range track.Segments {
			groups = append(groups, segment.Points)
		}
	}
	for _, route := range doc.Routes {
		groups = append(groups, route.Points)
	}
	if len(groups) == 0 || len(groups) > 6 {
		return Route{}, nil, fmt.Errorf("provide 1–6 track segments or routes; standalone waypoints are not routes")
	}
	route := Route{Type: "MultiLineString"}
	count := 0
	for _, group := range groups {
		if len(group) < 2 {
			return Route{}, nil, fmt.Errorf("each segment needs at least two points")
		}
		points := make([]Point, 0, len(group))
		for _, p := range group {
			lat, e1 := strconv.ParseFloat(p.Lat, 64)
			lon, e2 := strconv.ParseFloat(p.Lon, 64)
			if e1 != nil || e2 != nil || math.IsNaN(lat) || math.IsNaN(lon) || math.Abs(lat) > 90 || math.Abs(lon) > 180 {
				return Route{}, nil, fmt.Errorf("coordinates must be finite WGS84 longitude/latitude")
			}
			count++
			if count > 20000 {
				return Route{}, nil, fmt.Errorf("route exceeds 20,000 points")
			}
			points = append(points, Point{lon, lat})
		}
		route.Coordinates = append(route.Coordinates, points)
	}
	// Every segment gets both endpoints. Remaining slots follow distance along segments,
	// never across gaps between tracks. Samples are original GPS points.
	samples := []Point{}
	budget := 12 / len(route.Coordinates)
	for _, points := range route.Coordinates {
		distances := make([]float64, len(points))
		for i := 1; i < len(points); i++ {
			distances[i] = distances[i-1] + distance(points[i-1], points[i])
		}
		total := distances[len(points)-1]
		if total == 0 {
			return Route{}, nil, fmt.Errorf("segment has no length")
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
	return route, samples, nil
}

func distance(a, b Point) float64 {
	const rad = math.Pi / 180
	dlat := (b[1] - a[1]) * rad
	dlon := (b[0] - a[0]) * rad
	h := math.Sin(dlat/2)*math.Sin(dlat/2) + math.Cos(a[1]*rad)*math.Cos(b[1]*rad)*math.Sin(dlon/2)*math.Sin(dlon/2)
	return 6371 * 2 * math.Asin(math.Sqrt(math.Min(1, h)))
}
