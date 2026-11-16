// NODE_PATH=/path/to/playwright-core/.. node scripts/perf/planner-restore.cjs READY_JSON OUTPUT_JSON [runs=5]
// READY_JSON is make dev's private ready.json. Only synthetic local providers are used.
'use strict';
const {chromium} = require('playwright-core');
const {spawn} = require('node:child_process');
const fs = require('node:fs');
const http = require('node:http');
const os = require('node:os');
const path = require('node:path');
const zlib = require('node:zlib');
const {once} = require('node:events');
const {setTimeout: delay} = require('node:timers/promises');

async function main() {
    const [readyFile, output, count = '5'] = process.argv.slice(2);
    if (!readyFile || !output) throw Error('Expected READY_JSON OUTPUT_JSON [runs]');
    const ready = JSON.parse(fs.readFileSync(readyFile));
    const upstream = new URL(ready.url);
    if (upstream.hostname !== '127.0.0.1') throw Error('Only local make dev stacks are supported');
    let origin;
    const proxy = http.createServer((req, res) => {
        const headers = {...req.headers, host: upstream.host};
        for (const key of ['origin', 'referer']) if (headers[key]) headers[key] = headers[key].replace(origin, ready.url);
        delete headers['accept-encoding'];
        const request = http.request(new URL(req.url, upstream), {method: req.method, headers}, response => {
            const chunks = [];
            response.on('data', chunk => chunks.push(chunk));
            response.on('end', () => {
                const headers = {...response.headers};
                if (headers.location) headers.location = headers.location.replace(ready.url, origin);
                if (req.url.startsWith('/static/')) headers['cache-control'] = 'max-age=14400';
                let body = Buffer.concat(chunks);
                if (/text|javascript|json|svg/.test(headers['content-type'] || '') && /br/.test(req.headers['accept-encoding'] || '')) {
                    body = zlib.brotliCompressSync(body, {params: {[zlib.constants.BROTLI_PARAM_QUALITY]: 4}});
                    headers['content-encoding'] = 'br';
                    headers.vary = 'accept-encoding';
                }
                headers['content-length'] = body.length;
                res.writeHead(response.statusCode, headers);
                res.end(body);
            });
        });
        request.on('error', error => { res.writeHead(502); res.end(error.message); });
        req.pipe(request);
    });
    proxy.listen(0, '127.0.0.1');
    await once(proxy, 'listening');
    origin = `http://127.0.0.1:${proxy.address().port}`;
    const directory = fs.mkdtempSync(path.join(os.tmpdir(), 'rhr-restore-cdp-'));
    const processChrome = spawn(process.env.CHROMIUM || '/usr/bin/chromium', [
        '--headless', '--no-sandbox', '--disable-gpu', '--no-first-run', '--no-proxy-server',
        '--remote-debugging-port=0', `--user-data-dir=${directory}`,
    ], {stdio: 'ignore', env: {...process.env, DBUS_SESSION_BUS_ADDRESS: 'unix:path=/dev/null'}});
    let browser;
    try {
        const activePort = path.join(directory, 'DevToolsActivePort');
        let port = '';
        for (let n = 0; !/^\d+$/.test(port); n++) {
            if (n > 300 || processChrome.exitCode !== null) throw Error('Chromium CDP did not start');
            if (fs.existsSync(activePort)) port = fs.readFileSync(activePort, 'utf8').split('\n')[0];
            if (!/^\d+$/.test(port)) await delay(100);
        }
        browser = await chromium.connectOverCDP(`http://127.0.0.1:${port}`);
        const results = [];
        for (const mobile of [false, true]) {
            const context = await browser.newContext({viewport: mobile ? {width: 390, height: 844} : {width: 1440, height: 900}, deviceScaleFactor: mobile ? 3 : 1, isMobile: mobile, hasTouch: mobile});
            await context.addInitScript(() => {
                window.restoreEvidence = {visible: null, earlyRoutes: false};
                const sample = () => {
                    const restoring = document.documentElement.classList.contains('planner-restoring');
                    const card = document.querySelector('#results-section .route-card');
                    if (card && restoring && getComputedStyle(document.getElementById('event-form')).visibility !== 'hidden') window.restoreEvidence.earlyRoutes = true;
                    if (!restoring && card && card.getBoundingClientRect().height > 0) window.restoreEvidence.visible ??= performance.now();
                    requestAnimationFrame(sample);
                };
                requestAnimationFrame(sample);
            });
            const page = await context.newPage();
            const errors = [];
            page.on('pageerror', error => errors.push(error.message));
            await page.goto(ready.login.replace(ready.url, origin));
            await page.waitForFunction(() => !document.documentElement.classList.contains('planner-restoring'));
            await page.click('#event-activity-location-select button');
            await page.locator('#route-editor input[type=radio]').first().check();
            await page.click('#route-editor form[method=post] button[type=submit]');
            await page.waitForFunction(() => document.getElementById('activity-location-native').value);
            await page.evaluate(() => { selectAllParticipants(); selectAllDrivers(); });
            await page.waitForFunction(() => +document.querySelector('[data-picker-summary="drivers"]').textContent > 0 && +document.querySelector('[data-picker-summary="participants"]').textContent > 0);
            await page.fill('#route-time', '17:30');
            await page.click('#calculate-btn');
            await page.waitForSelector('#results-section .route-card');
            await delay(4000);
            const cdp = await context.newCDPSession(page);
            await cdp.send('Network.enable');
            await cdp.send('Emulation.setCPUThrottlingRate', {rate: mobile ? 4 : 1});
            if (mobile) await cdp.send('Network.emulateNetworkConditions', {offline: false, latency: 150, downloadThroughput: 1.6 * 1024 * 1024 * 0.9 / 8, uploadThroughput: 750 * 1024 * 0.9 / 8});
            let requests = new Map();
            cdp.on('Network.requestWillBeSent', e => requests.set(e.requestId, {path: new URL(e.request.url).pathname, start: e.timestamp, type: e.type}));
            cdp.on('Network.loadingFinished', e => { const request = requests.get(e.requestId); if (request) request.end = e.timestamp; });
            for (let run = 0; run < Number(count); run++) {
                requests = new Map();
                const load = os.loadavg();
                await page.goto(origin + '/');
                await page.waitForFunction(() => window.restoreEvidence.visible !== null, null, {timeout: 60000});
                const evidence = await page.evaluate(() => ({...window.restoreEvidence,
                    draft: JSON.parse(localStorage.getItem('ride-home-router:event-planner-draft:v1')),
                    status: document.getElementById('results-section').dataset.planStatus,
                    cards: document.querySelectorAll('#results-section .route-card').length,
                    requests: performance.getEntriesByType('resource').filter(r => /\/planner\/|\/routes\/session/.test(r.name)).map(r => ({path: new URL(r.name).pathname, start: r.startTime, duration: r.duration})),
                }));
                const start = [...requests.values()].find(r => r.type === 'Document').start;
                results.push({profile: mobile ? 'mobile' : 'desktop', run, load, ...evidence, cdp: [...requests.values()].filter(r => /\/planner\/|\/routes\/session/.test(r.path)).map(r => ({path: r.path, start: (r.start - start) * 1000, duration: (r.end - r.start) * 1000})), errors: errors.splice(0)});
                console.log(JSON.stringify({profile: mobile ? 'mobile' : 'desktop', run, visible: Math.round(evidence.visible), load, requests: evidence.requests}));
            }
            await context.close();
        }
        fs.writeFileSync(output, JSON.stringify(results, null, 2) + '\n');
    } finally {
        if (browser) await browser.close();
        processChrome.kill('SIGTERM');
        if (processChrome.exitCode === null) await once(processChrome, 'exit');
        proxy.closeAllConnections();
        await new Promise(resolve => proxy.close(resolve));
        fs.rmSync(directory, {recursive: true, force: true, maxRetries: 10, retryDelay: 100});
    }
}
main().catch(error => { console.error(error); process.exitCode = 1; });
