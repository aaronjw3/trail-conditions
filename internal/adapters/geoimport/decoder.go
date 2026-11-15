package geoimport

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/aaronjw3/trail-conditions/internal/domain"
)

// Decoder normalizes supported route formats into WGS84 points, lines, and polygons.
// File extension selects the parser; each parser validates the actual content.
type Decoder struct{}

func (Decoder) Decode(filename string, data []byte) (domain.Route, error) {
	if len(data) > 2<<20 {
		return domain.Route{}, fmt.Errorf("route upload exceeds 2 MiB")
	}
	var route domain.Route
	var err error
	switch strings.ToLower(filepath.Ext(filename)) {
	case ".gpx":
		route, err = parseGPX(bytes.NewReader(data))
	case ".kml":
		route, err = parseKML(bytes.NewReader(data))
	case ".kmz":
		route, err = parseKMZ(data)
	default:
		return route, fmt.Errorf("unsupported route format; choose GPX, KML, or KMZ")
	}
	if err != nil {
		return domain.Route{}, err
	}
	if _, err = route.Samples(); err != nil {
		return domain.Route{}, err
	}
	return route, nil
}

func parseKMZ(data []byte) (domain.Route, error) {
	archive, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return domain.Route{}, fmt.Errorf("invalid KMZ archive")
	}
	if len(archive.File) > 64 {
		return domain.Route{}, fmt.Errorf("KMZ exceeds 64 entries")
	}
	route := domain.Route{}
	remaining := int64(8 << 20)
	for _, entry := range archive.File {
		if entry.FileInfo().IsDir() || !strings.EqualFold(filepath.Ext(entry.Name), ".kml") {
			continue
		}
		if entry.UncompressedSize64 > uint64(remaining) {
			return domain.Route{}, fmt.Errorf("KMZ KML content exceeds 8 MiB")
		}
		reader, err := entry.Open()
		if err != nil {
			return domain.Route{}, fmt.Errorf("cannot open KMZ member")
		}
		content, err := io.ReadAll(io.LimitReader(reader, remaining+1))
		reader.Close()
		if err != nil {
			return domain.Route{}, fmt.Errorf("cannot read KMZ member: %w", err)
		}
		remaining -= int64(len(content))
		if remaining < 0 {
			return domain.Route{}, fmt.Errorf("KMZ KML content exceeds 8 MiB")
		}
		part, err := parseKML(bytes.NewReader(content))
		if err != nil {
			return domain.Route{}, fmt.Errorf("KMZ KML member: %w", err)
		}
		route.Points = append(route.Points, part.Points...)
		route.Lines = append(route.Lines, part.Lines...)
		route.Polygons = append(route.Polygons, part.Polygons...)
	}

	return route, nil
}
