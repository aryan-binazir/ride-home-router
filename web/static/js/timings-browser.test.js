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
        // After the edit: car 0 was re-measured with new values, car 1 is unchanged but stale,
        // and car 2 is a new itinerary that has never been measured.
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
