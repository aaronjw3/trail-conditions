package geoimport

import (
	"archive/zip"
	"bytes"
	"fmt"
	"strings"
	"testing"

	"github.com/aaronjw3/trail-conditions/internal/domain"
)

const gpx = `<gpx><trk><trkseg><trkpt lat="45" lon="-111"/><trkpt lat="45.1" lon="-111.1"/></trkseg><trkseg><trkpt lat="46" lon="-112"/><trkpt lat="46.1" lon="-112.1"/></trkseg></trk></gpx>`
const kml = `<kml xmlns="http://www.opengis.net/kml/2.2"><Document><Folder><Placemark><MultiGeometry><LineString><coordinates>-111,45,1000 -111.1,45.1,1100</coordinates></LineString><LineString><coordinates>-112,46 -112.1,46.1</coordinates></LineString></MultiGeometry></Placemark></Folder></Document></kml>`

func kmz(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buffer bytes.Buffer
	archive := zip.NewWriter(&buffer)
	for name, content := range files {
		file, err := archive.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := file.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}
func TestFormatsPreserveSegments(t *testing.T) {
	for filename, data := range map[string][]byte{"route.gpx": []byte(gpx), "route.KML": []byte(kml), "route.kmz": kmz(t, map[string]string{"nested/doc.kml": kml, "image.png": "unused"})} {
		route, err := (Decoder{}).Decode(filename, data)
		if err != nil {
			t.Fatal(filename, err)
		}
		samples, err := route.Samples()
		if err != nil {
			t.Fatal(err)
		}
		if len(route.Lines) != 2 || len(samples) != 4 || samples[0] != (domain.Point{-111, 45}) || samples[2] != (domain.Point{-112, 46}) {
			t.Fatal("lost segments or coordinate order", route)
		}
	}
}
func TestKMLTracksAndKMZMultipleDocuments(t *testing.T) {
	track := `<kml xmlns:gx="http://www.google.com/kml/ext/2.2"><Placemark><gx:MultiTrack><gx:Track><gx:coord>-111 45 1000</gx:coord><gx:coord>-111.1 45.1 1100</gx:coord></gx:Track><gx:Track><gx:coord>-112 46 1000</gx:coord><gx:coord>-112.1 46.1 1100</gx:coord></gx:Track></gx:MultiTrack></Placemark></kml>`
	route, err := (Decoder{}).Decode("track.kml", []byte(track))
	if err != nil || len(route.Lines) != 2 {
		t.Fatal("tracks", err)
	}
	route, err = (Decoder{}).Decode("multi.kmz", kmz(t, map[string]string{"a.kml": kml, "b.kml": track}))
	if err != nil || len(route.Lines) != 4 {
		t.Fatal("KMZ lost a document", err)
	}
}
func TestRejectsMalformedAndUnsupportedRoutes(t *testing.T) {
	for name, input := range map[string]string{
		"nan.gpx":      strings.Replace(gpx, `lat="45"`, `lat="NaN"`, 1),
		"range.gpx":    strings.Replace(gpx, `lon="-111"`, `lon="181"`, 1),
		"missing.gpx":  strings.Replace(gpx, `lat="45"`, ``, 1),
		"empty.gpx":    `<gpx/>`,
		"trailing.gpx": gpx + `<gpx/>`,
		"zero.gpx":     strings.Replace(gpx, `lat="45.1" lon="-111.1"`, `lat="45" lon="-111"`, 1),
		"wrong.kml":    gpx,
		"trailing.kml": kml + `<kml/>`,
		"missing.kml":  strings.Replace(kml, "-111,45,1000 -111.1,45.1,1100", "", 1),
		"nan.kml":      strings.Replace(kml, "-111,45,1000", "-111,NaN,1000", 1),
		"polygon.kml":  `<kml><Placemark><Polygon/></Placemark></kml>`,
		"network.kml":  strings.Replace(kml, "<Document>", "<Document><NetworkLink/>", 1),
		"zip.kmz":      "not a zip",
		"route.shp":    "unsupported",
	} {
		if _, err := (Decoder{}).Decode(name, []byte(input)); err == nil {
			t.Fatal("accepted", name)
		}
	}
}
func TestKMZBoundsAndMissingGeometry(t *testing.T) {
	files := map[string]string{}
	for i := 0; i < 65; i++ {
		files[fmt.Sprintf("%d.txt", i)] = ""
	}
	for _, data := range [][]byte{
		kmz(t, files), kmz(t, map[string]string{"image.png": "no route"}),
		kmz(t, map[string]string{"doc.kml": strings.Repeat(" ", (8<<20)+1)}),
		kmz(t, map[string]string{"doc.kml": kml, "broken.kml": "<kml>"}),
	} {
		if _, err := (Decoder{}).Decode("route.kmz", data); err == nil {
			t.Fatal("accepted invalid archive")
		}
	}
	if _, err := (Decoder{}).Decode("route.kml", bytes.Repeat([]byte(" "), (2<<20)+1)); err == nil {
		t.Fatal("accepted oversized upload")
	}
}
func TestSamplingIsBoundedAndKeepsEndpoints(t *testing.T) {
	var xml strings.Builder
	xml.WriteString(`<gpx><trk>`)
	for segment := 0; segment < 3; segment++ {
		xml.WriteString(`<trkseg>`)
		for i := 0; i < 100; i++ {
			fmt.Fprintf(&xml, `<trkpt lat="%f" lon="-111"/>`, 45+float64(segment)+float64(i)/1000)
		}
		xml.WriteString(`</trkseg>`)
	}
	xml.WriteString(`</trk></gpx>`)
	route, err := (Decoder{}).Decode("route.gpx", []byte(xml.String()))
	if err != nil {
		t.Fatal(err)
	}
	samples, err := route.Samples()
	if err != nil || len(samples) != 12 {
		t.Fatal("sample budget", err)
	}
	for i, segment := range route.Lines {
		if samples[i*4] != segment[0] || samples[i*4+3] != segment[len(segment)-1] {
			t.Fatal("lost endpoint")
		}
	}
}

func TestGPXWaypointsAndMixedPaths(t *testing.T) {
	for name, xml := range map[string]string{
		"waypoints": `<gpx><wpt lat="45" lon="-111"/><wpt lat="46" lon="-112"/></gpx>`,
		"mixed":     strings.Replace(gpx, "<gpx>", `<gpx><wpt lat="45" lon="-111"/><wpt lat="46" lon="-112"/>`, 1),
	} {
		route, err := (Decoder{}).Decode(name+".gpx", []byte(xml))
		if err != nil {
			t.Fatal(err)
		}
		if len(route.Points) != 2 || route.Points[0] != (domain.Point{-111, 45}) {
			t.Fatal("lost waypoints", route)
		}
		if name == "mixed" && len(route.Lines) != 2 {
			t.Fatal("lost track segments")
		}
		if name == "waypoints" && len(route.Lines) != 0 {
			t.Fatal("connected standalone waypoints")
		}
	}
	// GPX permits single-point routes and track segments; preserve them as points.
	route, err := (Decoder{}).Decode("single.gpx", []byte(`<gpx><rte><rtept lat="45" lon="-111"/></rte></gpx>`))
	if err != nil || len(route.Points) != 1 {
		t.Fatal("single-point route", route, err)
	}
}

const mixedKML = `<kml><Placemark><MultiGeometry>
<Point><coordinates>-111,45</coordinates></Point>
<MultiGeometry><LineString><coordinates>-111,45 -112,46</coordinates></LineString></MultiGeometry>
<Polygon><innerBoundaryIs><LinearRing><coordinates>-111.8,45.2 -111.6,45.2 -111.6,45.3 -111.8,45.2</coordinates></LinearRing></innerBoundaryIs>
<outerBoundaryIs><LinearRing><coordinates>-112,45 -111,45 -111,46 -112,45</coordinates></LinearRing></outerBoundaryIs></Polygon>
</MultiGeometry></Placemark></kml>`

func TestKMLAndKMZPreserveMixedGeometryAndPolygonHoles(t *testing.T) {
	for filename, data := range map[string][]byte{
		"mixed.kml": []byte(mixedKML),
		"mixed.kmz": kmz(t, map[string]string{"doc.kml": mixedKML, "icons/pin.png": "ignored"}),
	} {
		route, err := (Decoder{}).Decode(filename, data)
		if err != nil {
			t.Fatal(filename, err)
		}
		if len(route.Points) != 1 || len(route.Lines) != 1 || len(route.Polygons) != 1 || len(route.Polygons[0]) != 2 {
			t.Fatal("lost mixed geometry", route)
		}
		if route.Polygons[0][0][0] != (domain.Point{-112, 45}) || route.Polygons[0][1][0] != (domain.Point{-111.8, 45.2}) {
			t.Fatal("shell/hole order changed", route)
		}
		samples, err := route.Samples()
		if err != nil || len(samples) > domain.MaxForecastLocations {
			t.Fatal("sample budget", err)
		}
		for _, sample := range samples {
			if sample == route.Polygons[0][1][0] {
				t.Fatal("sampled polygon hole")
			}
		}
	}
}

func TestRejectsMalformedPointsAndPolygonBoundaries(t *testing.T) {
	for _, geometry := range []string{
		`<Point/>`, `<Point><coordinates>1,1 2,2</coordinates></Point>`,
		`<Point><coordinates>NaN,1</coordinates></Point>`,
		`<Polygon/>`,
		`<Polygon><outerBoundaryIs/></Polygon>`,
		`<Polygon><outerBoundaryIs><LinearRing><coordinates>1,1 2,2 3,1 4,1</coordinates></LinearRing></outerBoundaryIs></Polygon>`,
		`<Polygon><outerBoundaryIs><LinearRing><coordinates>1,1 1,1 1,1 1,1</coordinates></LinearRing></outerBoundaryIs></Polygon>`,
		`<Polygon><LinearRing><coordinates>1,1 2,2 3,1 1,1</coordinates></LinearRing></Polygon>`,
		`<LinearRing><coordinates>1,1 2,2 3,1</coordinates></LinearRing>`,
		`<Polygon><outerBoundaryIs><Point><coordinates>1,1</coordinates></Point></outerBoundaryIs></Polygon>`,
	} {
		if _, err := (Decoder{}).Decode("invalid.kml", []byte("<kml><Placemark>"+geometry+"</Placemark></kml>")); err == nil {
			t.Fatal("accepted", geometry)
		}
	}
	invalidHole := strings.Replace(mixedKML, "-111.8,45.2", "-111.8,95.2", -1)
	if _, err := (Decoder{}).Decode("invalid.kml", []byte(invalidHole)); err == nil {
		t.Fatal("accepted invalid hole coordinates")
	}
}

func TestManyWaypointsRetainedWithBoundedWeatherSamples(t *testing.T) {
	var xml strings.Builder
	xml.WriteString("<gpx>")
	for i := 0; i < 100; i++ {
		fmt.Fprintf(&xml, `<wpt lat="%f" lon="-111"/>`, 45+float64(i)/1000)
	}
	xml.WriteString("</gpx>")
	route, err := (Decoder{}).Decode("waypoints.gpx", []byte(xml.String()))
	if err != nil {
		t.Fatal(err)
	}
	samples, err := route.Samples()
	if err != nil || len(samples) != 12 || len(route.Points) != 100 {
		t.Fatal("sampling discarded geometry", route, err)
	}
	if samples[0] != route.Points[0] || samples[11] != route.Points[99] {
		t.Fatal("sampling did not span the collection")
	}
}
