// HTMX owns requests and HTML updates. This module owns the persistent map.
const status = document.querySelector('#status');
let map;
let fitted = false;
const geometryElement = document.querySelector('#route-data');
const geometry = geometryElement ? JSON.parse(geometryElement.textContent) : {};
const {points: waypoints = [], lines = [], polygons = []} = geometry;
const routeData = {type: 'FeatureCollection', features: [
  ...waypoints.map(coordinates => ({type: 'Feature', geometry: {type: 'Point', coordinates}, properties: {}})),
  ...lines.map(coordinates => ({type: 'Feature', geometry: {type: 'LineString', coordinates}, properties: {}})),
  ...polygons.map(coordinates => ({type: 'Feature', geometry: {type: 'Polygon', coordinates}, properties: {}}))
]};
const scales = {
  temperature: {low: -10, high: 30, unit: '°C', colors: ['#386cb0', '#e3923c']},
  precipitation: {low: 0, high: 10, unit: 'mm/h', colors: ['#c3ded9', '#24539b']},
  wind: {low: 0, high: 60, unit: 'km/h', colors: ['#c7dc9b', '#9a497a']}
};
function updateMap() {
  const element = document.querySelector('#map-data');
  if (!element) return;
  const {samples, metric} = JSON.parse(element.textContent);
  if (!map?.getSource('samples')) return;
  const scale = scales[metric];
  const points = {type: 'FeatureCollection', features: samples.map((f, i) => ({
    type: 'Feature', geometry: {type: 'Point', coordinates: f.point},
    properties: {value: f.value, label: String(i + 1)}
  }))};
  map.getSource('samples').setData(points);
  map.setPaintProperty('samples', 'circle-color', ['interpolate', ['linear'], ['get', 'value'], scale.low, scale.colors[0], scale.high, scale.colors[1]]);
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
  const configResponse = await fetch('/api/map-config');
  if (!configResponse.ok) throw new Error(await configResponse.text());
  const {token} = await configResponse.json();
  if (!window.mapboxgl) throw new Error('Mapbox could not load. Check your connection.');
  map = new mapboxgl.Map({container: 'map', accessToken: token, style: 'mapbox://styles/mapbox/standard-satellite', center: [-121.735, 46.82], zoom: 10});
  map.addControl(new mapboxgl.NavigationControl(), 'top-right');
  document.querySelector('#basemap').addEventListener('change', event => {
    map.setStyle(`mapbox://styles/mapbox/${event.target.value}`);
  });
  map.on('error', () => {
    const error = document.querySelector('#map-error');
    error.hidden = false;
    error.textContent = 'Some map resources could not load. Forecast readings remain available below.';
  });
  map.on('style.load', () => {
    // Changing styles removes custom sources and layers; recreate them each time.
    document.querySelector('#map-error').hidden = true;
    map.addSource('route', {type: 'geojson', data: routeData});
    map.addSource('samples', {type: 'geojson', data: {type: 'FeatureCollection', features: []}});
    map.addLayer({id: 'areas', type: 'fill', source: 'route', slot: 'top', filter: ['==', ['geometry-type'], 'Polygon'], paint: {'fill-color': '#285b48', 'fill-opacity': 0.18}});
    map.addLayer({id: 'route-halo', type: 'line', source: 'route', slot: 'top', paint: {'line-color': '#fff', 'line-width': 7}});
    map.addLayer({id: 'route', type: 'line', source: 'route', slot: 'top', paint: {'line-color': '#285b48', 'line-width': 3}});
    map.addLayer({id: 'waypoints', type: 'circle', source: 'route', slot: 'top', filter: ['==', ['geometry-type'], 'Point'], paint: {'circle-radius': 5, 'circle-color': '#285b48', 'circle-stroke-color': '#fff', 'circle-stroke-width': 2}});
    map.addLayer({id: 'samples', type: 'circle', source: 'samples', slot: 'top', paint: {'circle-radius': 8, 'circle-color': '#e3923c', 'circle-stroke-color': '#fff', 'circle-stroke-width': 2}});
    if (!fitted && routeData.features.length) {
      const coords = [...waypoints, ...lines.flat(), ...polygons.flat(2)];
      const bounds = coords.reduce((b, p) => b.extend(p), new window.mapboxgl.LngLatBounds(coords[0], coords[0]));
      map.fitBounds(bounds, {padding: 65, maxZoom: 14});
      fitted = true;
    }
    updateMap();
  });
} catch (cause) {
  const error = document.querySelector('#map-error');
  error.hidden = false;
  error.textContent = `${cause.message || 'Map unavailable.'} Forecast readings still work below.`;
}
