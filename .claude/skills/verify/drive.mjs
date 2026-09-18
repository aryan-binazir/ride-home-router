// Minimal CDP driver: node drive.mjs <cfEmail|-> <scenario>
import { spawn } from 'node:child_process';
import { readFileSync, mkdtempSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';

const [cfEmail, scenario] = process.argv.slice(2);
const token = readFileSync('/tmp/verify-token', 'utf8').trim();
const base = 'http://127.0.0.1:8099';
const port = 9300 + Math.floor(Math.random() * 500);
const profile = mkdtempSync(join(tmpdir(), 'verify-chromium-'));
const chrome = spawn('/usr/bin/chromium', ['--headless=new', '--no-sandbox', '--disable-gpu', '--no-first-run', `--remote-debugging-port=${port}`, `--user-data-dir=${profile}`, '--window-size=1280,900', 'about:blank'], { stdio: 'ignore' });
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
let targets;
for (let i = 0; i < 50; i++) { try { targets = await (await fetch(`http://127.0.0.1:${port}/json/list`)).json(); if (targets.length) break; } catch {} await sleep(200); }
const ws = new WebSocket(targets.find((t) => t.type === 'page').webSocketDebuggerUrl);
await new Promise((r) => (ws.onopen = r));
let id = 0; const pending = new Map(); const events = [];
ws.onmessage = (m) => { const msg = JSON.parse(m.data); if (msg.id) { const p = pending.get(msg.id); pending.delete(msg.id); msg.error ? p.reject(new Error(JSON.stringify(msg.error))) : p.resolve(msg.result); } else events.push(msg); };
const send = (method, params = {}) => new Promise((resolve, reject) => { pending.set(++id, { resolve, reject }); ws.send(JSON.stringify({ id, method, params })); });
const evalJS = async (expression) => { const r = await send('Runtime.evaluate', { expression, awaitPromise: true, returnByValue: true }); if (r.exceptionDetails) throw new Error(r.exceptionDetails.text + ' ' + JSON.stringify(r.exceptionDetails.exception?.description)); return r.result.value; };
const goto = async (path) => { events.length = 0; await send('Page.navigate', { url: base + path }); for (let i = 0; i < 100 && !events.some((e) => e.method === 'Page.loadEventFired'); i++) await sleep(100); await sleep(300); };
const waitFor = async (expr, label, ms = 15000) => { for (let i = 0; i < ms / 100; i++) { if (await evalJS(expr)) return; await sleep(100); } throw new Error('timeout waiting for ' + label); };
const shot = async (name) => { const r = await send('Page.captureScreenshot', { format: 'png' }); writeFileSync(`/tmp/verify-shots/${name}.png`, Buffer.from(r.data, 'base64')); };
const log = (k, v) => console.log(k + ': ' + JSON.stringify(v));
const realClick = async (selector) => {
  const box = await evalJS(`(()=>{const e=document.querySelector(${JSON.stringify(selector)}); e.scrollIntoView({block:'center'}); const r=e.getBoundingClientRect(); return {x:r.x+r.width/2,y:r.y+r.height/2}})()`);
  await send('Input.dispatchMouseEvent', { type: 'mouseMoved', x: box.x, y: box.y });
  await send('Input.dispatchMouseEvent', { type: 'mousePressed', x: box.x, y: box.y, button: 'left', clickCount: 1 });
  await send('Input.dispatchMouseEvent', { type: 'mouseReleased', x: box.x, y: box.y, button: 'left', clickCount: 1 });
};
try {
  await send('Network.enable'); await send('Page.enable'); await send('Runtime.enable');
  await send('Network.setExtraHTTPHeaders', { headers: { Authorization: 'Bearer ' + token, ...(cfEmail !== '-' ? { 'Cf-Access-Authenticated-User-Email': cfEmail } : {}) } });
  await send('Network.setBlockedURLs', { urls: ['*/js/auth.js*'] });
  const feedbackBtn = `document.querySelector('[data-session-action="feedback"]')`;
  const calculate = async () => {
    await goto('/');
    await waitFor(`typeof selectAllParticipants === 'function' && !!document.getElementById('route-time')`, 'planner');
    if (!(await evalJS(`document.querySelector('select[name="activity_location_id"]')?.value`))) {
      await evalJS(`document.querySelector('button[hx-get="/api/v1/planner/location-editor"]').click(); true`);
      await waitFor(`!!document.querySelector('#route-editor input[name="location_id"]')`, 'location editor');
      await evalJS(`const c=document.querySelector('#route-editor input[name="location_id"]'); c.checked=true; c.form.requestSubmit(); true`);
      await waitFor(`!!document.querySelector('select[name="activity_location_id"]')?.value`, 'location chosen');
    }
    log('location_select', await evalJS(`(()=>{const s=document.querySelector('select[name="activity_location_id"]'); return s?{value:s.value,text:s.options[s.selectedIndex]?.text}:null})()`));
    await evalJS(`window.__hx=[]; ['htmx:confirm','htmx:beforeRequest','htmx:afterRequest','htmx:responseError','htmx:sendError','htmx:targetError','htmx:swapError'].forEach(n=>document.addEventListener(n,e=>window.__hx.push(n+' '+(e.detail?.requestConfig?.path||e.detail?.path||'')+' '+(e.detail?.xhr?.status||'')))); true`);
    await evalJS(`selectAllParticipants(); selectAllDrivers(); const t=document.getElementById('route-time'); t.value='15:30'; t.dispatchEvent(new Event('change',{bubbles:true})); true`);
    log('inert', await evalJS(`({form:document.getElementById('event-form').inert, inertAncestor:!!document.getElementById('calculate-btn').closest('[inert]')})`));
    await realClick('#calculate-btn');
    try { await waitFor(`!!document.querySelector('.routes-container')`, 'routes'); } catch (e) { log('calc_toasts', await evalJS(`[...document.querySelectorAll('.toast')].map(t=>t.textContent.trim())`)); log('results_text', await evalJS(`document.getElementById('results-section')?.innerText.slice(0,300)`)); log('diag', await evalJS(`({html:document.documentElement.className, valid:(()=>{try{return validateBeforeCalculate()}catch(e){return 'err '+e.message}})(), btnDisabled:document.getElementById('calculate-btn').disabled, participants:document.querySelectorAll('.participant-checkbox:checked').length, drivers:document.querySelectorAll('.driver-checkbox:checked').length, time:document.getElementById('route-time').value, stats:document.getElementById('selection-stats')?.textContent, htmx:typeof htmx, events:window.__hx})`)); throw e; }
    await sleep(800);
  };
  const feedbackState = () => evalJS(`(()=>{const b=${feedbackBtn}; return b?{present:true,disabled:b.disabled,title:b.title,text:b.textContent.trim()}:{present:false}})()`);
  if (scenario === 'settings') {
    await goto('/settings');
    log('settings_checkbox', await evalJS(`(()=>{const c=document.querySelector('input[name="collect_reviewer_notes"]'); return c?{present:true,checked:c.checked,label:c.closest('label')?.textContent.trim(),help:c.closest('.form-group')?.querySelector('.form-help')?.textContent}:{present:false}})()`));
    log('settings_email_field_before', await evalJS(`document.querySelector('input[name="sme_email"]')?.value`));
    await evalJS(`document.querySelector('input[name="sme_email"]').value='reviewer@example.test'; document.getElementById('settings-form').requestSubmit(); true`);
    await waitFor(`!!document.querySelector('.toast')`, 'toast');
    log('settings_toast', await evalJS(`document.querySelector('.toast')?.textContent.trim()`));
    await shot('01-settings-admin');
    await goto('/settings');
    log('settings_email_field_after_reload', await evalJS(`document.querySelector('input[name="sme_email"]')?.value`));
  } else if (scenario === 'toggle-off' || scenario === 'toggle-on') {
    await goto('/settings');
    await evalJS(`document.querySelector('input[name="collect_reviewer_notes"]').checked=${scenario === 'toggle-on'}; document.getElementById('settings-form').requestSubmit(); true`);
    await waitFor(`!!document.querySelector('.toast')`, 'toast');
    log('toggle_toast', await evalJS(`document.querySelector('.toast')?.textContent.trim()`));
    await goto('/settings');
    log('checkbox_after', await evalJS(`document.querySelector('input[name="collect_reviewer_notes"]').checked`));
  } else if (scenario === 'button-only') {
    await calculate();
    log('feedback_button', await feedbackState());
    await shot(`02-button-only-${cfEmail.replace(/[^a-z]/g, '')}`);
  } else if (scenario === 'flow') {
    await calculate();
    log('button_before_edit', await feedbackState());
    await shot('03-calculated');
    await evalJS(`${feedbackBtn}.removeAttribute('disabled'); true`); await realClick('[data-session-action="feedback"]');
    await waitFor(`!!document.getElementById('route-feedback-title')`, 'dialog');
    log('dialog_before_edit_items', await evalJS(`[...document.querySelectorAll('.route-feedback-changes li')].map(l=>l.textContent.trim())`));
    await evalJS(`closeRouteEditor(); true`);
    const moveLabel = await evalJS(`document.querySelector('[data-editor-url*="action=move"]').getAttribute('aria-label')`);
    log('move_button', moveLabel);
    await realClick('[data-editor-url*="action=move"]');
    await waitFor(`!!document.querySelector('#route-editor input[name="destination"]')`, 'editor');
    log('move_choice', await evalJS(`(()=>{const c=document.querySelector('#route-editor input[name="destination"]'); c.checked=true; return c.closest('label').textContent.trim()})()`));
    await realClick('#route-editor form[method="post"] button[type="submit"]');
    await waitFor(`${feedbackBtn} && !${feedbackBtn}.disabled`, 'feedback enabled');
    await sleep(500);
    log('button_after_edit', await feedbackState());
    log('reset_button_present', await evalJS(`!!document.querySelector('[data-session-action="edit"][onclick*="resetRoutes"]')`));
    await shot('04-after-move');
    await realClick('[data-session-action="feedback"]');
    await waitFor(`!!document.getElementById('route-feedback-title') && document.getElementById('route-editor-dialog').open`, 'dialog');
    log('dialog_items', await evalJS(`[...document.querySelectorAll('.route-feedback-changes li')].map(l=>l.textContent.trim())`));
    log('dialog_buttons', await evalJS(`[...document.querySelectorAll('.route-feedback-actions button')].map(b=>b.textContent.trim())`));
    log('dialog_note_initial', await evalJS(`document.getElementById('route-feedback-note').value`));
    await evalJS(`document.getElementById('route-feedback-note').value='Pat lives on the same street as the second driver.'; true`);
    await shot('05-dialog-filled');
    await realClick('.route-feedback-actions button[type="submit"]');
    await waitFor(`!document.getElementById('route-editor-dialog').open`, 'dialog closed');
    await waitFor(`[...document.querySelectorAll('.toast')].some(t=>t.textContent.includes('Feedback'))`, 'feedback toast');
    log('submit_toast', await evalJS(`[...document.querySelectorAll('.toast')].map(t=>t.textContent.trim())`));
    await shot('06-after-submit');
    await realClick('[data-session-action="feedback"]');
    await waitFor(`document.getElementById('route-editor-dialog').open && document.getElementById('route-feedback-note')?.value.length>0`, 'reopened');
    log('dialog_note_reopened', await evalJS(`document.getElementById('route-feedback-note').value`));
    await evalJS(`document.getElementById('route-feedback-note').value='DISCARD ME'; true`); await realClick('.route-feedback-actions button:first-child');
    await waitFor(`!document.getElementById('route-editor-dialog').open`, 'closed after not now');
    await realClick('[data-session-action="feedback"]');
    await waitFor(`document.getElementById('route-editor-dialog').open && !!document.getElementById('route-feedback-note')`, 'reopened 2');
    log('dialog_note_after_not_now', await evalJS(`document.getElementById('route-feedback-note').value`));
    await evalJS(`closeRouteEditor(); true`);
    log('session_id', await evalJS(`document.querySelector('.routes-container').dataset.sessionId`));
    await evalJS(`document.querySelectorAll('.toast').forEach(t=>t.remove()); document.getElementById('save-event-notes').value='verify run'; true`); await realClick('#save-event-panel button[type="submit"]');
    await waitFor(`document.getElementById('save-result')?.textContent.trim().length>0 || [...document.querySelectorAll('.toast')].some(t=>/Event saved|saved the event/i.test(t.textContent))`, 'save result');
    await sleep(500);
    log('save_result', await evalJS(`document.getElementById('save-result')?.textContent.trim()`));
    log('save_toasts', await evalJS(`[...document.querySelectorAll('.toast')].map(t=>t.textContent.trim())`));
    log('button_after_save', await feedbackState());
    await shot('07-after-save');
  }
} catch (e) { console.log('ERROR: ' + e.message); await shot('error-' + scenario).catch(() => {}); process.exitCode = 1; }
ws.close(); chrome.kill();
