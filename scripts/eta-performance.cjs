const { chromium } = require('playwright-core');
const fs = require('node:fs');
const os = require('node:os');
const assert = require('node:assert/strict');
const ready = JSON.parse(fs.readFileSync(process.env.READY, 'utf8'));
const source = fs.readFileSync(process.env.SOURCE || 'web/static/js/event-planner.js', 'utf8');
async function measure(page, cdp, action, selector, done) {
    await page.evaluate(() => { window.measurement = null; document.addEventListener('click', () => {
        window.measurement = {start: performance.now(), count: window.formatterCount};
    }, {once: true, capture: true}); });
    await cdp.send('Profiler.start');
    await page.click(selector);
    await page.waitForFunction(done, null, {polling: 'raf'});
    const result = await page.evaluate(() => new Promise(resolve => requestAnimationFrame(() => {
        const end = performance.now();
        setTimeout(() => resolve({ms: end - measurement.start,
            constructors: formatterCount - measurement.count,
            longTaskMs: tasks.filter(t => t.startTime >= measurement.start && t.startTime < end).reduce((n,t) => n+t.duration,0)}), 0);
    })));
    const {profile} = await cdp.send('Profiler.stop');
    const nodes = new Map(profile.nodes.map(n => [n.id, n.callFrame.functionName]));
    const self = {};
    profile.samples.forEach((id,i) => { const name = nodes.get(id) || '(anonymous)'; self[name] = (self[name] || 0) + profile.timeDeltas[i]/1000; });
    return {action, ...result, load: os.loadavg(), self: Object.entries(self).sort((a,b)=>b[1]-a[1]).slice(0,15)};
}
(async () => {
    const browser = await chromium.launch({executablePath: process.env.CHROMIUM || '/usr/bin/chromium', args: ['--no-proxy-server']});
    try {
        for (let run=0; run<Number(process.env.RUNS || 5); run++) {
            const context = await browser.newContext({viewport:{width:390,height:844},deviceScaleFactor:3,isMobile:true});
            await context.addInitScript(() => {
                window.formatterCount = 0; window.tasks=[];
                const original = Intl.DateTimeFormat;
                Intl.DateTimeFormat = new Proxy(original, {construct(target,args) { window.formatterCount++; return Reflect.construct(target,args); }});
                new PerformanceObserver(list => tasks.push(...list.getEntries().map(e=>({startTime:e.startTime,duration:e.duration})))).observe({type:'longtask'});
            });
            await context.route('**/static/js/event-planner.js*', route => route.fulfill({body:source,contentType:'application/javascript'}));
            const page = await context.newPage();
            await page.goto(ready.login);
            await page.waitForFunction(() => !document.documentElement.classList.contains('planner-restoring'));
            await page.click('#event-activity-location-select button');
            await page.locator('#route-editor input[type=radio]').first().check();
            await page.click('#route-editor form[method=post] button[type=submit]');
            await page.waitForFunction(() => document.getElementById('activity-location-native').value !== '');
            await page.evaluate(async () => { await selectAllParticipants(); await selectAllDrivers(); });
            await page.fill('#route-time','17:30');
            const cdp = await context.newCDPSession(page);
            await cdp.send('Emulation.setCPUThrottlingRate',{rate:4});
            await cdp.send('Network.enable');
            await cdp.send('Network.emulateNetworkConditions',{offline:false,latency:150,downloadThroughput:188744,uploadThroughput:86400});
            await cdp.send('Profiler.enable');
            console.log(JSON.stringify({run,...await measure(page,cdp,'calculate','#calculate-btn',()=>!!document.querySelector('.route-card'))}));
            const fixture = '<section><div class="routes-container" data-route-time="17:30" data-route-mode="dropoff">' + Array.from({length:16}, (_,r) => '<article class="route-card" data-route-duration-secs="3600">' + Array.from({length:5}, (_,i) => `<div class="stop-item" data-participant-name="Rider ${r*5+i}" data-stop-cumulative-duration-secs="${i*600}"><span class="stop-eta">stale</span><button>Move</button></div>`).join('') + '</article>').join('') + '</div></section>';
            const equivalence = await page.evaluate(({fixture}) => {
                const a=document.createElement('div'); a.innerHTML=fixture;
                const b=a.cloneNode(true);
                const old=RideHomeRouterPlanner.createRouteHandoff({platform:{},formatTime:date=>new Intl.DateTimeFormat(undefined,{hour:'numeric',minute:'2-digit'}).format(date)});
                const current=RideHomeRouterPlanner.createRouteHandoff({platform:{}});
                for (const mode of ['pickup','dropoff']) for (const time of ['00:00','12:00','23:59','invalid']) {
                    for (const el of [a,b]) { el.querySelector('.routes-container').dataset.routeMode=mode; el.querySelector('.routes-container').dataset.routeTime=time; }
                    old.populateEtas(a.querySelector('.routes-container')); current.populateEtas(b.querySelector('.routes-container'));
                    if(a.outerHTML!==b.outerHTML) return false;
                }
                const timings = [];
                for (let n=0;n<5;n++) {
                    const container=a.querySelector('.routes-container'); container.dataset.routeTime='17:30';
                    const start=performance.now(); for(let j=0;j<10;j++) current.populateEtas(container);
                    timings.push(performance.now()-start);
                }
                return {matches:true, populate80x10Ms:timings};
            },{fixture});
            assert.ok(equivalence,'ETA strings and full results DOM match per-call formatter oracle');
            await page.locator('.routes-container button[aria-label^="Move "]').first().click();
            await page.locator('#route-editor input[name=destination]').first().check();
            await page.evaluate(()=>document.querySelectorAll('.route-card').forEach(c=>c.dataset.perfOld='1'));
            console.log(JSON.stringify({run,equivalence,...await measure(page,cdp,'move','#route-editor form[method=post] button[type=submit]',()=>!document.getElementById('route-editor-dialog').open && !!document.querySelector('.route-card:not([data-perf-old])'))}));
            await context.close();
        }
    } finally { await browser.close(); }
})().catch(error => {console.error(error);process.exitCode=1;});
