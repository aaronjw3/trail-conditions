# Trail Conditions

Explore hourly weather forecasts along a GPX, KML, or KMZ route. Upload a hiking or biking route, choose an hour, and compare temperature, precipitation, and wind at sampled locations on a map.

An independent portfolio project demonstrating Go integration with the [Open-Meteo forecast API](https://open-meteo.com/en/docs), route-format normalization into GeoJSON, complete snapshot publication, and a server-rendered HTMX interface. Contains no employer code, schemas, or data.

## Run with Docker Compose

Requires Docker Desktop or Docker Engine with Compose. The app uses Mapbox for its map and Open-Meteo for forecasts. Set your public Mapbox token in your terminal; no weather API key is needed.

In zsh, enter the token at a hidden prompt:

```zsh
read -r -s 'MAPBOX_PUBLIC_TOKEN?Paste your public Mapbox token, then press Enter: '
export MAPBOX_PUBLIC_TOKEN
docker compose up --build
```

Open **http://127.0.0.1:8080**. Upload a GPX, KML, or KMZ route or select **Try the foothills example**. The example is synthetic geometry near Bozeman, not a navigable trail. Selecting an hour or map color uses the current snapshot; only uploads and explicit refreshes request new weather.

Compose starts two services:

- `app`: the Go application, published at `127.0.0.1:8080`.
- `db`: PostgreSQL 18, published at `127.0.0.1:5433` for local development. The app reaches it at `db:5432` on the Compose network.

Set `TRAIL_DB_PORT` to choose another host database port:

```sh
export TRAIL_DB_PORT=55432
docker compose up -d --wait db
export DATABASE_URL="postgres://trail:trail_local@127.0.0.1:${TRAIL_DB_PORT}/trail?sslmode=disable"
export TEST_DATABASE_URL="$DATABASE_URL"
```

Keep that variable exported for subsequent Compose commands, or save it in your local `.env`. The app container always connects to `db:5432`; this setting only changes access from the host. Native execution and tests must use the selected host port.

The app starts after PostgreSQL passes its readiness check. It creates its `current_snapshot` table before serving requests. PostgreSQL stores data in the named `postgres-data` volume, mounted at `/var/lib/postgresql`. Stop with Ctrl+C, then `docker compose down` to remove containers and the network; the volume remains. `docker compose up` reloads the stored snapshot. Stop an existing native or standalone-container copy first to free port 8080.

Compose uses the local-development database credentials `trail` / `trail_local`. The services are published only on loopback. This remains a single-user demonstration, not an authenticated public deployment. Run one app instance per database: its active snapshot and refresh lock are process-local.

The image uses a Go build stage and a non-root distroless runtime. Templates and browser assets are embedded in the binary. The runtime contains CA certificates for outbound HTTPS but no shell or Go compiler. `.env` files and local data are excluded from the build context. Tokens are passed at runtime, not during image creation.

To inspect the stored row:

```sh
docker compose exec db psql -U trail -d trail -c \
  'SELECT retrieved_at, jsonb_array_length(forecasts) AS sample_count FROM current_snapshot;'
```

### Token configuration

Use a public `pk.` Mapbox token, never a secret `sk.` token. The server exposes the public token through `/api/map-config` because Mapbox runs in the browser. Configure token URL restrictions to include `http://127.0.0.1:8080` for local use. The map has Satellite and Streets options.

For persistence across terminal sessions, save `export MAPBOX_PUBLIC_TOKEN='your-public-token'` in the project's ignored `.env` file using your editor. Run `source .env` before starting the app. Never commit the file. Native Go execution does not automatically load `.env`.

The browser loads pinned HTMX and Mapbox versions from CDNs. Mapbox receives map requests for the viewed area. Open-Meteo receives sampled coordinates, not the original uploaded file. The normalized route remains in PostgreSQL until replaced. Forecasts represent modeled conditions at sampled locations, not precise conditions throughout a mountain route. Open-Meteo's free API is for noncommercial use under its [terms](https://open-meteo.com/en/terms).

### Run Go natively with a Docker database

Requires Go 1.25 or later:

```sh
docker compose up -d --wait db
export DATABASE_URL='postgres://trail:trail_local@127.0.0.1:5433/trail?sslmode=disable'
# Export MAPBOX_PUBLIC_TOKEN as described above.
go run .
```

`DATABASE_URL` is required. Native execution defaults to `127.0.0.1:8080`; `TRAIL_LISTEN_ADDR` overrides it. The container sets that address to `0.0.0.0:8080` internally; Compose restricts host access with its loopback port mapping. No PostGIS extension is required.

## Architecture

The application uses HTTP, multipart uploads, HTML, and JSON. It does **not** use protobuf or gRPC. Clean Architecture here is about dependency direction and responsibility, not the transport protocol.

```text
main.go                           Composition: settings, dependencies, server lifecycle
internal/domain/                  Route, forecast, snapshot, validation and sampling
internal/application/             Import/refresh workflows and dependency contracts
internal/adapters/geoimport/       GPX, KML, and KMZ decoding
internal/adapters/openmeteo/       Provider HTTP requests and response normalization
internal/adapters/postgres/        PostgreSQL schema, loading, and transactional upsert
internal/adapters/httpui/          HTTP handlers and HTML presentation
web/                              HTMX templates, CSS, Mapbox JavaScript
```

The domain imports no adapters or application code. The application depends on domain types and its own `RouteDecoder`, `Weather`, and `SnapshotStore` contracts. It does not import HTTP, XML, ZIP, or database driver APIs. Concrete adapters satisfy those contracts and are wired in `main.go`.

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
6. Upsert the complete snapshot into PostgreSQL in one atomic statement. Publish it to in-memory readers only after the database acknowledges success. A failed collection never writes to the database. A rejected SQL statement leaves the previous row intact. If the connection fails after PostgreSQL commits but before acknowledgement reaches the app, the stored result may be newer than the in-memory result; restarting reloads the database state.
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

## PostgreSQL storage

`current_snapshot` holds one row:

| Column | Type | Purpose |
| --- | --- | --- |
| `singleton` | boolean primary key, constrained to true | Enforces one current snapshot |
| `retrieved_at` | timestamptz | Time the complete forecast was collected |
| `route` | jsonb | Normalized GeoJSON MultiLineString |
| `forecasts` | jsonb | Forecast locations and their hourly values |

The PostgreSQL adapter uses pgx. Its embedded `schema.sql` creates the table if absent at startup; this is initial schema creation, not a versioned migration framework. Save uses one parameterized `INSERT ... ON CONFLICT DO UPDATE`. The application's storage contract and forecast workflow do not depend on SQL or pgx.

Ordinary PostgreSQL suffices because the app reads and replaces whole snapshots. It does not run spatial predicates or use spatial indexes. Geometry is stored as GeoJSON JSONB, not a PostGIS `geometry` column.

## Tests

Start the local database, then run the full suite:

```sh
docker compose up -d --wait db
export TEST_DATABASE_URL='postgres://trail:trail_local@127.0.0.1:5433/trail?sslmode=disable'
go test -race -coverpkg=./... -coverprofile=coverage.out ./...
go tool cover -func=coverage.out
go vet ./...
go build ./...
node --check --input-type=module < web/app.js
node --test map.test.cjs
```

Database tests create uniquely named temporary schemas and drop them afterward. They require schema-creation permission and do not alter the application's `public.current_snapshot`. They verify initial creation, upserts, reloads, failed-statement preservation, invalid stored data, cancellation, concurrent refresh exclusion, and GPX/KML/KMZ uploads through the HTTP handler to stored and rendered snapshots. Without `TEST_DATABASE_URL`, those integration tests explicitly skip; importer, provider-response, and map-configuration tests still run. CI supplies PostgreSQL so database tests run there.

HTTP handlers use `httptest` recorders and provider responses use a controlled HTTP transport. PostgreSQL is real in database tests. These tests do not establish live Open-Meteo compatibility, DNS/TLS behavior, browser interactions, or forecast accuracy. The JavaScript regression test uses controlled DOM and Mapbox boundaries to verify layer restoration after changing basemaps; it does not test WebGL rendering.
