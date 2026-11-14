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

// Read line paths in document order, preserving every separate geometry.
// No network links, XML entities, styles, or archive assets are resolved.
func parseKML(r io.Reader) (domain.Route, error) {
	decoder := xml.NewDecoder(r)
	route := domain.Route{Type: "MultiLineString"}
	depth := 0
	rootSeen := false
	geometry := ""
	geometryDepth := 0
	coordinateSeen := false
	count := 0
	var points []domain.Point
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
				case "Point", "Polygon", "LinearRing", "Model", "NetworkLink", "GroundOverlay", "PhotoOverlay", "ScreenOverlay":
					return domain.Route{}, fmt.Errorf("KML %s is not supported; provide line routes only", element.Name.Local)
				}
			}
			if (standard && element.Name.Local == "LineString") || track {
				if geometry != "" {
					return domain.Route{}, fmt.Errorf("nested KML geometry")
				}
				geometry = element.Name.Local
				geometryDepth = depth
				points = nil
				coordinateSeen = false
			}
			lineCoords := standard && element.Name.Local == "coordinates" && geometry == "LineString"
			trackCoords := element.Name.Space == gxNS && element.Name.Local == "coord" && geometry == "Track"
			if lineCoords || trackCoords {
				if depth != geometryDepth+1 {
					return domain.Route{}, fmt.Errorf("coordinates must belong directly to a route geometry")
				}
				if lineCoords && coordinateSeen {
					return domain.Route{}, fmt.Errorf("duplicate LineString coordinates")
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
					if count > 20000 {
						return domain.Route{}, fmt.Errorf("route exceeds 20,000 points")
					}
				}
			}
		case xml.EndElement:
			if geometry != "" && depth == geometryDepth {
				if len(points) < 2 {
					return domain.Route{}, fmt.Errorf("each KML line or track needs at least two coordinates")
				}
				route.Coordinates = append(route.Coordinates, points)
				if len(route.Coordinates) > 6 {
					return domain.Route{}, fmt.Errorf("provide at most 6 route segments")
				}
				geometry = ""
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
	if !rootSeen || depth != 0 || len(route.Coordinates) == 0 {
		return domain.Route{}, fmt.Errorf("KML contains no line routes")
	}
	return route, nil
}
