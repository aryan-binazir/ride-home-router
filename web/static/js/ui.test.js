'use strict';

const test = require('node:test');
const assert = require('node:assert/strict');

const { filterTable, handleTableSwap, reapplyTableFilter, switchRosterTab, toggleEventDetail } = require('./ui.js');

function element(classes = []) {
  const values = new Set(classes);
  return {
    attributes: {},
    classList: {
      add: value => values.add(value),
      remove: value => values.delete(value),
      toggle: (value, force) => force ? values.add(value) : values.delete(value),
      contains: value => values.has(value),
    },
    setAttribute(name, value) {
      this.attributes[name] = value;
    },
  };
}

test('switchRosterTab toggles containers, styles, and pressed state', () => {
  const elements = {
    'participants-active': element([]),
    'participants-deleted': element(['hidden']),
    'participants-active-tab': element(['btn-primary']),
    'participants-deleted-tab': element(['btn-outline']),
  };
  global.document = { getElementById: id => elements[id] || null };
  test.after(() => { delete global.document; });

  switchRosterTab(elements['participants-deleted-tab'], 'participants');

  assert.equal(elements['participants-active'].classList.contains('hidden'), true);
  assert.equal(elements['participants-deleted'].classList.contains('hidden'), false);
  assert.equal(elements['participants-active-tab'].classList.contains('btn-outline'), true);
  assert.equal(elements['participants-deleted-tab'].classList.contains('btn-primary'), true);
  assert.equal(elements['participants-active-tab'].attributes['aria-pressed'], 'false');
  assert.equal(elements['participants-deleted-tab'].attributes['aria-pressed'], 'true');

  switchRosterTab(elements['participants-active-tab'], 'participants');

  assert.equal(elements['participants-active'].classList.contains('hidden'), false);
  assert.equal(elements['participants-deleted'].classList.contains('hidden'), true);
  assert.equal(elements['participants-active-tab'].classList.contains('btn-primary'), true);
  assert.equal(elements['participants-deleted-tab'].classList.contains('btn-outline'), true);
  assert.equal(elements['participants-active-tab'].attributes['aria-pressed'], 'true');
  assert.equal(elements['participants-deleted-tab'].attributes['aria-pressed'], 'false');
});

test('filterTable shows a filtered-empty row only when no data rows match', () => {
  const alpha = element([]);
  alpha.dataset = { search: 'Alpha group' };
  const beta = element([]);
  beta.dataset = { search: 'Beta group' };
  const empty = element(['hidden']);
  const tbody = { querySelectorAll: () => [alpha, beta] };
  global.document = {
    getElementById(id) {
      if (id === 'labels-tbody') return tbody;
      if (id === 'labels-tbody-empty') return empty;
      return null;
    },
  };
  test.after(() => { delete global.document; });

  filterTable({ value: 'Gamma' }, 'labels-tbody');
  assert.equal(alpha.classList.contains('hidden'), true);
  assert.equal(beta.classList.contains('hidden'), true);
  assert.equal(empty.classList.contains('hidden'), false);

  filterTable({ value: 'Alpha' }, 'labels-tbody');
  assert.equal(alpha.classList.contains('hidden'), false);
  assert.equal(beta.classList.contains('hidden'), true);
  assert.equal(empty.classList.contains('hidden'), true);
});

test('reapplyTableFilter keeps the current search after a list swap', () => {
  const matching = element([]);
  matching.dataset = { search: 'Alpha group' };
  const other = element([]);
  other.dataset = { search: 'Beta group' };
  const empty = element(['hidden']);
  const input = { value: 'Alpha' };
  const tbody = { querySelectorAll: () => [matching, other] };
  global.document = {
    getElementById(id) {
      return {
        'labels-search': input,
        'labels-tbody': tbody,
        'labels-tbody-empty': empty,
      }[id] || null;
    },
  };
  test.after(() => { delete global.document; });

  reapplyTableFilter('labels-list');

  assert.equal(matching.classList.contains('hidden'), false);
  assert.equal(other.classList.contains('hidden'), true);
  assert.equal(empty.classList.contains('hidden'), true);
});

test('handleTableSwap uses the HTMX response target', () => {
  const matching = element([]);
  matching.dataset = { search: 'Alpha group' };
  const other = element([]);
  other.dataset = { search: 'Beta group' };
  const empty = element(['hidden']);
  const input = { value: 'Alpha' };
  const tbody = { querySelectorAll: () => [matching, other] };
  global.document = {
    getElementById(id) {
      return {
        'labels-search': input,
        'labels-tbody': tbody,
        'labels-tbody-empty': empty,
      }[id] || null;
    },
  };
  test.after(() => { delete global.document; });

  handleTableSwap({ target: { id: 'label-form' }, detail: { target: { id: 'labels-list' } } });

  assert.equal(matching.classList.contains('hidden'), false);
  assert.equal(other.classList.contains('hidden'), true);
});

test('event history toggle updates expanded state and visible label', () => {
  const eventItem = element([]);
  const detail = { innerHTML: '', dataset: {}, getAttribute() { return null; }, setAttribute() {}, removeAttribute() {} };
  const label = { textContent: 'View details' };
  const toggle = {
    attributes: {},
    setAttribute(name, value) { this.attributes[name] = value; },
    querySelector() { return label; },
  };
  global.document = { getElementById: () => detail };
  global.htmx = { ajax() {} };
  test.after(() => {
    delete global.document;
    delete global.htmx;
  });

  toggleEventDetail(eventItem, 7, toggle);
  assert.equal(eventItem.classList.contains('expanded'), true);
  assert.equal(toggle.attributes['aria-expanded'], 'true');
  assert.equal(label.textContent, 'Hide details');

  detail.innerHTML = '<p>Loaded</p>';
  toggleEventDetail(eventItem, 7, toggle);
  assert.equal(eventItem.classList.contains('expanded'), false);
  assert.equal(toggle.attributes['aria-expanded'], 'false');
  assert.equal(label.textContent, 'View details');
});

test('event history toggle collapses while loading and recovers from request failure', async () => {
  const eventItem = element([]);
  eventItem.dataset = {};
  const detail = { innerHTML: '', dataset: {}, attributes: {}, getAttribute(name) { return this.attributes[name]; }, setAttribute(name, value) { this.attributes[name] = value; }, removeAttribute(name) { delete this.attributes[name]; } };
  const label = { textContent: 'View details' };
  const toggle = {
    attributes: {},
    setAttribute(name, value) { this.attributes[name] = value; },
    getAttribute(name) { return this.attributes[name]; },
    querySelector() { return label; },
  };
  const requests = [];
  global.document = { getElementById: () => detail };
  global.htmx = {
    ajax() {
      let resolve;
      let reject;
      const promise = new Promise((resolveRequest, rejectRequest) => {
        resolve = resolveRequest;
        reject = rejectRequest;
      });
      requests.push({ reject, resolve });
      return promise;
    },
  };
  test.after(() => {
    delete global.document;
    delete global.htmx;
  });

  toggleEventDetail(eventItem, 7, toggle);
  toggleEventDetail(eventItem, 7, toggle);
  assert.equal(requests.length, 1);
  assert.equal(eventItem.classList.contains('expanded'), false);
  assert.equal(toggle.attributes['aria-expanded'], 'false');
  detail.innerHTML = '<p>Loaded after collapse</p>';
  requests[0].resolve();
  await new Promise(resolve => setImmediate(resolve));
  assert.equal(detail.innerHTML, '');

  toggleEventDetail(eventItem, 7, toggle);
  assert.equal(requests.length, 2);
  requests[1].reject(new Error('request failed'));
  await new Promise(resolve => setImmediate(resolve));
  assert.equal(eventItem.classList.contains('expanded'), false);
  assert.equal(toggle.attributes['aria-expanded'], 'false');
  assert.equal(label.textContent, 'View details');
});
