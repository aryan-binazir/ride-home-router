'use strict';

const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const { execFileSync } = require('node:child_process');
const browser = process.env.BROWSER_TEST_BINARY;

for (const width of [390, 1440]) {
    test(`Route edits preserve ordering, ownership and editor interleavings at ${width}px`, { skip: !browser }, () => {
        const directory = fs.mkdtempSync(path.join(os.tmpdir(), 'rhr-edit-'));
        try {
            const file = path.join(directory, 'fixture.html');
            const assets = ['event-planner.js', 'editors.js'].map(name =>
                `<script>${fs.readFileSync(path.join(__dirname, name), 'utf8')}</script>`).join('');
            fs.writeFileSync(file, `<!doctype html><html><head><meta name="viewport" content="width=device-width,initial-scale=1"><style>${fs.readFileSync(path.join(__dirname, "../css/style.css"), "utf8")}</style></head><body>
<form id="event-form"><select name="activity_location_id"><option value="7" selected>School</option></select>
<input type="checkbox" class="participant-checkbox" value="1" checked><input type="checkbox" class="driver-checkbox" value="10" data-capacity="4" checked>
<input type="radio" name="mode" value="dropoff" checked><input id="route-time" name="route_time" value="15:30"><button id="calculate-btn" type="button">Calculate</button></form>
<div id="results-section"></div><dialog id="route-editor-dialog"><div id="route-editor"></div></dialog><pre id="evidence">pending</pre>
<script>
localStorage.clear();
const errors=[];addEventListener('error',e=>errors.push(e.message));addEventListener('unhandledrejection',e=>errors.push(String(e.reason)));
const requests=[], editors=[];
window.fetch=(url,options)=>new Promise(resolve=>requests.push({url,options,resolve}));
window.showConfirmDialog=async()=>true;
window.htmx={process(){},ajax(method,url){return new Promise(resolve=>{
 const target=document.getElementById('route-editor'), xhr={};
 document.dispatchEvent(new CustomEvent('htmx:beforeRequest',{detail:{target,xhr,requestConfig:{path:url}}}));
 editors.push({url,finish(html){const detail={target,xhr,shouldSwap:true};document.dispatchEvent(new CustomEvent('htmx:beforeSwap',{detail}));if(detail.shouldSwap){target.innerHTML=html;document.dispatchEvent(new CustomEvent('htmx:afterSwap',{detail}));}resolve();}});
});}};
</script>${assets}<script>
addEventListener('DOMContentLoaded',async()=>{
 const check=(ok,message)=>{if(!ok)throw Error(message)};
 const until=async predicate=>{for(let i=0;i<200;i++){if(predicate())return;await new Promise(r=>setTimeout(r,5));}throw Error('condition timeout')};
 const tick=()=>new Promise(r=>setTimeout(r,0));
 const card=(session,timings='stale',label='route')=>'<div id="route-'+session+'-0" class="route-card" data-route-index="0" data-itinerary="same" data-timings="'+timings+'" data-route-duration-secs="120" data-total-distance-meters="1000" data-detour-secs="20"><div class="route-timings">'+label+'</div><div class="stop-item" data-stop-cumulative-duration-secs="60"><span class="stop-details">Rider</span><span class="stop-eta"></span></div><div class="route-footer"></div><button data-session-action="preview">Preview</button><button data-session-action="edit" class="timings">Timings</button></div>';
 const routes=(session,label='route',timings='stale')=>'<div class="routes-container" data-session-id="'+session+'" data-out-of-balance="false" data-route-time="15:30" data-route-mode="dropoff"><div class="planner-plan-state-banner" hidden></div><button data-session-action="copy">Copy</button>'+card(session,timings,label)+'<div id="route-unused-'+session+'"></div><form hx-post="/api/v1/events"><input name="session_id" value="'+session+'"><input type="date" name="event_date" value="2026-09-01"><textarea name="notes"></textarea><button type="submit" data-session-action="save">Save</button></form></div>';
 const calculate=session=>{const xhr={},target=document.getElementById('results-section');document.body.dispatchEvent(new CustomEvent('htmx:beforeRequest',{detail:{xhr,elt:document.getElementById('calculate-btn')}}));target.innerHTML=routes(session);document.body.dispatchEvent(new CustomEvent('htmx:afterSwap',{detail:{xhr,target}}));target.dispatchEvent(new CustomEvent('htmx:afterSettle',{bubbles:true}));};
 const respond=(index,html,ok=true)=>requests[index].resolve({ok,headers:{get:()=>null},text:async()=>html});
 const save=()=>document.querySelector('#results-section form');
 const copy=()=>document.querySelector('[data-session-action="copy"]');
 const editorForm='<form onsubmit="submitRouteEditor(event)"><h2>Move rider</h2><input name="session_id" value="a"><input name="action" value="move"><input name="participant_id" value="1"><input name="from_route_index" value="0"><input name="destination" value="1"><button type="submit">Move</button></form>';
 const saved=[];document.addEventListener('submit',e=>{if(e.target.getAttribute('hx-post')==='/api/v1/events'){saved.push(new FormData(e.target));e.preventDefault();}});
 try {
 await tick();calculate('a');
 save().elements.event_date.value='2026-10-04';save().elements.event_date.dispatchEvent(new Event('input',{bubbles:true}));save().elements.notes.value='Bring snacks';
 await moveParticipant(1,0,1);const reset=resetRoutes();await until(()=>requests.length===1);await moveParticipant(2,1,0);save().dispatchEvent(new Event('submit',{bubbles:true,cancelable:true}));
 check(copy().disabled&&document.querySelector('[data-session-action="preview"]').disabled,'handoffs lock');check(await copyAllRoutes()===false,'pending copy rejected');check(previewRoute(document.querySelector('[data-session-action="preview"]'))===false,'pending preview rejected');
 respond(0,routes('a'));await until(()=>requests.length===2);check(requests[1].url.includes('/reset?'),'reset follows move');respond(1,routes('a'));await reset;await until(()=>requests.length===3);check(saved.length===0,'Save waits for last move');respond(2,routes('a'));await until(()=>saved.length===1);
 check(saved[0].get('event_date')==='2026-10-04'&&saved[0].get('notes')==='Bring snacks','Save retains fields');check(!copy().disabled,'handoffs unlock');
 check(JSON.parse(requests[0].options.body).participant_id===1&&JSON.parse(requests[2].options.body).participant_id===2,'move order');check(requests.every(r=>r.options.headers['X-Route-Fragment']==='true'&&r.options.headers['X-Route-Balance']==='false'),'edit headers');

 const rejected=addUnusedDriver(99);await until(()=>requests.length===4);respond(3,'Cannot add driver',false);check(await rejected===false,'manual rejection');await tick();check(!copy().disabled,'failure unlocks');
 const recovery=resetRoutes();await until(()=>requests.length===5);respond(4,routes('a'));check(await recovery===true,'manual recovery');await tick();

 await moveParticipant(3,0,1);const opening=openRouteEditor({dataset:{editorUrl:'/api/v1/routes/editor?session_id=a'}});await until(()=>requests.length===6);check(editors.length===0,'editor waits for moves');respond(5,routes('a'));await until(()=>editors.length===1);editors[0].finish(editorForm);await opening;check(document.getElementById('route-editor-dialog').open,'editor opens');const box=document.getElementById('route-editor-dialog').getBoundingClientRect();check(box.left>=0&&box.right<=innerWidth&&box.top>=0&&box.bottom<=innerHeight,'styled editor stays within viewport');
 const form=document.querySelector('#route-editor form');const denied=submitRouteEditor({preventDefault(){},target:form});await until(()=>requests.length===7);await tick();check(document.getElementById('route-editor-dialog').open&&form.querySelector('button').disabled,'editor waits for submit response');respond(6,'Cannot move rider',false);await denied;check(document.getElementById('route-editor-dialog').open&&!form.querySelector('button').disabled,'failed submit keeps editor open');
 const submitting=submitRouteEditor({preventDefault(){},target:form});const reopening=openRouteEditor({dataset:{editorUrl:'/api/v1/routes/editor?session_id=a'}});await until(()=>requests.length===8);await tick();check(document.getElementById('route-editor-dialog').open,'successful submit still waits');respond(7,routes('a'));await submitting;await until(()=>editors.length===2);editors[1].finish(editorForm);await reopening;check(!document.getElementById('route-editor-dialog').open&&document.getElementById('route-editor').children.length===0,'submit closing cancels its concurrent reopen');
 const staleOpen=openRouteEditor({dataset:{editorUrl:'/api/v1/routes/editor?session_id=a'}});await until(()=>editors.length===3);closeRouteEditor();editors[2].finish(editorForm);await staleOpen;check(!document.getElementById('route-editor-dialog').open,'closed editor response rejected');
 const oldOpen=openRouteEditor({dataset:{editorUrl:'/api/v1/routes/editor?session_id=a'}});await until(()=>editors.length===4);calculate('b');editors[3].finish(editorForm);await oldOpen;check(!document.getElementById('route-editor-dialog').open,'old session editor rejected');

 calculate('a');const old=addUnusedDriver(20);await until(()=>requests.length===9);calculate('b');respond(8,routes('a','old result'));await old;check(document.querySelector('.routes-container').dataset.sessionId==='b','old edit result rejected');
 await moveParticipant(4,0,1);document.getElementById('route-time').value='16:00';document.getElementById('route-time').dispatchEvent(new Event('change',{bubbles:true}));check(!document.querySelector('[data-session-action="preview"]').disabled,'queued edit cancellation unlocks preview immediately');await new Promise(r=>setTimeout(r,550));check(requests.length===9,'queued move canceled by Plan change');
 calculate('b');const active=addUnusedDriver(21);await until(()=>requests.length===10);const canceled=resetRoutes();await tick();document.getElementById('route-time').value='17:00';document.getElementById('route-time').dispatchEvent(new Event('change',{bubbles:true}));check(await canceled===false,'queued manual caller resolves');respond(9,routes('b','same session result'));await active;await tick();check(document.querySelector('.route-timings').textContent==='same session result','same installed session response retained');check(save().querySelector('button').disabled,'Plan remains stale');

 calculate('b');const timing=showRouteTimings(document.querySelector('.timings'));await until(()=>requests.length===11);check(requests[10].url==='/api/v1/routes/session/timings'&&JSON.parse(requests[10].options.body).route_index===0,'timing dispatch');respond(10,routes('b','measured', 'measured'));await timing;await tick();
 const full=addUnusedDriver(22);await until(()=>requests.length===12);respond(11,routes('b','stale'));await full;await tick();check(document.querySelector('.route-card').dataset.timings==='measured'&&document.querySelector('.route-timings').textContent==='measured','full render retains measured timings');
 const untouched=save();const patch=addUnusedDriver(23);await until(()=>requests.length===13);respond(12,'<div data-route-patch="b" data-route-count="1">'+card('b','measured','patch')+'</div>');await patch;await tick();check(save()===untouched&&document.querySelector('.route-timings').textContent==='patch','fragment retains form');
 document.getElementById('evidence').textContent=JSON.stringify({width:innerWidth,requests:requests.length,saves:saved.length,errors});
 }catch(e){document.getElementById('evidence').textContent=JSON.stringify({error:String(e),requests:requests.map(r=>r.url),errors});}
});</script></body></html>`);
            const output = execFileSync(process.execPath, [path.join(__dirname, 'frontend-browser.test.js'), '--browser-cdp', browser, file, String(width), '1000', '#evidence'], { encoding: 'utf8', timeout: 60000 });
            const result = JSON.parse(output);
            assert.equal(result.error, undefined, output);
            assert.deepEqual(result, { width, requests: 13, saves: 1, errors: [] });
        } finally {
            fs.rmSync(directory, { recursive: true, force: true });
        }
    });
}

for (const width of [390, 1440]) {
    test(`Save interception precedes real HTMX dispatch at ${width}px`, { skip: !browser }, () => {
        const directory = fs.mkdtempSync(path.join(os.tmpdir(), 'rhr-save-'));
        try {
            const file = path.join(directory, 'fixture.html');
            const assets = ['htmx.min.js', 'event-planner.js'].map(name =>
                `<script>${fs.readFileSync(path.join(__dirname, name), 'utf8')}</script>`).join('');
            fs.writeFileSync(file, `<!doctype html><html><head><meta name="viewport" content="width=device-width,initial-scale=1"><style>${fs.readFileSync(path.join(__dirname, "../css/style.css"), "utf8")}</style></head><body>
<form id="event-form"><select name="activity_location_id"><option value="7">School</option></select>
<input type="checkbox" class="participant-checkbox" value="1" checked><input type="checkbox" class="driver-checkbox" value="10" data-capacity="4" checked>
<input type="radio" name="mode" value="dropoff" checked><input id="route-time" name="route_time" value="15:30"><button id="calculate-btn" type="button">Calculate</button></form>
<div id="results-section"></div><pre id="evidence">pending</pre>
<script>
localStorage.clear();
const errors=[];addEventListener('error',e=>errors.push(e.message));addEventListener('unhandledrejection',e=>errors.push(String(e.reason)));
const edits=[], saves=[];
window.fetch=(url,options)=>new Promise(resolve=>edits.push({url,options,resolve}));
const realOpen=XMLHttpRequest.prototype.open;
XMLHttpRequest.prototype.open=function(method,url,...args){this.routeTestUrl=url;return realOpen.call(this,method,url,...args)};
XMLHttpRequest.prototype.send=function(body){saves.push({url:this.routeTestUrl,body:String(body)})};
</script>${assets}<script>
addEventListener('DOMContentLoaded',async()=>{
 const until=async predicate=>{for(let i=0;i<200;i++){if(predicate())return;await new Promise(r=>setTimeout(r,5));}throw Error('condition timeout')};
 const check=(ok,message)=>{if(!ok)throw Error(message)};
 const html='<div class="routes-container" data-session-id="a" data-out-of-balance="false"><form hx-post="/api/v1/events"><input name="session_id" value="a"><input type="date" name="event_date" value="2026-09-01"><textarea name="notes"></textarea><button type="submit" data-session-action="save">Save</button></form></div>';
 try {
 await new Promise(r=>setTimeout(r,0));
 const target=document.getElementById('results-section'),xhr={};
 document.body.dispatchEvent(new CustomEvent('htmx:beforeRequest',{detail:{xhr,elt:document.getElementById('calculate-btn')}}));
 target.innerHTML=html;htmx.process(target);
 document.body.dispatchEvent(new CustomEvent('htmx:afterSwap',{detail:{xhr,target}}));target.dispatchEvent(new CustomEvent('htmx:afterSettle',{bubbles:true}));
 const original=target.querySelector('form');original.elements.event_date.value='2026-10-04';original.elements.event_date.dispatchEvent(new Event('input',{bubbles:true}));original.elements.notes.value='Bring snacks';
 await moveParticipant(1,0,1);original.dispatchEvent(new Event('submit',{bubbles:true,cancelable:true}));
 check(edits.length===1&&saves.length===0,'HTMX Save must wait for edit');
 edits[0].resolve({ok:true,text:async()=>html});
 await until(()=>saves.length===1);
 check(target.querySelector('form')!==original,'Save uses replacement form');
 const payload=new URLSearchParams(saves[0].body);
 check(saves[0].url==='/api/v1/events'&&payload.get('session_id')==='a'&&payload.get('event_date')==='2026-10-04'&&payload.get('notes')==='Bring snacks','HTMX Save payload');
 document.getElementById('evidence').textContent=JSON.stringify({width:innerWidth,edits:edits.length,saves:saves.length,errors});
 }catch(e){document.getElementById('evidence').textContent=JSON.stringify({error:String(e),edits:edits.length,saves,errors});}
});</script></body></html>`);
            const output = execFileSync(process.execPath, [path.join(__dirname, 'frontend-browser.test.js'), '--browser-cdp', browser, file, String(width), '1000', '#evidence'], { encoding: 'utf8', timeout: 60000 });
            const result = JSON.parse(output);
            assert.equal(result.error, undefined, output);
            assert.deepEqual(result, { width, edits: 1, saves: 1, errors: [] });
        } finally {
            fs.rmSync(directory, { recursive: true, force: true });
        }
    });
}
