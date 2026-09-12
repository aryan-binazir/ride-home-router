'use strict';
const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const {execFileSync} = require('node:child_process');
const {pathToFileURL} = require('node:url');
const browser = process.env.BROWSER_TEST_BINARY;

function run(body, assets, scenario) {
    const directory = fs.mkdtempSync(path.join(os.tmpdir(), 'rhr-feedback-'));
    try {
        const file = path.join(directory, 'fixture.html');
        fs.writeFileSync(file, `<!doctype html><html><body>${body}<pre id="evidence">pending</pre>
<script>const errors=[];window.addEventListener('error',e=>errors.push(e.message));</script>
${assets.map(name => `<script>${fs.readFileSync(path.join(__dirname, name), 'utf8')}</script>`).join('')}
<script>document.addEventListener('DOMContentLoaded', async()=>{try {${scenario}} catch(e) {document.getElementById('evidence').textContent=JSON.stringify({error:String(e)});}});</script></body></html>`);
        const output = execFileSync(browser, ['--headless', '--no-sandbox', '--disable-gpu', '--no-first-run', `--user-data-dir=${directory}/profile`, '--dump-dom', '--virtual-time-budget=2000', pathToFileURL(file).href], {encoding: 'utf8', timeout: 60000, stdio: ['ignore', 'pipe', 'pipe']});
        const result = JSON.parse(output.match(/<pre id="evidence">(.*?)<\/pre>/s)?.[1] || 'null');
        assert.ok(result);
        assert.equal(result.error, undefined);
        assert.deepEqual(result.errors, []);
        return result;
    } finally { fs.rmSync(directory, {recursive: true, force: true}); }
}

test('mobile seat count follows riders, vans and filtered hidden selections', {skip: !browser}, () => {
    const result = run(`<span id="mobile-selected-seats">7 seats selected</span><form id="mobile-driver-picker"><div id="mobile-driver-results"><div class="mobile-driver-choice"><input id="driver" type="checkbox" name="driver_ids" data-capacity="4" checked><select><option data-capacity="4">Personal</option><option data-capacity="8">Van</option></select></div></div></form>`, ['mobile.js'], `
const values=[];const select=document.querySelector('select');select.selectedIndex=1;select.dispatchEvent(new Event('change',{bubbles:true}));values.push(document.getElementById('mobile-selected-seats').textContent);
document.getElementById('driver').click();values.push(document.getElementById('mobile-selected-seats').textContent);
document.getElementById('mobile-driver-results').innerHTML='<div class="mobile-driver-choice"><input id="next" type="checkbox" name="driver_ids" data-capacity="3" checked></div>';
document.dispatchEvent(new CustomEvent('htmx:afterSwap'));document.getElementById('next').click();values.push(document.getElementById('mobile-selected-seats').textContent);
document.getElementById('evidence').textContent=JSON.stringify({values,errors});`);
    assert.deepEqual(result.values, ['11 seats selected', '3 seats selected', '0 seats selected']);
});

test('import polling preserves changed choices and persists them after replacement', {skip: !browser}, () => {
    const result = run(`<div id="import-steps"><form id="import-selection-form" hx-put="/selection"><input name="selected" type="checkbox" value="1"><input name="selected" type="checkbox" value="2" checked></form></div>`, ['ui.js'], `
let persisted=0;window.htmx={trigger(form,name){if(name==='change'&&form.isConnected)persisted++;}};
const detail={target:document.getElementById('import-steps'),shouldSwap:true,serverResponse:'<form id="import-selection-form" hx-put="/selection"><input type="checkbox" name="selected" value="1" checked><input type="checkbox" name="selected" value="2"><input type="checkbox" name="selected" value="3" checked></form>'};
document.dispatchEvent(new CustomEvent('htmx:beforeSwap',{detail}));detail.target.innerHTML=detail.serverResponse;document.dispatchEvent(new CustomEvent('htmx:afterSettle'));
const selected=Array.from(document.querySelectorAll('input:checked'),input=>input.value);document.getElementById('evidence').textContent=JSON.stringify({selected,persisted,errors});`);
    assert.deepEqual(result.selected, ['2', '3']);
    assert.equal(result.persisted, 1);
});

test('mobile network and HTTP failures use the visible alert without duplicating server feedback', {skip: !browser}, () => {
    const result = run('<div id="mobile-request-error" role="alert" hidden></div>', ['mobile.js'], `
const messages=[];for(const type of ['htmx:sendError','htmx:timeout','htmx:responseError']){document.dispatchEvent(new CustomEvent(type,{detail:{xhr:{status:503,getResponseHeader:()=>null}}}));messages.push(document.getElementById('mobile-request-error').textContent);}
document.dispatchEvent(new CustomEvent('showToast',{detail:{message:'Choose a driver.'}}));document.dispatchEvent(new CustomEvent('htmx:responseError',{detail:{xhr:{status:400,getResponseHeader:()=>'{"showToast":{}}'}}}));messages.push(document.getElementById('mobile-request-error').textContent);
document.getElementById('evidence').textContent=JSON.stringify({messages,visible:!document.getElementById('mobile-request-error').hidden,errors});`);
    assert.equal(result.visible, true);
    assert.deepEqual(result.messages, ['Could not reach the server. Check your connection and try again.', 'Could not reach the server. Check your connection and try again.', 'The service is temporarily unavailable. Try again in a minute.', 'Choose a driver.']);
});

for (const template of ['activity_locations.html', 'vans.html']) {
    test(`${template} keeps entered values after failed saves`, {skip: !browser}, () => {
        const source = fs.readFileSync(path.join(__dirname, '../../templates', template), 'utf8');
        const handler = source.match(/hx-on::after-request="([^"]+)"/)[1];
        const result = run('<form><input value="Unsent place"><input value="123 Main St"></form>', [], `
const form=document.querySelector('form');let resets=0;form.reset=()=>resets++;
const handler=new Function('event',${JSON.stringify(handler)});
for(const detail of [{successful:false,elt:form,requestConfig:{verb:'post'}},{successful:true,elt:form.querySelector('input'),requestConfig:{verb:'post'}}])handler.call(form,{detail});
const before=resets;handler.call(form,{detail:{successful:true,elt:form,requestConfig:{verb:'post'}}});
document.getElementById('evidence').textContent=JSON.stringify({before,resets,errors});`);
        assert.equal(result.before, 0);
        assert.equal(result.resets, 1);
    });
}

test('completed import refreshes roster content while preserving restore bindings', {skip: !browser}, () => {
    const resultTemplate = fs.readFileSync(path.join(__dirname, '../../templates/partials/import_result.html'), 'utf8');
    const oob = resultTemplate.match(/hx-swap-oob="([^"]+)"/)[1];
    const result = run('<div id="participants-list" hx-get="/api/v1/participants" hx-trigger="rosterRestored from:body" hx-swap="innerHTML">Old roster</div><div id="import-steps"></div>', ['htmx.min.js'], `
const original=document.getElementById('participants-list');htmx.swap(document.getElementById('import-steps'),'<div id="participants-list" hx-swap-oob="${oob}">Imported roster</div>',{swapStyle:'innerHTML'});
const roster=document.getElementById('participants-list');document.getElementById('evidence').textContent=JSON.stringify({same:original===roster,content:roster.textContent,trigger:roster.getAttribute('hx-trigger'),errors});`);
    assert.equal(result.same, true);
    assert.equal(result.content, 'Imported roster');
    assert.equal(result.trigger, 'rosterRestored from:body');
});

test('mobile reset confirms once, blocks repeats and restores controls on return', {skip: !browser}, () => {
    const result = run(`<main class="mobile-shell"><form method="post" action="/m/routes/reset" data-confirm="Reset changes?"><button type="submit" name="intent" value="reset">Reset</button></form></main>`, ['ui.js','mobile.js'], `
let submitted=0;let intent;window.addEventListener('submit',event=>{if(event.defaultPrevented)return;event.preventDefault();submitted++;intent=new FormData(event.target).get('intent');});
const form=document.querySelector('form');const button=form.querySelector('button');form.requestSubmit(button);document.querySelector('[data-confirm-action="cancel"]').click();await Promise.resolve();const cancelled=submitted;
form.requestSubmit(button);document.querySelector('[data-confirm-action="confirm"]').click();await Promise.resolve();form.requestSubmit(button);const pending=button.disabled;
window.dispatchEvent(new PageTransitionEvent('pageshow',{persisted:true}));document.getElementById('evidence').textContent=JSON.stringify({cancelled,submitted,intent,pending,restored:!button.disabled,errors});`);
    assert.deepEqual({...result, errors: undefined}, {cancelled:0, submitted:1, intent:'reset', pending:true, restored:true, errors:undefined});
});

test('Calculate validation blocks the actual htmx trigger and duplicate requests', {skip: !browser}, () => {
    const index = fs.readFileSync(path.join(__dirname, '../../templates/index.html'), 'utf8');
    const button = index.match(/<button\b[^>]*id="calculate-btn"[^>]*>[\s\S]*?<\/button>/)[0];
    const result = run(`<form id="event-form"></form><div id="results-section"></div>${button}`, ['htmx.min.js'], `
let requests=0;window.validateBeforeCalculate=()=>false;window.XMLHttpRequest=class {constructor(){this.upload={addEventListener(){}};}open(){}setRequestHeader(){}overrideMimeType(){}addEventListener(){}send(){requests++;}};
htmx.config.selfRequestsOnly=false;const button=document.getElementById('calculate-btn');button.click();const rejected=requests;window.validateBeforeCalculate=()=>true;button.click();button.click();document.getElementById('evidence').textContent=JSON.stringify({rejected,requests,disabled:button.disabled,errors});`);
    assert.deepEqual(result, {rejected:0, requests:1, disabled:true, errors:[]});
});
