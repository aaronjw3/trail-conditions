# Trail Conditions

Explore hourly weather forecasts along a GPX, KML, or KMZ route. Upload a hiking or biking route, choose an hour, and compare temperature, precipitation, and wind at sampled locations on a map.

An independent portfolio project demonstrating Go integration with the [Open-Meteo forecast API](https://open-meteo.com/en/docs), route-format normalization into GeoJSON, complete snapshot publication, and a server-rendered HTMX interface. Contains no employer code, schemas, or data.

## Run

Requires Go 1.25 or later and internet access. A Mapbox public token is required for the map. Open-Meteo needs no API key. No database or npm build is required. Docker is an alternative to installing Go.

In zsh, enter the token at a hidden prompt (it will not enter shell history):

```zsh
read -r -s 'MAPBOX_PUBLIC_TOKEN?Paste your public Mapbox token, then press Enter: '
export MAPBOX_PUBLIC_TOKEN
go run .
```

The variable lasts for this terminal session. Use a public `pk.` token, never a secret `sk.` token. The server exposes only a public token through `/api/map-config` because Mapbox runs in the browser. Configure URL restrictions for your app, including `http://127.0.0.1:8080` for local use. Restart the server after changing the environment. The app does not automatically load `.env` files.

To keep the token across terminal sessions, create the already-ignored `.env` locally in your editor containing `export MAPBOX_PUBLIC_TOKEN='your-public-token'`, then run `source .env` before `go run .`. Do not commit the file.

Open **http://127.0.0.1:8080**. Upload a GPX, KML, or KMZ file or select **Try the foothills example**. The included example is synthetic geometry near Bozeman, not a navigable trail. Choosing a different hour or map color reads the existing snapshot; only an upload, example request, or explicit refresh calls the weather API.

When run natively, the app binds to loopback and is designed for one local user. It stores one complete route/forecast snapshot in `data/snapshot.json` and reloads it on restart. Set `TRAIL_DATA_DIR` to change that directory. Run only one process per data directory.

The browser loads pinned HTMX and Mapbox versions from CDNs and uses Mapbox Standard Satellite or Standard Streets. These services need network access. The map style selector switches between satellite imagery and a street map while retaining the route and forecast samples. Open-Meteo receives sample coordinates; the full GPX file is not sent to it. Uploaded routes remain in local snapshot storage until replaced or deleted. Map requests reveal the viewed region to the map provider.

Open-Meteo's free API is for noncommercial use under its [terms](https://open-meteo.com/en/terms). Weather data attribution is displayed in the app. Forecast values represent modeled conditions at sampled locations, not precise conditions throughout a mountain route.

## Docker

Requires Docker Desktop or Docker Engine. Build from the repository root:

```sh
docker build -t trail-conditions .
```

Export `MAPBOX_PUBLIC_TOKEN` in your terminal as described above, then run:

```sh
docker run --rm --name trail-conditions \
  -p 127.0.0.1:8080:8080 \
  -e MAPBOX_PUBLIC_TOKEN \
  -v trail-conditions-data:/data \
  trail-conditions
```

Open **http://127.0.0.1:8080**. Stop with Ctrl+C or `docker stop trail-conditions`. The named volume preserves the snapshot when the container is removed; running the command again reloads it. Stop any locally running copy first to free port 8080. To use another host port, change the left-hand port, for example `-p 127.0.0.1:8081:8080`, and allow that URL in your Mapbox token restrictions.

The image compiles the Go binary in a build stage and runs it as UID 65532 in a distroless runtime with CA certificates for outbound HTTPS. Templates and browser assets are embedded in the binary. The runtime has no shell or Go compiler. Credentials and local snapshots are excluded from the build context. No token is needed during the build; it is passed only when running the container.

The container listens on `0.0.0.0:8080` internally. The documented port mapping exposes it only on the host's loopback interface. Native execution defaults to `127.0.0.1:8080`; `TRAIL_LISTEN_ADDR` overrides the address. This is still a single-user application without authentication. Use only one process/container per snapshot volume.

## Architecture

The application uses HTTP, multipart uploads, HTML, and JSON. It does **not** use protobuf or gRPC. Clean Architecture here is about dependency direction and responsibility, not the transport protocol.

```text
main.go                           Composition: settings, dependencies, server lifecycle
internal/domain/                  Route, forecast, snapshot, validation and sampling
internal/application/             Import/refresh workflows and dependency contracts
internal/adapters/geoimport/       GPX, KML, and KMZ decoding
internal/adapters/openmeteo/       Provider HTTP requests and response normalization
internal/adapters/snapshotfile/    Snapshot loading and atomic file publication
internal/adapters/httpui/          HTTP handlers and HTML presentation
web/                              HTMX templates, CSS, Mapbox JavaScript
```

The domain imports no adapters or application code. The application depends on domain types and its own `RouteDecoder`, `Weather`, and `SnapshotStore` contracts. It does not import HTTP, XML, ZIP, or filesystem APIs. Concrete adapters satisfy those contracts and are wired in `main.go`.

The import workflow accepts a filename and bytes, decodes a domain route, validates and samples it, collects complete forecasts, persists the snapshot, and publishes it for readers. Refresh uses the stored route through the same collection workflow. Concurrency exclusion belongs to the application service, so it applies independently of HTTP. Snapshot readers receive copies rather than mutable access to published state.

Format dispatch is isolated in `geoimport.Decoder.Decode`. A format adapter must return WGS84 line segments in `domain.Route`; parsing errors must be explicit. Adding a decoder and its dispatch case does not require changing forecast collection or persistence. The upload control's accepted extensions must also match supported formats. There is no `.proto` file or protobuf format enum to extend.

## Supported imports

| Format | Supported route geometry |
| --- | --- |
| GPX | Track segments (`trkseg`) and routes (`rte`) |
| KML | `LineString`, including nested folders and `MultiGeometry`; `gx:Track` and `gx:MultiTrack` |
| KMZ | KML documents inside a ZIP archive, including nested archive paths |

All formats preserve separate segments and normalize to longitude/latitude GeoJSON. Elevation, timestamps, names, and styles are not retained in the route model. This is a route importer, not a general-purpose geospatial converter.

KML point, polygon, ring, model, overlay, and network-link content is rejected, including mixed documents containing otherwise valid lines. Missing `gx:coord` values are rejected rather than interpolated. Remote resources are never fetched. KML 2.2 namespaces and unqualified KML are accepted.

KMZ imports every `.kml` member in archive order instead of silently selecting only the first one. Each must contain supported route geometry. Other assets are ignored. Nothing is extracted to disk. Archives are limited to 64 entries and 8 MiB total expanded KML content; malformed members fail the whole import. This intentionally does not implement Google Earth's linked-document or asset semantics.

## Data flow

1. Decode an upload of at most 2 MiB. Validate a maximum of 20,000 points across 1–6 segments, each with at least two points and nonzero length. Reject invalid coordinates. Standalone waypoints do not define a route.
2. Preserve segment boundaries in a GeoJSON `MultiLineString`. Never connect disconnected tracks.
3. Select at most 12 original GPS points, allocating sample slots equally across segments and placing interior targets by cumulative great-circle distance. Always retain each segment's endpoints. Sparse input can yield fewer samples; this is not spatial interpolation.
4. Request three days of hourly temperature (°C), precipitation (mm), and wind speed (km/h) from Open-Meteo for each sample. Requests use UTC timestamps and explicit units.
5. Require 72 aligned hourly readings, expected units, and no missing values for every sample. A forecast date change between requests fails the refresh. This checks structural completeness, not simultaneous provider-model versions.
6. Write the complete snapshot to a temporary file, sync it, and rename it in the same directory. Publish it to in-memory readers only after persistence succeeds. A failed collection retains the previous snapshot and timestamp. This does not claim durability against every filesystem or power-loss scenario.
7. Render HTML on the server and update the map from the selected snapshot.

Precipitation is the model's hourly accumulation; temperature and wind are hourly readings. All forecast times in the interface are UTC.

## HTMX and JavaScript responsibilities

Go's `html/template` renders forecast controls, timestamps, and reading cards. HTMX handles multipart route uploads, refresh POSTs, and hour/metric GETs, replacing the forecast panel with returned HTML. There is no custom JavaScript fetch layer or client-side rendering loop for those controls and cards.

Mapbox owns a persistent WebGL map. `web/app.js` updates GeoJSON sources and paint properties after HTMX swaps, fits newly uploaded routes, and handles sample-card map navigation. It also displays request errors. The map is outside the swapped panel so changing the selected hour does not recreate it.

HTMX reduces application-authored request and DOM-update code; it does not eliminate JavaScript or make the map server-rendered. This small app uses Go standard-library templates, not a frontend framework or generated template code.

## Reliability boundaries

- One refresh at a time; overlapping writes return HTTP 409.
- Total collection deadline: 90 seconds. Per-request timeout: 15 seconds.
- HTTP 502/503/504 get at most three attempts, with bounded waits. A numeric `Retry-After` up to five seconds is honored. Other statuses, including 429, fail for an explicit later retry. Transport failures are not automatically retried.
- Provider responses are limited to 1 MiB. Redirects are rejected.
- Forecast retrieval is sequential and bounded to 12 locations. This is a small local tool, not a distributed job system.
- Failed uploads or refreshes do not replace the displayed forecast. Its retrieval timestamp remains visible; stored forecasts are not automatically refreshed.
- State-changing routes require the HTMX request header and do not enable cross-origin access. This is not user authentication; do not expose this local server as a public service.

## Endpoints

| Endpoint | Behavior |
| --- | --- |
| `POST /forecast` | Multipart GPX/KML/KMZ field `route`; returns the rendered forecast panel |
| `POST /example` | Collects forecasts for the bundled synthetic route |
| `POST /refresh` | Refreshes the currently published sample locations |
| `GET /panel?hour=0&metric=temperature` | Renders a selected snapshot hour; metric also accepts `precipitation` or `wind` |
| `GET /api/map-config` | Returns the configured public Mapbox token; rejects missing or non-public tokens |
| `GET /api/snapshot` | Returns the complete stored snapshot as JSON, or `null` |
| `GET /example.gpx` | Downloads the synthetic example |

POST routes require `HX-Request: true`.

## Tests

```sh
go test -race -coverpkg=./... -coverprofile=coverage.out ./...
go tool cover -func=coverage.out
go vet ./...
go build ./...
node --check --input-type=module < web/app.js
node --test map.test.cjs
```

Tests protect GPX/KML/KMZ segment preservation, archive bounds, coordinate validation, forecast completeness and units, bounded retry behavior, cancellation before publication, preservation of the last complete snapshot, multipart uploads, and server-rendered HTMX responses.

HTTP handlers run through `httptest` request/response recorders. Provider responses use a controlled HTTP transport. These tests exercise real parsing, rendering, and filesystem publication, but do not establish live API compatibility, DNS/TLS behavior, browser interactions, or forecast accuracy.

The JavaScript regression test uses controlled DOM and Mapbox boundaries to verify that switching basemaps restores route and forecast layers without resetting the viewport. It does not verify WebGL rendering or live Mapbox resources.
