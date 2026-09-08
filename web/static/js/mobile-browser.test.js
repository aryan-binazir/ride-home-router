'use strict';
const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const { execFileSync } = require('node:child_process');
const { pathToFileURL } = require('node:url');

const browser = process.env.BROWSER_TEST_BINARY;
// This opt-in check needs a browser, but no npm packages or running app server.
for (const kind of ['rider', 'driver']) {
    for (const hidden of [false, true]) {
        test(`mobile ${kind} filter keeps edits made while loading, hidden=${hidden}`, { skip: !browser }, () => {
            const directory = fs.mkdtempSync(path.join(os.tmpdir(), 'rhr-browser-'));
            try {
                const template = fs.readFileSync(path.join(__dirname, '../../templates/mobile', `${kind}s.html`), 'utf8');
                const stripTemplate = value => value.replace(/{{.*?}}/gs, '');
                const form = stripTemplate(template.match(/<form\b[^>]*>/)[0]);
                const search = stripTemplate(template.match(/<input type="search"[\s\S]*?>/)[0]);
                const radio = stripTemplate(template.match(/<input class="mobile-chip-input"[\s\S]*?>/)[0]);
                const name = kind === 'driver' ? 'driver_ids' : 'participant_ids';
                const row = (id, checked = false) => `<div class="mobile-driver-choice"><input id="person-${id}" type="checkbox" name="${name}" value="${id}" ${checked ? 'checked' : ''}><select name="org_vehicle_${id}"><option value="">Personal</option><option value="9">Van</option></select></div>`;
                const response = `<div id="mobile-${kind}-results">${hidden ? row(3) : row(1) + row(2, true)}</div>`;
                const fixture = `<!doctype html><html><body>${form}${search}${radio}<div id="mobile-${kind}-results">${row(1)}${row(2, true)}</div><button type="submit">Done</button></form><pre id="evidence">pending</pre>
<script>
const evidence={aborted:0, swaps:0}; let requests=0;
class FakeXHR {
 constructor(){this.upload={addEventListener(){}};this.responseURL='file:///m/plan/${kind}s';this.status=200;this.readyState=4;}
 open(){} setRequestHeader(){} overrideMimeType(){} addEventListener(){}
 abort(){this.aborted=true;evidence.aborted++;this.onabort?.();}
 getAllResponseHeaders(){return 'Content-Type: text/html';}
 getResponseHeader(name){return name.toLowerCase()==='content-type'?'text/html':null;}
 send(){const number=++requests;
  if(number===1){setTimeout(()=>document.querySelector('input[name="label"]').dispatchEvent(new Event('change',{bubbles:true})),10);}
  if(number===2){setTimeout(()=>{
   document.querySelector('#person-1').click();document.querySelector('#person-2').click();
   const van=document.querySelector('select[name="org_vehicle_1"]');van.value='9';van.dispatchEvent(new Event('change',{bubbles:true}));
  },10);}
  setTimeout(()=>{if(this.aborted)return;this.response=this.responseText=${JSON.stringify(response)};this.onload();},number===1?200:40);
 }
}
window.XMLHttpRequest=FakeXHR;
</script><script>${fs.readFileSync(path.join(__dirname, 'htmx.min.js'), 'utf8')}</script><script>${fs.readFileSync(path.join(__dirname, 'mobile.js'), 'utf8')}</script><script>
document.addEventListener('htmx:afterSwap',()=>{evidence.swaps++;const data=new FormData(document.querySelector('form'));evidence.selected=data.getAll('${name}');evidence.van=data.get('org_vehicle_1');document.querySelector('#evidence').textContent=JSON.stringify(evidence);});
document.addEventListener('DOMContentLoaded',()=>{htmx.config.selfRequestsOnly=false;const search=document.querySelector('input[type="search"]');search.value='new';search.dispatchEvent(new Event('input',{bubbles:true}));});
</script></body></html>`;
                const file = path.join(directory, 'fixture.html');
                fs.writeFileSync(file, fixture);
                const output = execFileSync(browser, ['--headless', '--no-sandbox', '--disable-gpu', '--no-first-run', `--user-data-dir=${directory}/profile`, '--dump-dom', '--virtual-time-budget=2000', pathToFileURL(file).href], { encoding: 'utf8', timeout: 20000, stdio: ['ignore', 'pipe', 'pipe'], maxBuffer: 2 * 1024 * 1024 });
                const result = JSON.parse(output.match(/<pre id="evidence">(.*?)<\/pre>/s)?.[1] || 'null');
                assert.deepEqual(result?.selected, ['1'], 'the latest selection must survive replacing filtered controls');
                if (kind === 'driver') assert.equal(result.van, '9');
                assert.equal(result.aborted, 1, 'a newer filter must cancel the older request');
                assert.equal(result.swaps, 1);
            } finally {
                fs.rmSync(directory, { recursive: true, force: true });
            }
        });
    }
}
