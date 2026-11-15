# Trail Conditions

Upload a GPX, KML, or KMZ route and explore three days of hourly temperature, precipitation, and wind forecasts on a Mapbox map.

An independent portfolio project demonstrating Go integration with the [Open-Meteo API](https://open-meteo.com/en/docs), geospatial imports, PostgreSQL persistence, and a server-rendered HTMX interface. Contains no employer code or data.

## Run with Docker

Requires Docker with Compose. If you don't already have a `.env` file:

```sh
cp .env.example .env
```

Set `MAPBOX_PUBLIC_TOKEN` in `.env` to a public Mapbox `pk.` token. Allow `http://127.0.0.1:8080` in its URL restrictions. No Open-Meteo API key is required. Git ignores `.env`.

```sh
docker compose up -d --build
```

Open **http://127.0.0.1:8080**. Upload a route or try the bundled synthetic example near Bozeman. The map supports Satellite and Streets views.

Compose starts the Go app on port `8080` and PostgreSQL on the host port set by `TRAIL_DB_PORT` (`55432` in `.env.example`). Change that setting if the port is occupied. The app connects to PostgreSQL at `db:5432` inside Docker. Exported shell variables override `.env` values.

```sh
docker compose logs -f app   # Follow application logs
docker compose stop         # Stop the app and database
docker compose start        # Restart existing containers
```

Use `docker compose up -d --build` after code changes; restarting alone does not rebuild the image. Saved data persists in the `postgres-data` volume.

### Run Go locally

Requires Go 1.25 or later and the same `.env` configuration. Stop the containerized app first if it occupies port 8080.

```sh
set -a
source .env
set +a
docker compose up -d --wait db
go run .
```

## Architecture

```text
main.go                       Configuration and dependency wiring
internal/domain/              Route, forecast, sampling, and validation
internal/application/         Import/refresh workflows and adapter contracts
internal/adapters/geoimport/   GPX, KML, and KMZ decoding
internal/adapters/openmeteo/   Weather API client
internal/adapters/postgres/    Snapshot storage
internal/adapters/httpui/      HTTP handlers and HTML rendering
web/                          Templates, CSS, and Mapbox JavaScript
```

The application depends on domain types and contracts for decoding, weather, and storage. Adapters implement those contracts; `main.go` wires them together. Communication uses HTTP, HTML, and JSON.

Uploads are decoded into GeoJSON line segments and sampled at up to 12 locations. The app fetches 72 hourly readings for every location, saves the complete snapshot, then publishes it to the UI. PostgreSQL stores the route and forecasts as JSONB; PostGIS is not required.

HTMX handles uploads, refreshes, and forecast selections through server-rendered HTML. JavaScript manages the persistent Mapbox map, updates its sources and styling, and displays request errors. Changing the selected hour or metric uses the saved forecast without requesting new weather.

## Imports and limits

| Format | Supported geometry |
| --- | --- |
| GPX | Track segments and routes |
| KML | LineString, MultiGeometry, gx:Track, and gx:MultiTrack |
| KMZ | Supported KML documents inside a ZIP archive |

- Uploads: at most 2 MiB, 20,000 points, and 6 segments. Separate segments remain separate; elevation, timestamps, and styles are not retained.
- KMZ: read in memory without extracting files to disk; at most 64 entries and 8 MiB of expanded KML. Non-KML assets are ignored. Unsupported geometry and remote KML links are rejected.
- Weather: sequential requests, a 15-second timeout per attempt, and a 90-second deadline for the whole operation. Temporary connection failures and HTTP 502/503/504 share a three-attempt retry budget with bounded waits.
- Only one upload or refresh runs at a time. Failed weather collection preserves the previous complete snapshot. Forecasts refresh only on upload or explicit request; displayed times are UTC.

This is a single-user local demo: run one app instance per database. Open-Meteo receives sampled coordinates, while the route is stored locally in PostgreSQL. Forecasts describe modeled conditions at sample locations, not precise conditions throughout a mountain route. The bundled route is synthetic, not a navigation track. See Open-Meteo's [usage terms](https://open-meteo.com/en/terms).

## Tests

Adapter tests live beside their implementations, cross-layer PostgreSQL tests in `tests/integration`, and map behavior tests in `tests/web`.

Run without a database:

```sh
go test -race ./...
node --test tests/web/*.test.cjs
```

Run all Go tests, including integration tests:

```sh
set -a
source .env
set +a
docker compose up -d --wait db
go test -race -count=1 -tags=integration ./...
```

Integration tests require `TEST_DATABASE_URL` and create disposable schemas without changing the application's saved snapshot. Weather responses are controlled in tests; a local TLS server exercises connection recovery. Map tests use controlled DOM and Mapbox objects, not a real browser.

[GitHub Actions](.github/workflows/check.yml) runs formatting, static analysis, race-enabled tests, coverage reporting, JavaScript checks, and Go/Docker builds.
