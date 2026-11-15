package geoimport

import (
	"encoding/xml"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"

	"github.com/aaronjw3/trail-conditions/internal/domain"
)

type gpxPoint struct {
	Lat string `xml:"lat,attr"`
	Lon string `xml:"lon,attr"`
}

func parseGPX(r io.Reader) (domain.Route, error) {
	var doc struct {
		XMLName   xml.Name   `xml:"gpx"`
		Waypoints []gpxPoint `xml:"wpt"`
		Tracks    []struct {
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
		return domain.Route{}, fmt.Errorf("invalid GPX XML")
	}
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return domain.Route{}, fmt.Errorf("invalid trailing XML")
		}
		if text, ok := token.(xml.CharData); ok && strings.TrimSpace(string(text)) != "" {
			return domain.Route{}, fmt.Errorf("text after GPX document")
		}
		if _, ok := token.(xml.StartElement); ok {
			return domain.Route{}, fmt.Errorf("multiple XML documents")
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
	route := domain.Route{}
	for _, waypoint := range doc.Waypoints {
		groups = append(groups, []gpxPoint{waypoint})
	}
	count := 0
	for _, group := range groups {
		if len(group) == 0 {
			continue
		}
		points := make([]domain.Point, 0, len(group))
		for _, p := range group {
			lat, e1 := strconv.ParseFloat(p.Lat, 64)
			lon, e2 := strconv.ParseFloat(p.Lon, 64)
			if e1 != nil || e2 != nil || math.IsNaN(lat) || math.IsNaN(lon) || math.Abs(lat) > 90 || math.Abs(lon) > 180 {
				return domain.Route{}, fmt.Errorf("coordinates must be finite WGS84 longitude/latitude")
			}
			count++
			if count > domain.MaxRoutePoints {
				return domain.Route{}, fmt.Errorf("route exceeds %d points", domain.MaxRoutePoints)
			}
			points = append(points, domain.Point{lon, lat})
		}
		if len(points) == 1 {
			route.Points = append(route.Points, points[0])
		} else {
			route.Lines = append(route.Lines, points)
		}
	}
	return route, nil
}
