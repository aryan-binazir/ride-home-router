'use strict';

const test = require('node:test');
const assert = require('node:assert/strict');

const { filterTable, switchRosterTab, toggleEventDetail } = require('./ui.js');

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

test('event history toggle updates expanded state and visible label', () => {
  const eventItem = element([]);
  const detail = { innerHTML: '' };
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
