'use strict';
const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const { execFileSync } = require('node:child_process');
const { pathToFileURL } = require('node:url');

const browser = process.env.BROWSER_TEST_BINARY;

function card(index, driver, riders, timings, measuredSecs) {
    const stops = riders.map((rider, i) => `<div class="stop-item" data-participant-id="${rider}" data-stop-cumulative-duration-secs="${timings === 'measured' ? (i + 1) * measuredSecs : ''}"><div class="stop-details"><h4>R${rider}</h4></div>${timings === 'measured' ? `<div class="stop-distance"><strong class="stop-eta"></strong>${measuredSecs} m</div>` : ''}</div>`).join('');
    const stats = timings === 'measured' ? `<div class="stat"><dt>Total</dt><dd>${measuredSecs} km</dd></div>` : `<div class="stat route-timings-status"><dt>Timings</dt><dd>Timings not refreshed for this change. <button type="button">Show timings</button></dd></div>`;
    return `<section class="route-card" data-route-index="${index}" data-driver-id="${driver}" data-itinerary="s1:${driver}:${riders.join(',')},"  data-timings="${timings}" data-route-duration-secs="${timings === 'measured' ? measuredSecs * 10 : ''}"><dl class="route-stats route-timings">${stats}</dl><div class="route-stops">${stops}</div><div class="route-footer">${timings === 'measured' ? '<span class="route-attribution">Google Maps</span>' : ''}<span>Route</span></div></section>`;
}

test('preserveTimings keeps on-screen timings only for cars whose itinerary did not change', { skip: !browser }, () => {
    const directory = fs.mkdtempSync(path.join(os.tmpdir(), 'rhr-timings-'));
    try {
        const previous = `<div class="routes-container" data-session-id="s1">${card(0, 10, [1, 2], 'measured', 60)}${card(1, 11, [3], 'measured', 90)}</div>`;
        const response = `<div class="routes-container" data-session-id="s1">${card(0, 10, [1], 'measured', 45)}${card(1, 11, [3], 'stale', 0)}${card(2, 12, [2], 'stale', 0)}</div>`;
        const fixture = `<!doctype html><html><body><div id="results-section">${previous}</div><pre id="evidence">pending</pre>
<script>${fs.readFileSync(path.join(__dirname, 'event-planner.js'), 'utf8')}</script><script>
const target=document.getElementById('results-section');
const merged=window.RideHomeRouterPlanner.preserveTimings(target, ${JSON.stringify(response)}, document);
target.innerHTML=merged;
const cards=[...document.querySelectorAll('.route-card')].map(c=>({timings:c.dataset.timings,duration:c.dataset.routeDurationSecs,stats:c.querySelector('.route-timings').textContent.trim(),stops:[...c.querySelectorAll('.stop-item')].map(s=>s.dataset.stopCumulativeDurationSecs),attribution:!!c.querySelector('.route-attribution')}));
document.getElementById('evidence').textContent=JSON.stringify(cards);
</script></body></html>`;
        const file = path.join(directory, 'fixture.html');
        fs.writeFileSync(file, fixture);
        const output = execFileSync(browser, ['--headless', '--no-sandbox', '--disable-gpu', '--no-first-run', `--user-data-dir=${directory}/profile`, '--dump-dom', '--virtual-time-budget=1000', pathToFileURL(file).href], { encoding: 'utf8', timeout: 60000, stdio: ['ignore', 'pipe', 'pipe'], maxBuffer: 2 * 1024 * 1024 });
        const cards = JSON.parse(output.match(/<pre id="evidence">(.*?)<\/pre>/s)?.[1] || 'null');
        assert.equal(cards.length, 3);
        assert.equal(cards[0].timings, 'measured');
        assert.equal(cards[0].duration, '450', 'a re-measured car shows its new values');
        assert.equal(cards[1].timings, 'measured', 'an unchanged car keeps the timings already on screen');
        assert.equal(cards[1].duration, '900');
        assert.deepEqual(cards[1].stops, ['90']);
        assert.match(cards[1].stats, /90 km/);
        assert.equal(cards[1].attribution, true);
        assert.equal(cards[2].timings, 'stale', 'a new itinerary never inherits another car\'s numbers');
        assert.deepEqual(cards[2].stops, ['']);
    } finally {
        fs.rmSync(directory, { recursive: true, force: true });
    }
});

test('refreshRouteTotals fills the plan totals once every occupied car on screen is measured', { skip: !browser }, () => {
    const directory = fs.mkdtempSync(path.join(os.tmpdir(), 'rhr-totals-'));
    try {
        const card = (index, timings, stops, meters, detour) => `<section class="route-card" data-route-index="${index}" data-timings="${timings}" data-total-distance-meters="${meters}" data-detour-secs="${detour}"><div class="route-stops">${'<div class="stop-item"></div>'.repeat(stops)}</div></section>`;
        const summary = `<div class="route-summary"><div class="value" data-summary="total-distance">—</div><div class="value" data-summary="max-detour">—</div><div class="value" data-summary="average-detour">—</div></div>`;
        const page = (cards, outOfBalance) => `<div class="routes-container" data-use-miles="true" data-out-of-balance="${outOfBalance}">${cards}${summary}</div>`;
        const fixture = `<!doctype html><html><body><div id="results-section"></div><pre id="evidence">pending</pre>
<script>${fs.readFileSync(path.join(__dirname, 'event-planner.js'), 'utf8')}</script><script>
const target=document.getElementById('results-section');
const read=()=>[...target.querySelectorAll('[data-summary]')].map(e=>e.textContent);
const out={};
target.innerHTML=${JSON.stringify(page(card(0, 'measured', 2, 8000, 300) + card(1, 'stale', 1, '', '') + card(2, 'empty', 0, '', ''), false))};
out.partial=[window.RideHomeRouterPlanner.refreshRouteTotals(target), read()];
target.innerHTML=${JSON.stringify(page(card(0, 'measured', 2, 8000, 300) + card(1, 'measured', 1, 2000, 90) + card(2, 'empty', 0, '', ''), false))};
out.complete=[window.RideHomeRouterPlanner.refreshRouteTotals(target), read()];
target.innerHTML=${JSON.stringify(page(card(0, 'measured', 2, 8000, 300) + card(1, 'measured', 1, 2000, 90), true))};
out.paused=[window.RideHomeRouterPlanner.refreshRouteTotals(target), read()];
document.getElementById('evidence').textContent=JSON.stringify(out);
</script></body></html>`;
        const file = path.join(directory, 'fixture.html');
        fs.writeFileSync(file, fixture);
        const output = execFileSync(browser, ['--headless', '--no-sandbox', '--disable-gpu', '--no-first-run', `--user-data-dir=${directory}/profile`, '--dump-dom', '--virtual-time-budget=1000', pathToFileURL(file).href], { encoding: 'utf8', timeout: 60000, stdio: ['ignore', 'pipe', 'pipe'], maxBuffer: 2 * 1024 * 1024 });
        const out = JSON.parse(output.match(/<pre id="evidence">(.*?)<\/pre>/s)?.[1] || 'null');
        assert.deepEqual(out.partial, [false, ['—', '—', '—']], 'a stale car leaves the totals alone');
        assert.deepEqual(out.complete, [true, ['6.21 mi', '5m', '3m 15s']]);
        assert.deepEqual(out.paused, [false, ['—', '—', '—']], 'an out-of-balance plan never gets totals');
    } finally {
        fs.rmSync(directory, { recursive: true, force: true });
    }
});
