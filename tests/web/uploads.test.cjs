const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const vm = require('node:vm');

// Exercise queue scheduling and request events without live weather or a browser.
test('uploads are bounded, progress is per file, failures retry, and success keeps the queue open', () => {
  const element = () => ({
    hidden: true, textContent: '', value: 0,
    addEventListener(name, fn) { this[name] = fn; },
    removeAttribute(name) { delete this[name]; }
  });
  const rows = [];
  const elements = new Map(['#upload', '#file', '#uploads', '#upload-summary'].map(id => [id, element()]));
  elements.set('#upload-list', {append(row) { rows.push(row); }});
  elements.set('#upload-row', {content: {firstElementChild: {cloneNode() {
    const children = new Map(['.upload-name', 'progress', '.upload-status', '.upload-retry', '.upload-open'].map(id => [id, element()]));
    return {querySelector: id => children.get(id)};
  }}}});
  const requests = [];
  class Request {
    constructor() { this.upload = {}; requests.push(this); }
    open(method, url) { assert.equal(method, 'POST'); assert.equal(url, '/forecast'); }
    setRequestHeader(name, value) { assert.equal(name, 'X-Requested-With'); assert.equal(value, 'XMLHttpRequest'); }
    send(data) { this.data = data; }
    finish(status, body) { this.status = status; this.responseText = body; this.onload(); this.onloadend(); }
  }
  const events = [];
  const window = element();
  vm.runInNewContext(fs.readFileSync(`${__dirname}/../../web/uploads.js`, 'utf8'), {
    document: {querySelector: id => elements.get(id), body: {dispatchEvent(event) { events.push(event.type); }}},
    window, XMLHttpRequest: Request, Event,
    FormData: class { append(key, file) { this.file = file; assert.equal(key, 'route'); } }
  });
  const submit = files => {
    elements.get('#file').files = files;
    elements.get('#upload').submit({preventDefault() {}});
  };
  const file = name => ({name, size: 100});
  submit(['one.gpx', 'two.kml', 'three.kmz', 'four.gpx'].map(file));
  assert.equal(requests.length, 3);
  assert.match(elements.get('#upload-summary').textContent, /3 processing · 1 queued/);
  requests[0].upload.onprogress({lengthComputable: true, loaded: 50, total: 100});
  assert.equal(rows[0].querySelector('progress').value, 50);
  requests[0].upload.onload();
  assert.equal(rows[0].querySelector('progress').value, undefined);
  assert.equal(rows[0].querySelector('.upload-status').textContent, 'Fetching forecast…');
  // New selections join the existing queue instead of replacing it.
  submit([file('five.gpx')]);
  assert.equal(requests.length, 3);
  requests[1].finish(201, '{"url":"/routes/second"}');
  assert.equal(requests.length, 4);
  assert.equal(requests[3].data.file.name, 'four.gpx');
  assert.equal(rows[1].querySelector('.upload-open').href, '/routes/second');
  assert.deepEqual(events, ['routesChanged']);
  requests[0].finish(429, 'Server busy');
  assert.equal(requests.length, 5);
  assert.equal(rows[0].querySelector('.upload-status').textContent, 'Server busy');
  assert.equal(rows[0].querySelector('.upload-retry').hidden, false);
  rows[0].querySelector('.upload-retry').click();
  assert.equal(requests.length, 5);
  requests[2].finish(201, '{"url":"/routes/third"}');
  assert.equal(requests.length, 6);
  assert.equal(requests[5].data.file.name, 'one.gpx');
  let prevented = false;
  window.beforeunload({preventDefault() { prevented = true; }});
  assert.equal(prevented, true);
  requests[3].finish(201, '{"url":"/routes/fourth"}');
  requests[4].ontimeout(); requests[4].onloadend();
  assert.match(rows[4].querySelector('.upload-status').textContent, /timed out/);
  requests[5].finish(201, '{"url":"/routes/first"}');
  assert.equal(elements.get('#upload-summary').textContent, '4 of 5 complete · 0 processing · 0 queued · 1 failed');
  prevented = false;
  window.beforeunload({preventDefault() { prevented = true; }});
  assert.equal(prevented, false);
  submit([{name: '<b>oversize.gpx</b>', size: 3 * 1024 * 1024}]);
  assert.equal(requests.length, 6);
  assert.equal(rows[5].querySelector('.upload-name').textContent, '<b>oversize.gpx</b>');
  assert.match(rows[5].querySelector('.upload-status').textContent, /exceeds/);
});
