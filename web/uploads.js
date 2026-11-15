const form = document.querySelector('#upload');
const input = document.querySelector('#file');
const list = document.querySelector('#upload-list');
const summary = document.querySelector('#upload-summary');
const queue = [];
let active = 0;
let total = 0;
let completed = 0;
let failed = 0;

function updateSummary() {
  summary.textContent = `${completed} of ${total} complete · ${active} processing · ${queue.length} queued · ${failed} failed`;
}

function processQueue() {
  // Leave one of the server's four operation slots available for refreshes.
  while (active < 3 && queue.length) {
    const item = queue.shift();
    active++;
    item.retry.hidden = true;
    item.progress.hidden = false;
    item.progress.value = 0;
    item.status.textContent = 'Uploading…';
    const request = new XMLHttpRequest();
    request.open('POST', '/forecast');
    request.setRequestHeader('X-Requested-With', 'XMLHttpRequest');
    request.timeout = 100000;
    request.upload.onprogress = event => {
      if (event.lengthComputable) {
        item.progress.value = Math.round(event.loaded / event.total * 100);
        item.status.textContent = `Uploading ${item.progress.value}%`;
      }
    };
    request.upload.onload = () => {
      item.progress.removeAttribute('value');
      item.status.textContent = 'Fetching forecast…';
    };
    request.onload = () => {
      if (request.status !== 201) {
        item.status.textContent = request.responseText.trim() || 'Upload failed. Try again.';
        return;
      }
      try {
        const result = JSON.parse(request.responseText);
        if (!/^\/routes\/[A-Za-z0-9]+$/.test(result.url)) throw new Error('Invalid route URL');
        item.link.href = result.url;
        item.link.hidden = false;
        item.status.textContent = 'Complete';
        item.file = null;
        completed++;
        document.body.dispatchEvent(new Event('routesChanged'));
      } catch {
        item.status.textContent = 'Could not read the response. Check Saved routes before retrying.';
      }
    };
    request.onerror = () => {
      item.status.textContent = 'Connection lost. Check Saved routes before retrying.';
    };
    request.ontimeout = () => {
      item.status.textContent = 'Request timed out. Check Saved routes before retrying.';
    };
    request.onloadend = () => {
      active--;
      item.progress.hidden = true;
      if (item.file) {
        failed++;
        item.retry.hidden = false;
      }
      processQueue();
    };
    const data = new FormData();
    data.append('route', item.file);
    request.send(data);
  }
  updateSummary();
}

form.addEventListener('submit', event => {
  event.preventDefault();
  for (const file of input.files) {
    const row = document.querySelector('#upload-row').content.firstElementChild.cloneNode(true);
    row.querySelector('.upload-name').textContent = file.name;
    const item = {
      file,
      progress: row.querySelector('progress'),
      status: row.querySelector('.upload-status'),
      retry: row.querySelector('.upload-retry'),
      link: row.querySelector('.upload-open')
    };
    item.retry.addEventListener('click', () => {
      item.retry.hidden = true;
      item.status.textContent = 'Queued';
      failed--;
      queue.push(item);
      processQueue();
    });
    list.append(row);
    total++;
    if (file.size > 2 * 1024 * 1024) {
      item.status.textContent = 'File exceeds the 2 MB limit.';
      item.progress.hidden = true;
      failed++;
    } else {
      queue.push(item);
    }
  }
  input.value = '';
  document.querySelector('#uploads').hidden = false;
  processQueue();
});

window.addEventListener('beforeunload', event => {
  if (active || queue.length) {
    event.preventDefault();
    event.returnValue = '';
  }
});
