const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const vm = require('node:vm');

// Controlled Mapbox and DOM boundaries verify our style-switch lifecycle.
// This does not test Mapbox rendering, WebGL, or live styles.
test('style changes restore route and selected forecast without resetting the viewport', async () => {
  const snapshot = {
    route: {points: [[-121.8, 46.9]], lines: [[[-121.7, 46.8], [-121.75, 46.85]]], polygons: [[[[-122, 46], [-121, 46], [-121, 47], [-122, 46]], [[-121.8, 46.2], [-121.6, 46.2], [-121.6, 46.3], [-121.8, 46.2]]]]},
    forecasts: [{point: [-121.7, 46.8], temperature: [4], precipitation: [0], wind: [12]}]
  };
  const elements = new Map();
  for (const id of ['#status', '#route-data', '#map-data', '#sample-count', '#low', '#high', '#gradient', '#basemap', '#map-error']) {
    elements.set(id, {textContent: '', style: {}, classList: {add() {}, remove() {}}, addEventListener(name, fn) {this[name] = fn;}});
  }
  elements.get('#route-data').textContent = JSON.stringify(snapshot.route);
  elements.get('#map-data').textContent = JSON.stringify({samples: [{point: snapshot.forecasts[0].point, value: 12}], metric: 'wind'});
  const bodyEvents = {};
  let instance;
  class FakeMap {
    constructor() {instance = this; this.sources = {}; this.layers = []; this.events = {}; this.fits = 0;}
    addControl() {}
    on(name, fn) {this.events[name] = fn;}
    getSource(name) {return this.sources[name];}
    addSource(name, options) {this.sources[name] = {data: options.data, updates: 0, setData(data) {this.data = data; this.updates++;}};}
    addLayer(layer) {this.layers.push(layer.id);}
    setPaintProperty(id, property, value) {this.paint = value;}
    fitBounds() {this.fits++;}
    setStyle(style) {this.style = style; this.sources = {}; this.layers = []; this.events['style.load']();}
  }
  const mapboxgl = {Map: FakeMap, NavigationControl: class {}, LngLatBounds: class {extend() {return this;}}};
  const context = {
    document: {querySelector: id => elements.get(id), body: {addEventListener(name, fn) {bodyEvents[name] = fn;}}},
    window: {mapboxgl}, mapboxgl,
    fetch: async () => ({ok: true, json: async () => ({token: 'pk.test'})})
  };
  const code = fs.readFileSync(`${__dirname}/../../web/app.js`, 'utf8');
  await vm.runInNewContext(`(async () => {${code}\n})()`, context);
  instance.events['style.load']();
  assert.equal(instance.fits, 1);
  const originalGeometry = instance.sources.route.data;
  elements.get('#map-data').textContent = JSON.stringify({samples: [{point: snapshot.forecasts[0].point, value: 4}], metric: 'temperature'});
  bodyEvents['htmx:afterSwap']({detail: {target: {id: 'panel'}}});
  assert.equal(instance.sources.route.updates, 0);
  assert.equal(instance.sources.route.data, originalGeometry);
  assert.equal(instance.sources.samples.data.features[0].properties.value, 4);
  assert.equal(instance.fits, 1);
  elements.get('#basemap').change({target: {value: 'standard'}});
  assert.equal(instance.style, 'mapbox://styles/mapbox/standard');
  assert.deepEqual(instance.layers, ['areas', 'route-halo', 'route', 'waypoints', 'samples']);
  assert.equal(instance.sources.route.data.type, 'FeatureCollection');
  assert.equal(JSON.stringify(instance.sources.route.data.features.map(f => f.geometry)), JSON.stringify([
    {type: 'Point', coordinates: snapshot.route.points[0]},
    {type: 'LineString', coordinates: snapshot.route.lines[0]},
    {type: 'Polygon', coordinates: snapshot.route.polygons[0]}
  ]));
  assert.equal(instance.sources.samples.data.features[0].properties.value, 4);
  assert.equal(instance.sources.route.data, originalGeometry);
  assert.equal(instance.sources.route.updates, 0);
  assert.equal(instance.fits, 1);
  assert.equal(elements.get('#sample-count').textContent, '1 forecast locations');
});
