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
		if len(route.Coordinates) != 2 || len(samples) != 4 || samples[0] != (domain.Point{-111, 45}) || samples[2] != (domain.Point{-112, 46}) {
			t.Fatal("lost segments or coordinate order", route)
		}
	}
}
func TestKMLTracksAndKMZMultipleDocuments(t *testing.T) {
	track := `<kml xmlns:gx="http://www.google.com/kml/ext/2.2"><Placemark><gx:MultiTrack><gx:Track><gx:coord>-111 45 1000</gx:coord><gx:coord>-111.1 45.1 1100</gx:coord></gx:Track><gx:Track><gx:coord>-112 46 1000</gx:coord><gx:coord>-112.1 46.1 1100</gx:coord></gx:Track></gx:MultiTrack></Placemark></kml>`
	route, err := (Decoder{}).Decode("track.kml", []byte(track))
	if err != nil || len(route.Coordinates) != 2 {
		t.Fatal("tracks", err)
	}
	route, err = (Decoder{}).Decode("multi.kmz", kmz(t, map[string]string{"a.kml": kml, "b.kml": track}))
	if err != nil || len(route.Coordinates) != 4 {
		t.Fatal("KMZ lost a document", err)
	}
}
func TestRejectsMalformedAndUnsupportedRoutes(t *testing.T) {
	for name, input := range map[string]string{
		"nan.gpx":      strings.Replace(gpx, `lat="45"`, `lat="NaN"`, 1),
		"range.gpx":    strings.Replace(gpx, `lon="-111"`, `lon="181"`, 1),
		"missing.gpx":  strings.Replace(gpx, `lat="45"`, ``, 1),
		"empty.gpx":    `<gpx><wpt lat="1" lon="1"/></gpx>`,
		"trailing.gpx": gpx + `<gpx/>`,
		"zero.gpx":     strings.Replace(gpx, `lat="45.1" lon="-111.1"`, `lat="45" lon="-111"`, 1),
		"wrong.kml":    gpx,
		"trailing.kml": kml + `<kml/>`,
		"missing.kml":  strings.Replace(kml, "-111,45,1000 -111.1,45.1,1100", "", 1),
		"nan.kml":      strings.Replace(kml, "-111,45,1000", "-111,NaN,1000", 1),
		"mixed.kml":    strings.Replace(kml, "<MultiGeometry>", "<MultiGeometry><Point><coordinates>-111,45</coordinates></Point>", 1),
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
	for i, segment := range route.Coordinates {
		if samples[i*4] != segment[0] || samples[i*4+3] != segment[len(segment)-1] {
			t.Fatal("lost endpoint")
		}
	}
}
