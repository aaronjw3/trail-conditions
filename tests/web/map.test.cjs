const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const vm = require('node:vm');

// Controlled Mapbox and DOM boundaries verify our style-switch lifecycle.
// This does not test Mapbox rendering, WebGL, or live styles.
test('style changes restore route and selected forecast without resetting the viewport', async () => {
  const snapshot = {
    route: {type: 'MultiLineString', coordinates: [[[-121.7, 46.8], [-121.75, 46.85]]]},
    forecasts: [{point: [-121.7, 46.8], temperature: [4], precipitation: [0], wind: [12]}]
  };
  const elements = new Map();
  for (const id of ['#status', '#map-data', '#sample-count', '#low', '#high', '#gradient', '#basemap', '#map-error']) {
    elements.set(id, {textContent: '', style: {}, classList: {add() {}, remove() {}}, addEventListener(name, fn) {this[name] = fn;}});
  }
  elements.get('#map-data').textContent = JSON.stringify({snapshot, hour: 0, metric: 'wind'});
  const bodyEvents = {};
  let instance;
  class FakeMap {
    constructor() {instance = this; this.sources = {}; this.layers = []; this.events = {}; this.fits = 0;}
    addControl() {}
    on(name, fn) {this.events[name] = fn;}
    getSource(name) {return this.sources[name];}
    addSource(name, options) {this.sources[name] = {data: options.data, setData(data) {this.data = data;}};}
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
  elements.get('#basemap').change({target: {value: 'standard'}});
  assert.equal(instance.style, 'mapbox://styles/mapbox/standard');
  assert.deepEqual(instance.layers, ['route-halo', 'route', 'samples']);
  assert.equal(JSON.stringify(instance.sources.route.data.geometry), JSON.stringify(snapshot.route));
  assert.equal(instance.sources.samples.data.features[0].properties.value, 12);
  assert.equal(instance.fits, 1);
  assert.equal(elements.get('#sample-count').textContent, '1 forecast locations');
});
