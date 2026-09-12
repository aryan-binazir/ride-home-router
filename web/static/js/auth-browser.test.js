'use strict';
const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const { execFileSync } = require('node:child_process');
const { pathToFileURL } = require('node:url');

const browser = process.env.BROWSER_TEST_BINARY;
// Opt in to real DOM events and native requestSubmit without packages or a server.
for (const outcome of ['token', 'missing', 'rejected']) {
    for (const submitter of [true, false]) {
        test(`mobile auth refresh=${outcome}, explicit submitter=${submitter}`, { skip: !browser }, () => {
            const directory = fs.mkdtempSync(path.join(os.tmpdir(), 'rhr-auth-browser-'));
            try {
                const source = name => fs.readFileSync(path.join(__dirname, name), 'utf8');
                const fixture = `<!doctype html><html><head>
<meta http-equiv="Content-Security-Policy" content="form-action 'none'; connect-src 'none'">
</head><body><main class="mobile-shell">
<form id="mobile-driver-picker" method="post" action="about:blank">
<input name="notes" value="Unsent pickup details">
<div class="mobile-driver-choice"><input type="checkbox" name="driver_ids" value="1" checked><select name="org_vehicle_1"><option value="">Personal</option><option value="9" selected>Van</option></select></div>
<div class="mobile-driver-choice"><input type="checkbox" name="driver_ids" value="2"><select name="org_vehicle_2"><option value="">Personal</option><option value="8" selected>Bus</option></select></div>
<div class="mobile-driver-choice"><input type="checkbox" name="driver_ids" value="3" checked><select name="org_vehicle_3"><option value="">Personal</option></select></div>
<button id="save" type="submit" name="intent" value="save">Save</button>
<button type="submit" name="intent" value="other">Other</button>
</form></main><pre id="evidence">pending</pre>
<script>
const evidence = {final: [], refreshCalls: 0, fetches: [], errors: [], navigations: 0};
const originalURL = location.href;
const nativeRequestSubmit = HTMLFormElement.prototype.requestSubmit;
window.addEventListener('beforeunload', () => evidence.navigations++);
window.addEventListener('error', event => evidence.errors.push(event.message));
window.addEventListener('unhandledrejection', event => evidence.errors.push(String(event.reason)));
const form = document.querySelector('form');
const button = document.getElementById('save');
const fields = () => Array.from(form.elements, input => ({name: input.name, value: input.value, checked: input.checked}));
const controls = () => Array.from(form.querySelectorAll('select'), input => input.disabled);
const initialFields = fields();
// The injected SDK loads as a real script; only configuration and Clerk are synthetic.
window.Clerk = {
 load: async () => { setTimeout(run, 0); },
 session: {getToken: options => {
  evidence.refreshCalls++;
  evidence.options = options;
  return new Promise((resolve, reject) => setTimeout(() => {
   evidence.refreshSettled = true;
   if (${JSON.stringify(outcome)} === 'rejected') reject(new Error('offline'));
   else resolve(${JSON.stringify(outcome)} === 'token' ? 'synthetic-token' : null);
  }, 40));
 }}
};
window.fetch = async url => {
 evidence.fetches.push(url);
 if (url !== '/auth/config') throw new Error('Unexpected network request');
 return {ok: true, json: async () => ({scriptURL: 'data:text/javascript,window.syntheticSDKLoaded%3Dtrue', publishableKey: 'synthetic'})};
};
// Runs only after auth permits propagation. Prevent navigation before recording data.
form.addEventListener('submit', event => {
 event.preventDefault();
 evidence.final.push({
  settled: evidence.refreshSettled === true,
  submitter: event.submitter?.id || null,
  data: Array.from(new FormData(form, event.submitter)),
  disabled: controls()
 });
});
function run() {
 try {
  evidence.sdkLoaded = window.syntheticSDKLoaded === true;
  evidence.nativeUnchanged = form.requestSubmit === nativeRequestSubmit;
  if (${submitter}) form.requestSubmit(button);
  else form.requestSubmit();
  evidence.beforeRefresh = {final: evidence.final.length, disabled: controls(), fields: fields()};
  // A second attempt during refresh must not start another refresh or final POST.
  if (${submitter}) form.requestSubmit(button);
  else form.requestSubmit();
  setTimeout(() => {
   const link = document.querySelector('[role="alert"] a');
   evidence.fieldsUnchanged = JSON.stringify(fields()) === JSON.stringify(initialFields);
   evidence.urlUnchanged = location.href === originalURL;
   evidence.recovery = link ? {
    href: link.getAttribute('href'), target: link.target, rel: link.rel,
    visible: link.getClientRects().length > 0 && getComputedStyle(link).visibility === 'visible',
    text: link.parentElement.textContent
   } : null;
   document.getElementById('evidence').textContent = JSON.stringify(evidence);
  }, 150);
 } catch (error) {
  document.getElementById('evidence').textContent = JSON.stringify({error: String(error)});
 }
}
</script>
<script>${source('mobile.js')}</script>
<script>${source('auth.js')}</script>
</body></html>`;
                const file = path.join(directory, 'fixture.html');
                fs.writeFileSync(file, fixture);
                const output = execFileSync(browser, [
                    '--headless', '--no-sandbox', '--disable-gpu', '--no-first-run',
                    `--user-data-dir=${directory}/profile`, '--dump-dom',
                    '--virtual-time-budget=2000', pathToFileURL(file).href,
                ], { encoding: 'utf8', timeout: 60000, stdio: ['ignore', 'pipe', 'pipe'], maxBuffer: 2 * 1024 * 1024 });
                const serialized = output.match(/<pre id="evidence">(.*?)<\/pre>/s)?.[1];
                assert.ok(serialized && serialized !== 'pending', 'browser must finish the scenario without navigating');
                const result = JSON.parse(serialized);
                assert.equal(result.error, undefined);
                assert.deepEqual(result.errors, []);
                assert.equal(result.sdkLoaded, true);
                assert.equal(result.nativeUnchanged, true);
                assert.deepEqual(result.fetches, ['/auth/config']);
                assert.equal(result.refreshCalls, 1, 'pending attempts must share one refresh');
                assert.deepEqual(result.options, { skipCache: true });
                assert.equal(result.beforeRefresh.final, 0, 'no final submit before the asynchronous refresh');
                assert.deepEqual(result.beforeRefresh.disabled, [false, true, true], 'mobile capture handler runs before auth');
                assert.equal(result.fieldsUnchanged, true, 'entered values and selections survive refresh');
                assert.equal(result.urlUnchanged, true);
                assert.equal(result.navigations, 0);
                assert.equal(result.refreshSettled, true);
                if (outcome === 'token') {
                    assert.deepEqual(result.final, [{
                        settled: true,
                        submitter: submitter ? 'save' : null,
                        data: [
                            ['notes', 'Unsent pickup details'], ['driver_ids', '1'],
                            ['org_vehicle_1', '9'], ['driver_ids', '3'],
                            ...(submitter ? [['intent', 'save']] : []),
                        ],
                        disabled: [false, true, true],
                    }], 'exactly one final request retains the submitter and only eligible vehicle fields');
                    assert.equal(result.recovery, null);
                } else {
                    assert.deepEqual(result.final, [], 'failed refresh must never reach final submission');
                    assert.equal(result.recovery?.href, '/sign-in');
                    assert.equal(result.recovery.target, '_blank');
                    assert.equal(result.recovery.rel, 'noopener');
                    assert.equal(result.recovery.visible, true);
                    assert.match(result.recovery.text, /Your form has not been submitted/);
                }
            } finally {
                fs.rmSync(directory, { recursive: true, force: true });
            }
        });
    }
}
