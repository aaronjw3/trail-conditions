// HTMX owns requests and HTML updates. This module owns the persistent map.
const status = document.querySelector('#status');
let map;
let routeKey;
let pending;
const scales = {
  temperature: {low: -10, high: 30, unit: '°C', colors: ['#386cb0', '#e3923c']},
  precipitation: {low: 0, high: 10, unit: 'mm/h', colors: ['#c3ded9', '#24539b']},
  wind: {low: 0, high: 60, unit: 'km/h', colors: ['#c7dc9b', '#9a497a']}
};
function updateMap() {
  const element = document.querySelector('#map-data');
  if (!element) return;
  pending = JSON.parse(element.textContent);
  if (!map?.isStyleLoaded() || !map.getSource('samples')) return;
  const {snapshot, hour, metric} = pending;
  const scale = scales[metric];
  const points = {type: 'FeatureCollection', features: snapshot.forecasts.map((f, i) => ({
    type: 'Feature', geometry: {type: 'Point', coordinates: f.point},
    properties: {value: f[metric][hour], label: String(i + 1)}
  }))};
  map.getSource('route').setData({type: 'Feature', geometry: snapshot.route, properties: {}});
  map.getSource('samples').setData(points);
  map.setPaintProperty('samples', 'circle-color', ['interpolate', ['linear'], ['get', 'value'], scale.low, scale.colors[0], scale.high, scale.colors[1]]);
  const key = JSON.stringify(snapshot.route);
  if (key !== routeKey) {
    const coords = snapshot.route.coordinates.flat();
    const bounds = coords.reduce((b, p) => b.extend(p), new window.maplibregl.LngLatBounds(coords[0], coords[0]));
    map.fitBounds(bounds, {padding: 65, maxZoom: 14});
    routeKey = key;
  }
  document.querySelector('#sample-count').textContent = `${points.features.length} forecast locations`;
  document.querySelector('#low').textContent = `${scale.low} ${scale.unit}`;
  document.querySelector('#high').textContent = `${scale.high} ${scale.unit}`;
  document.querySelector('#gradient').style.background = `linear-gradient(to right, ${scale.colors.join(',')})`;
}
document.body.addEventListener('htmx:afterSwap', event => {
  if (event.detail.target.id !== 'panel') return;
  status.classList.remove('error');
  status.textContent = document.querySelector('#map-data') ? 'Showing the last complete forecast.' : 'Choose a route to get started.';
  updateMap();
});
document.body.addEventListener('htmx:responseError', event => {
  status.classList.add('error');
  status.textContent = event.detail.xhr.responseText || 'Request failed. Your previous forecast is unchanged.';
});
document.body.addEventListener('htmx:sendError', () => {
  status.classList.add('error');
  status.textContent = 'Cannot reach the local server. Your displayed forecast has not been refreshed.';
});
document.body.addEventListener('click', event => {
  const card = event.target.closest('.sample');
  if (card && map) map.flyTo({center: [Number(card.dataset.longitude), Number(card.dataset.latitude)], zoom: 13});
});
try {
  const module = await import('https://unpkg.com/maplibre-gl@6.18.0/dist/maplibre-gl.mjs');
  const maplibregl = module.default || module;
  window.maplibregl = maplibregl;
  map = new maplibregl.Map({container: 'map', style: 'https://demotiles.maplibre.org/style.json', center: [-110.98, 45.71], zoom: 9});
  map.addControl(new maplibregl.NavigationControl(), 'top-right');
  map.on('error', () => {
    const error = document.querySelector('#map-error');
    error.hidden = false;
    error.textContent = 'Some map resources could not load. Forecast readings remain available below.';
  });
  map.on('load', () => {
    map.addSource('route', {type: 'geojson', data: {type: 'FeatureCollection', features: []}});
    map.addSource('samples', {type: 'geojson', data: {type: 'FeatureCollection', features: []}});
    map.addLayer({id: 'route-halo', type: 'line', source: 'route', paint: {'line-color': '#fff', 'line-width': 7}});
    map.addLayer({id: 'route', type: 'line', source: 'route', paint: {'line-color': '#285b48', 'line-width': 3}});
    map.addLayer({id: 'samples', type: 'circle', source: 'samples', paint: {'circle-radius': 8, 'circle-color': '#e3923c', 'circle-stroke-color': '#fff', 'circle-stroke-width': 2}});
    updateMap();
  });
} catch {
  const error = document.querySelector('#map-error');
  error.hidden = false;
  error.textContent = 'Map unavailable. Check your connection or WebGL support. Forecast readings still work below.';
}
