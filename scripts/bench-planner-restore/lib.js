const { chromium } = require('playwright-core');
const fs = require('fs');
const os = require('os');

const READY = process.env.DEV_READY;
const ready = JSON.parse(fs.readFileSync(READY, 'utf8'));
if (process.env.PROXY) { const m = ready.url.replace('127.0.0.1', process.env.PROXY === '1' ? '127.0.0.8' : process.env.PROXY); ready.login = ready.login.replace(ready.url, m); ready.url = m; }
const ORIGIN = ready.url;

const PROFILES = {
  desktop: { viewport: { width: 1440, height: 900 }, dpr: 1, mobile: false, cpu: 1, net: null },
  mobile: {
    viewport: { width: 390, height: 844 }, dpr: 3, mobile: true, cpu: 4,
    net: { offline: false, latency: 150, downloadThroughput: (1.6 * 1024 * 1024 * 0.9) / 8, uploadThroughput: (750 * 1024 * 0.9) / 8 },
  },
};

const INIT = `
  window.__perf = {lcp: 0, longtasks: [], cls: 0};
  try { new PerformanceObserver(l => { for (const e of l.getEntries()) window.__perf.lcp = e.startTime; }).observe({type: 'largest-contentful-paint', buffered: true}); } catch (_) {}
  try { new PerformanceObserver(l => { for (const e of l.getEntries()) window.__perf.longtasks.push([e.startTime, e.duration]); }).observe({type: 'longtask', buffered: true}); } catch (_) {}
  try { new PerformanceObserver(l => { for (const e of l.getEntries()) if (!e.hadRecentInput) window.__perf.cls += e.value; }).observe({type: 'layout-shift', buffered: true}); } catch (_) {}
`;

async function launch() {
  return chromium.launch({ executablePath: '/usr/bin/chromium', headless: true, args: ['--no-first-run', '--disable-extensions'] });
}

async function newContext(browser, profileName, identity = 'member') {
  const p = PROFILES[profileName];
  const context = await browser.newContext({ viewport: p.viewport, deviceScaleFactor: p.dpr, isMobile: p.mobile, hasTouch: p.mobile });
  await context.addInitScript(INIT);
  const page = await context.newPage();
  const loginURL = identity === 'member' ? ready.login : ready.login + '&choose=1';
  await page.goto(loginURL);
  if (identity !== 'member') await page.click(`[data-identity="${identity}"]`);
  await page.waitForURL(ORIGIN + '/', { timeout: 30000 });
  await page.waitForLoadState('load');
  const cdp = await context.newCDPSession(page);
  await cdp.send('Network.enable');
  await cdp.send('Performance.enable');
  const net = { reqs: new Map(), log: [] };
  cdp.on('Network.requestWillBeSent', e => net.reqs.set(e.requestId, { url: e.request.url, method: e.request.method, start: e.timestamp, type: e.type, initiator: e.initiator && e.initiator.type }));
  cdp.on('Network.responseReceived', e => { const r = net.reqs.get(e.requestId); if (r) { r.status = e.response.status; r.fromCache = e.response.fromDiskCache || e.response.fromMemoryCache || false; r.headers = e.response.headers; r.timing = e.response.timing; r.protocol = e.response.protocol; } });
  cdp.on('Network.requestServedFromCache', e => { const r = net.reqs.get(e.requestId); if (r) r.fromCache = true; });
  cdp.on('Network.loadingFinished', e => { const r = net.reqs.get(e.requestId); if (r) { r.bytes = e.encodedDataLength; r.end = e.timestamp; net.log.push(r); } });
  cdp.on('Network.loadingFailed', e => { const r = net.reqs.get(e.requestId); if (r) { r.failed = e.errorText; r.end = e.timestamp; net.log.push(r); } });
  return { context, page, cdp, net, profile: p };
}

async function throttle(s, on) {
  const p = s.profile;
  await s.cdp.send('Emulation.setCPUThrottlingRate', { rate: on ? p.cpu : 1 });
  if (p.net) await s.cdp.send('Network.emulateNetworkConditions', on ? p.net : { offline: false, latency: 0, downloadThroughput: -1, uploadThroughput: -1 });
}

function resetNet(s) { s.net.reqs.clear(); s.net.log.length = 0; }

function summarizeNet(s) {
  const log = s.net.log.slice();
  const t0 = Math.min(...log.map(r => r.start));
  return {
    count: log.length,
    bytes: log.reduce((a, r) => a + (r.bytes || 0), 0),
    fromCache: log.filter(r => r.fromCache).length,
    items: log.map(r => ({
      url: r.url.replace(ORIGIN, '').replace(/key=[^&]+/, 'key=REDACTED').slice(0, 90), status: r.status, bytes: r.bytes, cache: !!r.fromCache,
      startMs: Math.round((r.start - t0) * 1000), durMs: Math.round(((r.end || r.start) - r.start) * 1000),
      ttfbMs: r.timing ? Math.round(r.timing.receiveHeadersEnd - r.timing.sendEnd) : null,
      cc: r.headers && (r.headers['cache-control'] || r.headers['Cache-Control'] || ''), enc: r.headers && (r.headers['content-encoding'] || ''),
    })),
  };
}

async function pageMetrics(page) {
  return page.evaluate(() => {
    const nav = performance.getEntriesByType('navigation')[0];
    const fcp = performance.getEntriesByName('first-contentful-paint')[0];
    const p = window.__perf || { lcp: 0, longtasks: [] };
    const fcpT = fcp ? fcp.startTime : 0;
    const tbt = p.longtasks.reduce((a, [s, d]) => a + Math.max(0, d - 50), 0);
    const res = performance.getEntriesByType('resource').map(r => ({ n: r.name.replace(location.origin, '').split('?')[0], rb: r.renderBlockingStatus, ts: r.transferSize, start: Math.round(r.startTime), end: Math.round(r.responseEnd) }));
    return {
      ttfb: nav ? Math.round(nav.responseStart) : null, fcp: Math.round(fcpT), lcp: Math.round(p.lcp), dcl: nav ? Math.round(nav.domContentLoadedEventEnd) : null,
      load: nav ? Math.round(nav.loadEventEnd) : null, tbt: Math.round(tbt), longtasks: p.longtasks.length, cls: +(p.cls || 0).toFixed(3),
      docBytes: nav ? nav.transferSize : null, docDecoded: nav ? nav.decodedBodySize : null, domNodes: document.getElementsByTagName('*').length, res,
    };
  });
}

function load() { return os.loadavg().map(x => x.toFixed(2)).join(' '); }

function stats(xs) {
  const s = xs.slice().sort((a, b) => a - b);
  const med = s[Math.floor(s.length / 2)];
  return { med, min: s[0], max: s[s.length - 1] };
}

module.exports = { launch, newContext, throttle, resetNet, summarizeNet, pageMetrics, load, stats, ORIGIN, PROFILES };
