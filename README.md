# Trail Conditions

Explore hourly weather forecasts along a GPX route. Upload a hiking or biking route, choose an hour, and compare temperature, precipitation, and wind at sampled locations on a map.

An independent portfolio project demonstrating Go integration with the [Open-Meteo forecast API](https://open-meteo.com/en/docs), GPX-to-GeoJSON processing, complete snapshot publication, and a server-rendered HTMX interface. Contains no employer code, schemas, or data.

## Run

Requires Go 1.25 or later and internet access. A Mapbox public token is required for the map. Open-Meteo needs no API key. No database, npm build, or containers are required.

In zsh, enter the token at a hidden prompt (it will not enter shell history):

```zsh
read -r -s 'MAPBOX_PUBLIC_TOKEN?Paste your public Mapbox token, then press Enter: '
export MAPBOX_PUBLIC_TOKEN
go run .
```

The variable lasts for this terminal session. Use a public `pk.` token, never a secret `sk.` token. The server exposes only a public token through `/api/map-config` because Mapbox runs in the browser. Configure URL restrictions for your app, including `http://127.0.0.1:8080` for local use. Restart the server after changing the environment. The app does not automatically load `.env` files.

To keep the token across terminal sessions, create the already-ignored `.env` locally in your editor containing `export MAPBOX_PUBLIC_TOKEN='your-public-token'`, then run `source .env` before `go run .`. Do not commit the file.

Open **http://127.0.0.1:8080**. Upload a GPX file or select **Try the foothills example**. The included example is synthetic geometry near Bozeman, not a navigable trail. Choosing a different hour or map color reads the existing snapshot; only an upload, example request, or explicit refresh calls the weather API.

The app binds to loopback and is designed for one local user. It stores one complete route/forecast snapshot in `data/snapshot.json` and reloads it on restart. Set `TRAIL_DATA_DIR` to change that directory. Run only one process per data directory.

The browser loads pinned HTMX and Mapbox versions from CDNs and uses Mapbox Standard Satellite or Standard Streets. These services need network access. The map style selector switches between satellite imagery and a street map while retaining the route and forecast samples. Open-Meteo receives sample coordinates; the full GPX file is not sent to it. Uploaded routes remain in local snapshot storage until replaced or deleted. Map requests reveal the viewed region to the map provider.

Open-Meteo's free API is for noncommercial use under its [terms](https://open-meteo.com/en/terms). Weather data attribution is displayed in the app. Forecast values represent modeled conditions at sampled locations, not precise conditions throughout a mountain route.

## Data flow

1. Parse a GPX upload of at most 2 MiB and 20,000 points. Accept 1–6 track segments or routes, each with at least two points and nonzero length. Reject invalid coordinates. Standalone waypoints do not define a route.
2. Preserve segment boundaries in a GeoJSON `MultiLineString`. Never connect disconnected tracks.
3. Select at most 12 original GPS points, allocating sample slots equally across segments and placing interior targets by cumulative great-circle distance. Always retain each segment's endpoints. Sparse input can yield fewer samples; this is not spatial interpolation.
4. Request three days of hourly temperature (°C), precipitation (mm), and wind speed (km/h) from Open-Meteo for each sample. Requests use UTC timestamps and explicit units.
5. Require 72 aligned hourly readings, expected units, and no missing values for every sample. A forecast date change between requests fails the refresh. This checks structural completeness, not simultaneous provider-model versions.
6. Write the complete snapshot to a temporary file, sync it, and rename it in the same directory. Publish it to in-memory readers only after persistence succeeds. A failed collection retains the previous snapshot and timestamp. This does not claim durability against every filesystem or power-loss scenario.
7. Render HTML on the server and update the map from the selected snapshot.

Precipitation is the model's hourly accumulation; temperature and wind are hourly readings. All forecast times in the interface are UTC.

## HTMX and JavaScript responsibilities

Go's `html/template` renders forecast controls, timestamps, and reading cards. HTMX handles multipart GPX uploads, refresh POSTs, and hour/metric GETs, replacing the forecast panel with returned HTML. There is no custom JavaScript fetch layer or client-side rendering loop for those controls and cards.

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
| `POST /forecast` | Multipart GPX field `route`; returns the rendered forecast panel |
| `POST /example` | Collects forecasts for the bundled synthetic route |
| `POST /refresh` | Refreshes the currently published sample locations |
| `GET /panel?hour=0&metric=temperature` | Renders a selected snapshot hour; metric also accepts `precipitation` or `wind` |
| `GET /api/map-config` | Returns the configured public Mapbox token; rejects missing or non-public tokens |
| `GET /api/snapshot` | Returns the complete stored snapshot as JSON, or `null` |
| `GET /example.gpx` | Downloads the synthetic example |

POST routes require `HX-Request: true`.

## Tests

```sh
go test -race -cover ./...
go vet ./...
go build ./...
node --check --input-type=module < web/app.js
node --test map.test.cjs
```

Tests protect GPX segment preservation, coordinate validation, forecast completeness and units, bounded retry behavior, cancellation before publication, preservation of the last complete snapshot, multipart uploads, and server-rendered HTMX responses.

HTTP handlers run through `httptest` request/response recorders. Provider responses use a controlled HTTP transport. These tests exercise real parsing, rendering, and filesystem publication, but do not establish live API compatibility, DNS/TLS behavior, browser interactions, or forecast accuracy.

The JavaScript regression test uses controlled DOM and Mapbox boundaries to verify that switching basemaps restores route and forecast layers without resetting the viewport. It does not verify WebGL rendering or live Mapbox resources.
