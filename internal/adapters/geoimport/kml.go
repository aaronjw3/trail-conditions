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

const kmlNS = "http://www.opengis.net/kml/2.2"
const gxNS = "http://www.google.com/kml/ext/2.2"

// Preserve points, lines, polygon rings, and mixed collections.
// No network links, XML entities, styles, or archive assets are resolved.
func parseKML(r io.Reader) (domain.Route, error) {
	decoder := xml.NewDecoder(r)
	route := domain.Route{}
	depth := 0
	rootSeen := false
	geometry := ""
	geometryDepth := 0
	coordinateSeen := false
	count := 0
	var points []domain.Point
	polygonDepth := 0
	boundary, boundaryDepth := "", 0
	var polygon [][]domain.Point
	boundaryHasRing := false
	outerSeen := false
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return domain.Route{}, fmt.Errorf("invalid KML XML")
		}
		switch element := token.(type) {
		case xml.StartElement:
			depth++
			if depth > 64 {
				return domain.Route{}, fmt.Errorf("KML nesting exceeds 64 levels")
			}
			if depth == 1 {
				if rootSeen || element.Name.Local != "kml" || (element.Name.Space != "" && element.Name.Space != kmlNS) {
					return domain.Route{}, fmt.Errorf("expected one KML 2.2 document")
				}
				rootSeen = true
			}
			standard := element.Name.Space == "" || element.Name.Space == kmlNS
			track := element.Name.Space == gxNS && element.Name.Local == "Track"
			if standard {
				switch element.Name.Local {
				case "Model", "NetworkLink", "GroundOverlay", "PhotoOverlay", "ScreenOverlay":
					return domain.Route{}, fmt.Errorf("KML %s is not supported", element.Name.Local)
				}
			}
			if standard && element.Name.Local == "Polygon" {
				if geometry != "" || polygonDepth != 0 {
					return domain.Route{}, fmt.Errorf("nested KML geometry")
				}
				polygonDepth = depth
				polygon = [][]domain.Point{nil}
				outerSeen = false
			}
			if standard && (element.Name.Local == "outerBoundaryIs" || element.Name.Local == "innerBoundaryIs") {
				if polygonDepth == 0 || depth != polygonDepth+1 || boundary != "" {
					return domain.Route{}, fmt.Errorf("invalid polygon boundary")
				}
				boundary, boundaryDepth = element.Name.Local, depth
				boundaryHasRing = false
				if boundary == "outerBoundaryIs" {
					if outerSeen {
						return domain.Route{}, fmt.Errorf("duplicate polygon exterior")
					}
					outerSeen = true
				}
			}
			if (standard && (element.Name.Local == "Point" || element.Name.Local == "LineString" || element.Name.Local == "LinearRing")) || track {
				if geometry != "" {
					return domain.Route{}, fmt.Errorf("nested KML geometry")
				}
				if polygonDepth != 0 && (element.Name.Local != "LinearRing" || boundary == "" || depth != boundaryDepth+1 || boundaryHasRing) {
					return domain.Route{}, fmt.Errorf("polygon boundaries must contain one LinearRing")
				}
				geometry = element.Name.Local
				geometryDepth = depth
				points = nil
				coordinateSeen = false
			}
			lineCoords := standard && element.Name.Local == "coordinates" && geometry != "" && geometry != "Track"
			trackCoords := element.Name.Space == gxNS && element.Name.Local == "coord" && geometry == "Track"
			if lineCoords || trackCoords {
				if depth != geometryDepth+1 {
					return domain.Route{}, fmt.Errorf("coordinates must belong directly to a geometry")
				}
				if lineCoords && coordinateSeen {
					return domain.Route{}, fmt.Errorf("duplicate geometry coordinates")
				}
				coordinateSeen = true
				var text string
				if err := decoder.DecodeElement(&text, &element); err != nil {
					return domain.Route{}, fmt.Errorf("invalid coordinate element")
				}
				depth--
				tuples := strings.Fields(text)
				if trackCoords {
					tuples = []string{text}
				}
				if len(tuples) == 0 {
					return domain.Route{}, fmt.Errorf("missing KML coordinates")
				}
				for _, tuple := range tuples {
					parts := strings.Split(tuple, ",")
					if trackCoords {
						parts = strings.Fields(tuple)
					}
					if len(parts) < 2 || len(parts) > 3 || (trackCoords && len(parts) != 3) {
						return domain.Route{}, fmt.Errorf("invalid KML coordinate tuple")
					}
					values := make([]float64, len(parts))
					for i, part := range parts {
						value, err := strconv.ParseFloat(part, 64)
						if err != nil || math.IsNaN(value) || math.IsInf(value, 0) {
							return domain.Route{}, fmt.Errorf("invalid KML coordinate number")
						}
						values[i] = value
					}
					points = append(points, domain.Point{values[0], values[1]})
					count++
					if count > domain.MaxRoutePoints {
						return domain.Route{}, fmt.Errorf("route exceeds %d points", domain.MaxRoutePoints)
					}
				}
			}
		case xml.EndElement:
			if geometry != "" && depth == geometryDepth {
				switch geometry {
				case "Point":
					if len(points) != 1 {
						return domain.Route{}, fmt.Errorf("KML Point needs exactly one coordinate")
					}
					route.Points = append(route.Points, points[0])
				case "LinearRing":
					if len(points) < 4 || points[0] != points[len(points)-1] {
						return domain.Route{}, fmt.Errorf("LinearRing needs at least four coordinates and must be closed")
					}
					if polygonDepth != 0 {
						if boundary == "outerBoundaryIs" {
							polygon[0] = points
						} else {
							polygon = append(polygon, points)
						}
						boundaryHasRing = true
					} else {
						unique := make(map[domain.Point]bool)
						for _, point := range points {
							unique[point] = true
						}
						if len(unique) < 3 {
							return domain.Route{}, fmt.Errorf("LinearRing needs at least three distinct vertices")
						}
						route.Lines = append(route.Lines, points)
					}
				default:
					if len(points) < 2 {
						return domain.Route{}, fmt.Errorf("each KML line or track needs at least two coordinates")
					}
					route.Lines = append(route.Lines, points)
				}
				geometry = ""
			}
			if boundary != "" && depth == boundaryDepth {
				if !boundaryHasRing {
					return domain.Route{}, fmt.Errorf("polygon boundary has no ring")
				}
				boundary = ""
			}
			if polygonDepth != 0 && depth == polygonDepth {
				if len(polygon[0]) == 0 {
					return domain.Route{}, fmt.Errorf("polygon needs an exterior ring")
				}
				route.Polygons = append(route.Polygons, polygon)
				polygonDepth = 0
			}
			depth--
		case xml.CharData:
			if depth == 0 && strings.TrimSpace(string(element)) != "" {
				return domain.Route{}, fmt.Errorf("text outside KML document")
			}
		case xml.Directive:
			return domain.Route{}, fmt.Errorf("XML directives are not supported")
		}
	}
	if !rootSeen || depth != 0 {
		return domain.Route{}, fmt.Errorf("expected a KML document")
	}
	return route, nil
}
