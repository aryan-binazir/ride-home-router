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
    const result = run(`<span id="mobile-selected-seats" data-seats="7">7 seats selected</span><form id="mobile-driver-picker"><div id="mobile-driver-results"><div class="mobile-driver-choice"><input id="driver" type="checkbox" name="driver_ids" data-capacity="4" checked><select><option value="" data-capacity="4">Personal</option><option value="9" data-capacity="8">Van</option></select></div></div></form>`, ['mobile.js'], `
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

test('repeated upload submissions are dropped instead of queued', {skip: !browser}, () => {
    const template = fs.readFileSync(path.join(__dirname, '../../templates/partials/import_panel.html'), 'utf8');
    const form = template.match(/<form[\s\S]*?>/)[0];
    const result = run(`${form}<button type="submit">Upload</button></form><div id="import-steps"></div>`, ['htmx.min.js'], `
let requests=0;window.XMLHttpRequest=class {constructor(){this.upload={addEventListener(){}};this.status=200;this.readyState=4;this.responseURL='file:///api/v1/imports';}open(){}setRequestHeader(){}overrideMimeType(){}addEventListener(){}getAllResponseHeaders(){return 'Content-Type: text/html';}getResponseHeader(){return null;}send(){requests++;setTimeout(()=>{this.response=this.responseText='<p>Uploaded</p>';this.onload();},20);}};
htmx.config.selfRequestsOnly=false;const form=document.querySelector('form');form.requestSubmit();form.requestSubmit();await new Promise(resolve=>setTimeout(resolve,100));document.getElementById('evidence').textContent=JSON.stringify({requests,errors});`);
    assert.equal(result.requests,1);
});

test('mobile counts a shared van once and accounts for restored checkbox state', {skip: !browser}, () => {
    const result = run(`<span id="mobile-selected-seats" data-seats="4">4 seats selected</span><form id="mobile-driver-picker">${[1,2].map(id=>`<div class="mobile-driver-choice"><input type="checkbox" name="driver_ids" data-capacity="4" ${id===1?'checked':''}><select><option value="" data-capacity="4">Personal</option><option value="9" data-capacity="8">Van</option></select></div>`).join('')}</form><script>document.querySelectorAll('input')[1].checked=true;</script>`, ['mobile.js'], `
const restored=document.getElementById('mobile-selected-seats').textContent;for(const select of document.querySelectorAll('select')){select.value='9';select.dispatchEvent(new Event('change',{bubbles:true}));}document.getElementById('evidence').textContent=JSON.stringify({restored,total:document.getElementById('mobile-selected-seats').textContent,errors});`);
    assert.equal(result.restored,'8 seats selected');
    assert.equal(result.total,'12 seats selected');
});

test('failed auth renewal clears a pending mobile reset confirmation', {skip: !browser}, () => {
    const result = run('<main class="mobile-shell"><form method="post" action="/m/routes/reset" data-confirm="Reset changes?"><button type="submit">Reset</button></form></main>', ['ui.js','mobile.js'], `
let blockReplay=false;document.addEventListener('submit',event=>{if(blockReplay){blockReplay=false;event.preventDefault();event.stopImmediatePropagation();event.target.dispatchEvent(new Event('auth:submitFailed',{bubbles:true}));}},true);
const form=document.querySelector('form');form.requestSubmit();blockReplay=true;document.querySelector('[data-confirm-action="confirm"]').click();await Promise.resolve();form.requestSubmit();document.getElementById('evidence').textContent=JSON.stringify({open:document.querySelector('.confirm-overlay').classList.contains('is-open'),errors});`);
    assert.equal(result.open,true);
});

test('import polls preserve choices without retrying a failed selection write', {skip: !browser}, () => {
    const result = run('<div id="import-steps"><form id="import-selection-form" hx-put="/selection"><input name="selected" type="checkbox" value="1"></form></div>', ['ui.js'], `
let writes=0;window.htmx={trigger(){writes++;}};const target=document.getElementById('import-steps');
document.dispatchEvent(new CustomEvent('htmx:afterRequest',{detail:{elt:document.querySelector('form'),successful:false,xhr:{status:503}}}));
for(let i=0;i<2;i++){const detail={target,shouldSwap:true,serverResponse:'<form id="import-selection-form" hx-put="/selection"><input name="selected" type="checkbox" value="1" checked></form>'};document.dispatchEvent(new CustomEvent('htmx:beforeSwap',{detail}));target.innerHTML=detail.serverResponse;document.dispatchEvent(new CustomEvent('htmx:afterSettle'));}
document.getElementById('evidence').textContent=JSON.stringify({writes,checked:document.querySelector('input').checked,errors});`);
    assert.equal(result.writes,0);
    assert.equal(result.checked,false);
});

test('import polls preserve choices without retrying a legacy HTTP 200 selection error', {skip: !browser}, () => {
    const result = run('<div id="import-steps"><form id="import-selection-form" hx-put="/selection"><input name="selected" type="checkbox" value="1"></form></div>', ['ui.js'], `
let writes=0;window.htmx={trigger(){writes++;}};const target=document.getElementById('import-steps');
document.dispatchEvent(new CustomEvent('htmx:afterRequest',{detail:{elt:document.querySelector('form'),successful:true,xhr:{status:200,getResponseHeader:()=>JSON.stringify({showToast:{type:"error"}})}}}));
for(let i=0;i<2;i++){const detail={target,shouldSwap:true,serverResponse:'<form id="import-selection-form" hx-put="/selection"><input name="selected" type="checkbox" value="1" checked></form>'};document.dispatchEvent(new CustomEvent('htmx:beforeSwap',{detail}));target.innerHTML=detail.serverResponse;document.dispatchEvent(new CustomEvent('htmx:afterSettle'));}
document.getElementById('evidence').textContent=JSON.stringify({writes,checked:document.querySelector('input').checked,errors});`);
    assert.equal(result.writes,0);
    assert.equal(result.checked,false);
});


test('mobile driver picker restores van controls on pageshow', {skip: !browser}, () => {
    const result = run('<main class="mobile-shell"><form id="mobile-driver-picker" method="post"><div class="mobile-driver-choice"><input type="checkbox" name="driver_ids" value="1"><select name="org_vehicle_1"><option value="">Personal</option><option value="9">Van</option></select></div><button type="submit">Done</button></form></main>', ['mobile.js'], `
window.addEventListener('submit',event=>event.preventDefault());const form=document.querySelector('form');form.requestSubmit();const during=document.querySelector('select').disabled;
window.dispatchEvent(new PageTransitionEvent('pageshow',{persisted:true}));document.querySelector('input').click();document.getElementById('evidence').textContent=JSON.stringify({during,restored:!document.querySelector('select').disabled,errors});`);
    assert.equal(result.during,true);
    assert.equal(result.restored,true);
});

test('a new import checkbox edit permits persistence after an earlier failed write', {skip: !browser}, () => {
    const result = run('<div id="import-steps"><form id="import-selection-form" hx-put="/selection"><input name="selected" type="checkbox" value="1"></form></div>', ['ui.js'], `
let writes=0;window.htmx={trigger(){writes++;}};const target=document.getElementById('import-steps');
document.dispatchEvent(new CustomEvent('htmx:afterRequest',{detail:{elt:document.querySelector('form'),successful:false}}));
document.querySelector('input').click();
const detail={target,shouldSwap:true,serverResponse:'<form id="import-selection-form" hx-put="/selection"><input name="selected" type="checkbox" value="1"></form>'};document.dispatchEvent(new CustomEvent('htmx:beforeSwap',{detail}));target.innerHTML=detail.serverResponse;document.dispatchEvent(new CustomEvent('htmx:afterSettle'));
document.getElementById('evidence').textContent=JSON.stringify({writes,checked:document.querySelector('input').checked,errors});`);
    assert.equal(result.writes,1);
    assert.equal(result.checked,true);
});

test('mobile filter swap retains a driver selected while the response was in flight', {skip: !browser}, () => {
    const row='<div class="mobile-driver-choice"><input name="driver_ids" type="checkbox" value="1" data-capacity="4"></div>';
    const result = run('<span id="mobile-selected-seats" data-seats="0"></span><form id="mobile-driver-picker"><div id="mobile-driver-results">'+row+'</div></form>', ['mobile.js'], `
document.querySelector('input').click();const target=document.getElementById('mobile-driver-results');const detail={target,shouldSwap:true,serverResponse:${JSON.stringify('<div id="mobile-driver-results">'+row+'</div>')}};
document.dispatchEvent(new CustomEvent('htmx:beforeSwap',{detail}));target.outerHTML=detail.serverResponse;document.dispatchEvent(new CustomEvent('htmx:afterSwap'));
document.getElementById('evidence').textContent=JSON.stringify({checked:document.querySelector('input').checked,seats:document.getElementById('mobile-selected-seats').textContent,errors});`);
    assert.equal(result.checked,true);
    assert.equal(result.seats,'4 seats selected');
});


test('filtered duplicate van assignment becomes an accurate valid total after deselection', {skip: !browser}, () => {
    const row=(id,capacity)=>`<div class="mobile-driver-choice"><input name="driver_ids" type="checkbox" value="${id}" data-capacity="${capacity}" checked><select name="org_vehicle_${id}"><option value="" data-capacity="${capacity}">Personal</option><option value="9" data-capacity="8">Van</option></select></div>`;
    const result = run('<span id="mobile-selected-seats" data-seats="6"></span><form id="mobile-driver-picker"><div id="mobile-driver-results">'+row(1,4)+row(2,2)+'</div></form>', ['mobile.js'], `
for(const select of document.querySelectorAll('select')){select.value='9';select.dispatchEvent(new Event('change',{bubbles:true}));}
const totals=[document.getElementById('mobile-selected-seats').textContent];
const target=document.getElementById('mobile-driver-results');const detail={target,shouldSwap:true,serverResponse:${JSON.stringify('<div id="mobile-driver-results">'+row(1,4)+'</div>')}};
document.dispatchEvent(new CustomEvent('htmx:beforeSwap',{detail}));target.outerHTML=detail.serverResponse;document.dispatchEvent(new CustomEvent('htmx:afterSwap'));
totals.push(document.getElementById('mobile-selected-seats').textContent);document.querySelector('input[type="checkbox"]').click();totals.push(document.getElementById('mobile-selected-seats').textContent);
document.getElementById('evidence').textContent=JSON.stringify({totals,errors});`);
    assert.deepEqual(result.totals,['10 seats selected','10 seats selected','8 seats selected']);
});

test('shared drawer traps keyboard focus and closes without moving page content', {skip: !browser}, () => {
    const css = fs.readFileSync(path.join(__dirname, '../css/style.css'), 'utf8');
    const result = run(`<style>${css}</style><header class="appbar"><button class="nav-toggle" aria-expanded="false">Menu</button><div class="nav-backdrop" hidden></div><nav class="nav" id="primary-navigation"><button class="nav-close">Close</button><a href="/participants">Participants</a><button data-sign-out>Sign out</button></nav></header><main><h1>Drivers</h1></main>`, ['navigation.js'], `
const toggle=document.querySelector('.nav-toggle'),nav=document.querySelector('.nav'),close=document.querySelector('.nav-close'),backdrop=document.querySelector('.nav-backdrop');
const top=document.querySelector('h1').getBoundingClientRect().top;
toggle.click();
await new Promise(requestAnimationFrame);
const opened=nav.classList.contains('is-open')&&!backdrop.hidden&&document.activeElement===close;
close.dispatchEvent(new KeyboardEvent('keydown',{key:'Tab',shiftKey:true,bubbles:true,cancelable:true}));
const trapped=document.activeElement===document.querySelector('[data-sign-out]');
document.dispatchEvent(new KeyboardEvent('keydown',{key:'Escape',bubbles:true}));
const escaped=backdrop.hidden&&document.activeElement===toggle;
toggle.click();backdrop.click();
document.getElementById('evidence').textContent=JSON.stringify({opened,trapped,escaped,closed:backdrop.hidden&&!nav.classList.contains('is-open'),shift:document.querySelector('h1').getBoundingClientRect().top-top,errors});`);
    assert.deepEqual(result, {opened:true, trapped:true, escaped:true, closed:true, shift:0, errors:[]});
});

test('late authentication failure does not shift shared page content', {skip: !browser}, () => {
    const css = fs.readFileSync(path.join(__dirname, '../css/style.css'), 'utf8');
    const result = run(`<style>${css}</style><h1>Participants</h1><script>window.fetch=()=>new Promise((_,reject)=>setTimeout(()=>reject(Error('offline')),100));</script>`, ['auth.js'], `
const top=document.querySelector('h1').getBoundingClientRect().top;
await new Promise(resolve=>setTimeout(resolve,200));
document.getElementById('evidence').textContent=JSON.stringify({shift:document.querySelector('h1').getBoundingClientRect().top-top,message:document.querySelector('[role=alert]').textContent,errors});`);
    assert.equal(result.shift, 0);
    assert.match(result.message, /Could not reach the sign-in service/);
});

test('navigation items keep their positions when the active page changes', {skip: !browser}, () => {
    const css = fs.readFileSync(path.join(__dirname, '../css/style.css'), 'utf8');
    const result = run(`<style>${css}</style><header class="appbar"><a class="brand-link">Ride Home Router</a><nav class="nav"><a class="active">Event Planning</a><a>Participants</a><a>Drivers</a><a>Labels</a></nav></header>`, [], `
const links=[...document.querySelectorAll('.nav a')];
const positions=()=>links.map(e=>({x:e.getBoundingClientRect().x,width:e.getBoundingClientRect().width}));
const initial=positions();
let stable=true;
for(const active of links){for(const link of links)link.classList.toggle('active',link===active);stable=stable&&JSON.stringify(positions())===JSON.stringify(initial);}
document.getElementById('evidence').textContent=JSON.stringify({stable,errors});`);
    assert.equal(result.stable, true);
});

test('planner loading indicator does not change page geometry', {skip: !browser}, () => {
    const css = fs.readFileSync(path.join(__dirname, '../css/style.css'), 'utf8');
    const result = run(`<style>${css}</style><header class="appbar">Ride Home Router</header><main class="app-main"><div class="planner-loading" role="status"><span class="planner-spinner"></span></div><div class="main"><h1>Plan Event</h1></div></main>`, [], `
const geometry=()=>[document.querySelector('.appbar').getBoundingClientRect().toJSON(),document.querySelector('.main').getBoundingClientRect().toJSON()];
const initial=geometry();
document.documentElement.classList.add('planner-restoring');
const stable=JSON.stringify(initial)===JSON.stringify(geometry());
const hidden=getComputedStyle(document.querySelector('.main')).visibility==='hidden';
document.getElementById('evidence').textContent=JSON.stringify({stable,hidden,errors});`);
    assert.equal(result.stable, true);
    assert.equal(result.hidden, true);
});

test('settings reveal all four cards together after every section finishes loading', {skip: !browser}, () => {
    const result = run(`<script>document.documentElement.classList.add('settings-restoring');const responses={};window.authFetch=url=>new Promise(resolve=>responses[url]=resolve);window.htmx={process(){}};</script><main><section>Preferences</section><div id="google-key-slot"></div><div id="access-slot"></div><div id="admins-slot"></div></main>`, ['settings.js'], `
const pending=document.documentElement.classList.contains('settings-restoring');
responses['/api/v1/settings/google-maps-key']({ok:true,text:async()=>'<section>Google key</section>'});
for(let i=0;i<10;i++)await Promise.resolve();
const partialHidden=document.documentElement.classList.contains('settings-restoring')&&document.getElementById('google-key-slot').textContent==='';
responses['/api/v1/access']({ok:true,text:async()=>'<section>Approved emails</section>'});
responses['/api/v1/access/admins']({ok:true,text:async()=>'<section>Admins</section>'});
for(let i=0;i<15;i++)await Promise.resolve();
document.getElementById('evidence').textContent=JSON.stringify({pending,partialHidden,ready:!document.documentElement.classList.contains('settings-restoring'),cards:document.querySelectorAll('main section').length,errors});`);
    assert.deepEqual(result, {pending:true,partialHidden:true,ready:true,cards:4,errors:[]});
});

test('settings load failure keeps partial cards hidden and offers recovery', {skip: !browser}, () => {
    const result = run(`<script>document.documentElement.classList.add('settings-restoring');window.authFetch=async()=>({ok:false,status:503});</script><div id="google-key-slot"></div><div id="access-slot"></div>`, ['settings.js'], `
for(let i=0;i<15;i++)await Promise.resolve();
document.getElementById('evidence').textContent=JSON.stringify({hidden:document.documentElement.classList.contains('settings-restoring'),failed:document.documentElement.classList.contains('settings-load-failed'),errors});`);
    assert.deepEqual(result, {hidden:true,failed:true,errors:[]});
});

test('deferred settings loader waits for the authentication helper', {skip: !browser}, () => {
    const script = name => 'data:text/javascript,' + encodeURIComponent(fs.readFileSync(path.join(__dirname, name), 'utf8'));
    const result = run(`<script>document.documentElement.classList.add('settings-restoring');window.fetch=async url=>url==='/auth/config'?new Promise(()=>{}):{ok:true,text:async()=>'<section>Ready</section>'};window.htmx={process(){}};</script><script defer src="${script('settings.js')}"></script><script defer src="${script('auth.js')}"></script><div id="google-key-slot"></div><div id="access-slot"></div><div id="admins-slot"></div>`, [], `
await new Promise(resolve=>setTimeout(resolve,50));
document.getElementById('evidence').textContent=JSON.stringify({ready:!document.documentElement.classList.contains('settings-restoring'),failed:document.documentElement.classList.contains('settings-load-failed'),cards:document.querySelectorAll('section').length,errors});`);
    assert.deepEqual(result, {ready:true,failed:false,cards:3,errors:[]});
});

test('address marker dialog explains a guess, confirms it, and opens the edit form', {skip: !browser}, () => {
    const result = run(`<div id="participants-list"><table><tbody><tr id="participant-7"><td>
<button type="button" data-address-marker data-kind="participants" data-id="7" data-row="participant-7" data-match="guessed" data-address="12 Oak St Apt 4, Raliegh" data-matched="12 Oak Street, Raleigh, NC 27601">marker</button>
<button type="button" id="edit" hx-get="/api/v1/participants/7/edit">Edit</button></td></tr>
<tr id="participant-8"><td><button type="button" id="verified" data-address-marker data-kind="participants" data-id="8" data-row="participant-8" data-match="verified" data-address="1 Verified Way" data-matched="1 Verified Way, Cary, NC 27511">marker</button></td></tr></tbody></table></div>`, ['roster.js'], `
const calls=[];window.htmx={ajax(verb,url,options){calls.push({verb,url,target:options.target,swap:options.swap});}};
let edits=0;document.getElementById('edit').addEventListener('click',()=>edits++);
const text=selector=>document.querySelector(selector).textContent.trim();
const visible=selector=>!document.querySelector(selector).hidden;
document.querySelector('[data-address-marker]').click();
const dialog=document.querySelector('dialog.address-match-dialog');
const guessed={open:dialog.open,title:text('#address-match-title'),entered:text('[data-address-entered-value]'),matched:text('[data-address-matched-value]'),note:text('[data-address-note]'),enteredShown:visible('[data-address-entered]'),fix:visible('[data-address-action="fix"]'),confirm:visible('[data-address-action="confirm"]'),close:visible('[data-address-action="close"]')};
dialog.querySelector('[data-address-action="confirm"]').click();
const afterConfirm={open:dialog.open,calls:calls.slice()};
document.querySelector('[data-address-marker]').click();dialog.querySelector('[data-address-action="fix"]').click();
const afterFix={open:dialog.open,edits};
document.getElementById('verified').click();
const verified={open:dialog.open,title:text('#address-match-title'),matched:text('[data-address-matched-value]'),note:text('[data-address-note]'),enteredShown:visible('[data-address-entered]'),fix:visible('[data-address-action="fix"]'),confirm:visible('[data-address-action="confirm"]'),close:visible('[data-address-action="close"]')};
dialog.querySelector('[data-address-action="close"]').click();
document.getElementById('evidence').textContent=JSON.stringify({guessed,afterConfirm,afterFix,verified,closed:!dialog.open,errors});`);
    assert.deepEqual(result.guessed, {open: true, title: 'Address needs a look', entered: '12 Oak St Apt 4, Raliegh', matched: '12 Oak Street, Raleigh, NC 27601', note: "Google couldn't find this exactly, so this is its closest guess.", enteredShown: true, fix: true, confirm: true, close: false});
    assert.deepEqual(result.afterConfirm, {open: false, calls: [{verb: 'POST', url: '/api/v1/participants/7/address/confirm', target: '#participants-list', swap: 'innerHTML'}]});
    assert.deepEqual(result.afterFix, {open: false, edits: 1});
    assert.deepEqual(result.verified, {open: true, title: 'Address verified', matched: '1 Verified Way, Cary, NC 27511', note: 'Google matched this address exactly.', enteredShown: false, fix: false, confirm: false, close: true});
    assert.equal(result.closed, true);
});
