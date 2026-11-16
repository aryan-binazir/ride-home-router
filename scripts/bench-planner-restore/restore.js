const L = require('./lib');
(async () => {
  const browser = await L.launch();
  try {
    for (const prof of ['desktop', 'mobile']) {
      const s = await L.newContext(browser, prof);
      const p = s.page;
      await p.goto(L.ORIGIN + '/');
      await p.waitForFunction(() => !document.documentElement.classList.contains('planner-restoring'));
      await p.click('#event-activity-location-select button');
      await p.waitForSelector('#route-editor input[type=radio]');
      await p.check('#route-editor input[type=radio] >> nth=0');
      await p.click('#route-editor form[method=post] button[type=submit]');
      await p.waitForFunction(() => document.getElementById('activity-location-native').value !== '');
      await p.evaluate(() => { selectAllParticipants(); selectAllDrivers(); });
      await p.waitForFunction(() => +document.querySelector('[data-picker-summary="drivers"]').textContent > 0 && +document.querySelector('[data-picker-summary="participants"]').textContent > 0);
      await p.fill('#route-time', '17:30');
      await p.click('#calculate-btn');
      await p.waitForSelector('#results-section .routes-container .route-card');
      await p.waitForTimeout(10000);
      for (let run = 0; run < 5; run++) {
        L.resetNet(s);
        await L.throttle(s, true);
        await p.goto(L.ORIGIN + '/');
        await p.waitForFunction(() => !document.documentElement.classList.contains('planner-restoring') && document.querySelector('#results-section .route-card'), null, { polling: 'raf', timeout: 60000 });
        const t = await p.evaluate(() => ({ ready: Math.round(performance.now()), fcp: Math.round(performance.getEntriesByName('first-contentful-paint')[0]?.startTime || 0), dcl: Math.round(performance.getEntriesByType('navigation')[0].domContentLoadedEventEnd) }));
        await p.waitForTimeout(800);
        const n = L.summarizeNet(s);
        await L.throttle(s, false);
        console.log(`${prof} reload-with-session r${run}: fcp=${t.fcp} dcl=${t.dcl} routesVisible=${t.ready} req=${n.count} la=${L.load()}`);
        console.log('   ' + n.items.filter(i => !i.url.startsWith('/static')).map(i => `${i.url.split('?')[0]} ${i.status} ${i.bytes}B start=${i.startMs} dur=${i.durMs}`).join('\n   '));
      }
      await s.context.close();
    }
  } finally { await browser.close(); }
})();
