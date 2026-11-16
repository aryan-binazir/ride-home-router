'use strict';
const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const {execFileSync} = require('node:child_process');
const {pathToFileURL} = require('node:url');

const browser = process.env.BROWSER_TEST_BINARY;
const draftKey = 'ride-home-router:event-planner-draft:v1';

function run(width, scenario, {paged = true, draft = null, source = null, location = false, session = null} = {}) {
    const directory = fs.mkdtempSync(path.join(os.tmpdir(), 'rhr-plan-selection-'));
    try {
        const file = path.join(directory, 'plan.html');
        const setup = `
const errors=[];addEventListener('error',e=>errors.push(e.message));
addEventListener('unhandledrejection',e=>errors.push(String(e.reason)));
localStorage.clear();
${draft ? `localStorage.setItem(${JSON.stringify(draftKey)},${JSON.stringify(JSON.stringify(draft))});` : ''}
${session ? `localStorage.setItem('ride-home-router:active-session:v2',${JSON.stringify(JSON.stringify(session))});` : ''}
const requests=[];
window.authFetch=(url,options)=>new Promise((resolve,reject)=>requests.push({url,options,values:new URLSearchParams(options.body),resolve,reject}));
const vehicle=(id,value='',capacity=4)=>'<span class="van-assignment-inline" id="van-control-'+id+'"><select hidden class="van-assignment-select" id="van-assignment-'+id+'" data-driver-id="'+id+'" name="org_vehicle_'+id+'"><option selected value="'+value+'" data-capacity="'+capacity+'">Vehicle</option></select><span class="van-assignment-current">Vehicle</span></span>';
const picker=(kind,ids,selected=[],vans={})=>{
 const driver=kind==='drivers',singular=driver?'driver':'participant';
 const row=id=>'<label class="select-row" data-search="person '+id+'" data-labels="'+(id==='1'?'7':'8')+'"><input class="'+singular+'-checkbox" type="checkbox" name="'+singular+'_ids" value="'+id+'" data-capacity="4" '+(selected.includes(id)?'checked':'')+'><span>Person '+id+'</span>'+(driver?vehicle(id,vans[id]||'',vans[id]?12:4):'')+'</label>';
 const hidden=selected.filter(id=>!ids.includes(id)).map(id=>'<input hidden checked type="checkbox" class="'+singular+'-checkbox" name="'+singular+'_ids" value="'+id+'" data-capacity="4">'+(driver?'<span hidden>'+vehicle(id,vans[id]||'',vans[id]?12:4)+'</span>':'')).join('');
 return '<div id="'+kind+'-picker"><div id="'+kind+'-selection" ${paged ? 'data-page-source="\'+kind+\'"' : ''}>'+ids.map(row).join('')+'</div>'+hidden+'<button type="button" onclick="requestPlannerPicker(&quot;'+kind+'&quot;,50)">Next</button><button type="button" onclick="requestPlannerPicker(&quot;'+kind+'&quot;,0)">Previous</button></div>';
};
const respond=(request,html)=>request.resolve({ok:true,headers:new Headers(),text:async()=>html});
const tick=()=>new Promise(resolve=>setTimeout(resolve,0));
const wait=ms=>new Promise(resolve=>setTimeout(resolve,ms));
const selected=kind=>[...document.querySelectorAll('.'+(kind==='drivers'?'driver':'participant')+'-checkbox:checked')].map(input=>input.value).sort();
const saved=()=>JSON.parse(localStorage.getItem(${JSON.stringify(draftKey)}));
const stats=()=>({participants:document.getElementById('participants-selected-count').textContent,drivers:document.getElementById('drivers-selected-count').textContent,seats:document.getElementById('drivers-selected-seats').textContent,summary:document.getElementById('selection-stats').textContent});
const page=kind=>document.querySelector('#'+kind+'-picker button').click();
const choose=(kind,id)=>document.querySelector('.'+(kind==='drivers'?'driver':'participant')+'-checkbox[value="'+id+'"]').click();
const editVehicle=(id,value,capacity)=>htmx.swap(document.getElementById('van-control-'+id),vehicle(id,value,capacity),{swapStyle:'outerHTML'});
const search=kind=>{const input=document.getElementById(kind+'-search');input.value='Person';input.dispatchEvent(new Event('input',{bubbles:true}));};
`;
        const controls = ['participants', 'drivers'].map(kind => `<input id="${kind}-search" data-filter-role="search" data-list-id="${kind}-selection" oninput="filterSelectList(this,'${kind}-selection')"><button type="button" id="${kind}-all" onclick="selectAll${kind === 'drivers' ? 'Drivers' : 'Participants'}()">Select all matching</button><button type="button" class="label-filter-chip" data-list-id="${kind}-selection" data-label-id="7" onclick="toggleLabelFilter(this)">Label 7</button><button type="button" id="${kind}-filters" onclick="clearPlannerFilters('${kind}-selection')">Clear filters</button><div id="${kind}-mount"></div>`).join('');
        fs.writeFileSync(file, `<!doctype html><html class="planner-restoring"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><style>${fs.readFileSync(path.join(__dirname, "../css/style.css"), "utf8")}</style></head><body>
<script>${setup}</script><form id="event-form"><div id="event-activity-location-select"><select name="activity_location_id" ${location ? 'data-on-demand="true"' : ''}><option value="">Choose</option><option value="1">School</option></select></div><input name="route_time" value="18:30"><input type="radio" name="mode" value="dropoff" checked><input type="radio" name="mode" value="pickup">${controls}<button type="button" onclick="clearSelections()" id="clear">Clear all</button><button type="button" id="calculate-btn">Calculate</button></form><span id="participants-selected-count">0</span><span id="drivers-selected-count">0</span><span id="drivers-selected-seats">0</span><span id="selection-stats"></span><strong data-picker-summary="seats">0</strong><div id="results-section"></div><template id="results-empty-state-template">Empty</template><dialog id="route-editor-dialog"><div id="route-editor"></div></dialog><pre id="evidence">pending</pre>
<script>for(const kind of ['participants','drivers'])document.getElementById(kind+'-mount').innerHTML=picker(kind,['1','2']);</script>
${['htmx.min.js', 'editors.js', 'event-planner.js'].map(name => `<script>${name === 'event-planner.js' && source ? source : fs.readFileSync(path.join(__dirname, name), 'utf8')}</script>`).join('')}
<script>addEventListener('load',async()=>{try{${scenario}\ndocument.getElementById('evidence').textContent=JSON.stringify({...out,errors,width:innerWidth});}catch(error){document.getElementById('evidence').textContent=JSON.stringify({error:String(error),stack:error.stack});}});</script></body></html>`);
        const wrapper = path.join(directory, 'fixture.html');
        fs.writeFileSync(wrapper, `<!doctype html><html><body><iframe src="plan.html" style="width:${width}px;height:900px;border:0" onload="const frame=this;const collect=()=>{const text=frame.contentDocument.getElementById('evidence').textContent;if(text==='pending')setTimeout(collect,20);else document.getElementById('evidence').textContent=text;};collect();"></iframe><pre id="evidence">pending</pre></body></html>`);
        const output = execFileSync(browser, ['--headless', '--no-sandbox', '--disable-gpu', '--no-first-run', '--allow-file-access-from-files', `--user-data-dir=${directory}/profile`, `--window-size=${Math.max(width, 500)},950`, '--dump-dom', '--virtual-time-budget=3000', pathToFileURL(wrapper).href], {encoding: 'utf8', timeout: 60000, stdio: ['ignore', 'pipe', 'pipe'], maxBuffer: 3 * 1024 * 1024});
        const result = JSON.parse(output.match(/<pre id="evidence">(.*?)<\/pre>/s)?.[1] || 'null');
        assert.ok(result, 'browser completed the scenario');
        assert.equal(result.error, undefined, result.stack);
        assert.deepEqual(result.errors, []);
        assert.equal(result.width, width);
        return result;
    } finally {
        fs.rmSync(directory, {recursive: true, force: true});
    }
}

const currentSource = fs.readFileSync(path.join(__dirname, 'event-planner.js'), 'utf8');
const serialSource = JSON.parse(fs.readFileSync(path.join(__dirname, 'testdata/planner-restore-serial.json'), 'utf8'))
    .reduce((source, section) => {
        const marker = section.start.replace('        function restoreRouteSession(', '        async function restoreRouteSession(');
        const start = source.indexOf(marker);
        const end = source.indexOf(section.end, start);
        assert.ok(start >= 0 && end > start, 'serial restore oracle markers still match');
        return source.slice(0, start) + section.source + source.slice(end);
    }, currentSource);

const restoreDraft = {activityLocationId: '1', participantIds: ['1'], driverIds: ['1'], mode: 'pickup', routeTime: '19:15', vanAssignments: {'1': '9'}, labelFilters: {participants: ['7'], drivers: []}};
const restoreFingerprint = JSON.stringify({...restoreDraft, vanAssignments: [['1', '9']], labelFilters: undefined});

function restoreScenario({failure = '', order = false, cancel = '', malformed = false, status = 200} = {}) {
    return `
const processed=[];const process=htmx.process;htmx.process=element=>{processed.push(element.id);process(element);};
const starts=requests.map(r=>r.url);
const until=async predicate=>{for(let n=0;n<300;n++){if(predicate())return;await wait(10);}throw Error('restore did not settle');};
const snapshot=()=>({participants:selected('participants'),drivers:selected('drivers'),van:document.getElementById('van-assignment-1')?.value,stats:stats(),location:document.querySelector('select[name="activity_location_id"]').value,mode:document.querySelector('input[name="mode"]:checked').value,time:document.querySelector('input[name="route_time"]').value,inert:document.getElementById('event-form').inert,disabled:document.getElementById('calculate-btn').disabled,draft:saved(),active:localStorage.getItem('ride-home-router:active-session:v2'),toasts:[...document.querySelectorAll('.toast-message')].map(e=>e.textContent),html:document.getElementById('results-section').innerHTML});
const initial=snapshot();let earlyVisible=false;
const observed=new Set();let cancelled=false;
const poll=setInterval(()=>{
 for(const request of requests){
  if(observed.has(request))continue;observed.add(request);
  const kind=request.url.includes('routes/session')?'session':request.url.split('/').at(-1);
  const delays=${JSON.stringify(order ? {'location-editor':90,participants:60,drivers:30,session:0,'vehicle-assignments':20} : {})};
  setTimeout(async()=>{
   if(kind==='session'&&${JSON.stringify(cancel)}&&!cancelled){
    await until(()=>!document.getElementById('event-form').inert);
    cancelled=true;
    if(${JSON.stringify(cancel)}==='clear')clearSelections();
    else if(${JSON.stringify(cancel)}==='input'){const time=document.querySelector('input[name="route_time"]');time.value='20:30';time.dispatchEvent(new Event('change',{bubbles:true}));}
    else document.body.dispatchEvent(new CustomEvent('htmx:beforeRequest',{bubbles:true,detail:{elt:document.getElementById('calculate-btn'),xhr:{}}}));
    if(request.options.signal.aborted){request.reject(new DOMException('Aborted','AbortError'));return;}
   }
   if(kind===${JSON.stringify(failure)}&&${JSON.stringify(!malformed && status === 200)}){request.reject(Error('offline'));return;}
   let html=kind==='location-editor'?'<div id="event-activity-location-select"><select name="activity_location_id" data-on-demand="true"><option value="">Choose</option><option value="1">School</option></select></div>'
    :kind==='participants'?picker('participants',['1','2'],['1'])
    :kind==='drivers'?picker('drivers',['2'],['1'],{'1':'9'})
    :kind==='vehicle-assignments'?'<div>'+vehicle('1','9',12)+'</div>'
    :'<div class="routes-container" data-session-id="s1"><section class="route-card">Restored routes</section><button data-session-action="save">Save</button></div>';
   if(kind===${JSON.stringify(failure)}&&${JSON.stringify(malformed)})html='<div>wrong fragment</div>';
   if(kind===${JSON.stringify(failure)}&&${status}!==200)request.resolve({ok:false,status:${status},headers:new Headers(),text:async()=>'Restore rejected'});
   else respond(request,html);
  },delays[kind]||0);
 }
 if(document.documentElement.classList.contains('planner-restoring')&&document.querySelector('.route-card'))earlyVisible=true;
},1);
await until(()=>!document.documentElement.classList.contains('planner-restoring'));await wait(150);clearInterval(poll);
const out={initial,final:snapshot(),processed,earlyVisible,starts,aborted:requests.filter(r=>r.options.signal?.aborted).map(r=>r.url.split('?')[0])};`;
}

for (const width of [390, 1280]) {
    for (const options of [
        {}, {order:true}, {mismatch:true}, {paged:false},
        ...['location-editor','participants','drivers','vehicle-assignments','session'].map(failure=>({failure,order:true,paged:failure!=='vehicle-assignments'})),
        ...['location-editor','participants','drivers'].map(failure=>({failure,malformed:true,order:true})),
        ...['location-editor','participants','drivers','vehicle-assignments','session'].map(failure=>({failure,status:500,order:true,paged:failure!=='vehicle-assignments'})),
        ...[204,404,410].map(status=>({failure:'session',status,order:true})),
        ...['calculate','clear','input'].map(cancel=>({cancel})),
    ]) {
        test(`draft/session restore matches serial oracle ${JSON.stringify(options)} at ${width}px`, {skip: !browser}, () => {
            const config = {draft: restoreDraft, location: true, paged: options.paged !== false,
                session: {id:'s1',fingerprint:options.mismatch?'different inputs':restoreFingerprint}};
            const scenario = restoreScenario(options);
            const old = run(width, scenario, {...config, source:serialSource});
            const next = run(width, scenario, config);
            assert.deepEqual(next.initial, old.initial);
            assert.deepEqual(next.final, old.final);
            assert.deepEqual(next.processed, old.processed);
            assert.equal(next.earlyVisible, false);
            if (options.cancel) assert.ok(next.aborted.includes('/api/v1/routes/session'));
            if (options.paged !== false) assert.deepEqual(next.starts.map(url=>url.split('?')[0]), ['/api/v1/planner/location-editor','/api/v1/planner/participants','/api/v1/planner/drivers','/api/v1/routes/session']);
        });
    }
}

for (const width of [390, 1280]) {
    test(`pending picker preserves selection and vehicle edits at ${width}px`, {skip: !browser}, () => {
        const result = run(width, `
choose('drivers','1');editVehicle('1','9',12);await tick();
choose('participants','1');
page('drivers');const driverRequest=requests.at(-1);
page('participants');const participantRequest=requests.at(-1);
choose('drivers','1');choose('drivers','2');editVehicle('2','10',8);await tick();
choose('participants','1');choose('participants','2');
respond(driverRequest,picker('drivers',['3'],['1'],{'1':'9'}));
respond(participantRequest,picker('participants',['3'],['1']));await tick();
const out={drivers:selected('drivers'),participants:selected('participants'),van:document.getElementById('van-assignment-2').value,stats:stats(),draft:saved(),hidden:document.querySelector('.driver-checkbox[value="2"]').hidden,sent:driverRequest.values.getAll('driver_ids'),sentVan:driverRequest.values.get('org_vehicle_1')};`);
        assert.deepEqual(result.drivers, ['2']);
        assert.deepEqual(result.participants, ['2']);
        assert.equal(result.van, '10');
        assert.equal(result.hidden, true);
        assert.deepEqual(result.sent, ['1']);
        assert.equal(result.sentVan, '9');
        assert.deepEqual(result.stats, {participants: '1', drivers: '1', seats: '8', summary: '1 participant • 1 driver • 8 seats'});
        assert.deepEqual(result.draft.driverIds, ['2']);
        assert.deepEqual(result.draft.participantIds, ['2']);
        assert.deepEqual(result.draft.vanAssignments, {'2': '10'});
    });

    test(`pending picker preserves a retained driver's new van at ${width}px`, {skip: !browser}, () => {
        const result = run(width, `
choose('drivers','1');editVehicle('1','9',12);await tick();
page('drivers');const pending=requests.at(-1);
editVehicle('1','10',8);await tick();
respond(pending,picker('drivers',['1','3'],['1'],{'1':'9'}));await tick();
const out={selected:selected('drivers'),van:document.getElementById('van-assignment-1').value,stats:stats(),draft:saved(),summary:document.querySelector('[data-picker-summary="seats"]').textContent};`);
        assert.deepEqual(result.selected, ['1']);
        assert.equal(result.van, '10');
        assert.equal(result.stats.seats, '8');
        assert.equal(result.summary, '8');
        assert.deepEqual(result.draft.vanAssignments, {'1': '10'});
    });

    for (const olderFirst of [true, false]) {
        test(`only winning picker response updates the Plan, older ${olderFirst ? 'first' : 'last'}, at ${width}px`, {skip: !browser}, () => {
            const result = run(width, `
choose('drivers','1');page('drivers');const old=requests.at(-1);
page('drivers');const winner=requests.at(-1);
const before=saved();
${olderFirst ? "respond(old,picker('drivers',['2'],['1']));await tick();" : "respond(winner,picker('drivers',['3'],['1']));await tick();"}
const interim=[...document.querySelectorAll('#drivers-selection input')].map(input=>input.value);
${olderFirst ? "respond(winner,picker('drivers',['3'],['1']));" : "respond(old,picker('drivers',['2'],['1']));"}await tick();
const out={aborted:old.options.signal.aborted,interim,rows:[...document.querySelectorAll('#drivers-selection input')].map(input=>input.value),selected:selected('drivers'),stats:stats(),draft:saved(),before};`);
            assert.equal(result.aborted, true);
            assert.deepEqual(result.interim, olderFirst ? ['1', '2'] : ['3']);
            assert.deepEqual(result.rows, ['3']);
            assert.deepEqual(result.selected, ['1']);
            assert.equal(result.stats.seats, '4');
            assert.deepEqual(result.draft, result.before);
        });
    }

    test(`Clear All cancels debounce and ignores pending picker responses at ${width}px`, {skip: !browser}, () => {
        const result = run(width, `
choose('drivers','1');choose('participants','1');editVehicle('1','9',12);await tick();
page('drivers');const pending=requests.at(-1);search('drivers');search('participants');
document.getElementById('clear').click();const afterClear=requests.length;const immediate=saved();
respond(pending,picker('drivers',['2'],['1'],{'1':'9'}));await tick();
for(const request of requests.slice(afterClear-2))respond(request,picker(request.url.endsWith('drivers')?'drivers':'participants',['1','2']));
await wait(400);const out={aborted:pending.options.signal.aborted,afterClear,finalRequests:requests.length,immediate,drivers:selected('drivers'),participants:selected('participants'),stats:stats(),draft:saved(),search:[...document.querySelectorAll('[data-filter-role="search"]')].map(input=>input.value)};`);
        assert.equal(result.aborted, true);
        assert.equal(result.finalRequests, result.afterClear, 'pending search never starts after Clear All');
        assert.equal(result.immediate, null);
        assert.deepEqual(result.drivers, []);
        assert.deepEqual(result.participants, []);
        assert.deepEqual(result.search, ['', '']);
        assert.equal(result.stats.seats, '0');
        assert.deepEqual(result.draft.driverIds, []);
        assert.deepEqual(result.draft.participantIds, []);
        assert.deepEqual(result.draft.vanAssignments, {});
    });
    test(`serial paging and Select All retain off-page controls at ${width}px`, {skip: !browser}, () => {
        const result = run(width, `
choose('drivers','1');page('drivers');respond(requests.at(-1),picker('drivers',['3'],['1']));await tick();
choose('drivers','3');document.querySelector('#drivers-picker button:last-child').click();
const back=requests.at(-1);respond(back,picker('drivers',['1','2'],['1','3']));await tick();
const serial=selected('drivers');document.getElementById('drivers-all').click();const all=requests.at(-1);
respond(all,picker('drivers',['1','2'],['1','2','3']));await tick();
const out={serial,selected:selected('drivers'),select:all.values.get('select'),sent:back.values.getAll('driver_ids').sort(),stats:stats(),draft:saved()};`);
        assert.deepEqual(result.serial, ['1', '3']);
        assert.deepEqual(result.sent, ['1', '3']);
        assert.equal(result.select, 'all');
        assert.deepEqual(result.selected, ['1', '2', '3']);
        assert.equal(result.stats.seats, '12');
        assert.deepEqual(result.draft.driverIds.sort(), ['1', '2', '3']);
    });

    for (const paged of [false, true]) {
        test(`${paged ? 'on-demand' : 'local'} draft restores vans at ${width}px`, {skip: !browser}, () => {
            const result = run(width, `
const during=document.getElementById('event-form').inert;
${paged ? "respond(requests[0],picker('participants',['1','2'],['1']));await tick();respond(requests[1],picker('drivers',['2'],['1'],{'1':'9'}));" : "respond(requests[0],'<div>'+vehicle('1','9',12)+'</div>');"}
await tick();
const restored={drivers:selected('drivers'),participants:selected('participants'),seats:stats().seats,van:document.getElementById('van-assignment-1').value,enabled:!document.getElementById('calculate-btn').disabled,inert:document.getElementById('event-form').inert};
choose('participants','2');const out={during,restored,requests:requests.map(request=>({url:request.url,van:request.values.get('org_vehicle_1')})),draft:saved()};`, {paged, draft: {activityLocationId: '1', participantIds: ['1'], driverIds: ['1'], mode: 'pickup', routeTime: '19:15', vanAssignments: {'1': '9'}, labelFilters: {participants: ['7'], drivers: []}}});
            assert.equal(result.during, true);
            assert.deepEqual(result.restored, {drivers: ['1'], participants: ['1'], seats: '12', van: '9', enabled: true, inert: false});
            assert.deepEqual(result.requests.map(request => request.url), paged ? ['/api/v1/planner/participants', '/api/v1/planner/drivers'] : ['/api/v1/planner/vehicle-assignments']);
            assert.equal(result.requests.at(-1).van, '9');
            assert.equal(result.draft.mode, 'pickup');
            assert.equal(result.draft.routeTime, '19:15');
            assert.deepEqual(result.draft.vanAssignments, {'1': '9'});
            assert.deepEqual(result.draft.labelFilters, {participants: ['7'], drivers: []});
        });
    }

    test(`local search and labels select only matching rows at ${width}px`, {skip: !browser}, () => {
        const result = run(width, `
const input=document.getElementById('participants-search');input.value='person 2';input.dispatchEvent(new Event('input',{bubbles:true}));
document.getElementById('participants-all').click();const searched=selected('participants');
document.getElementById('participants-filters').click();document.querySelector('.label-filter-chip[data-list-id="participants-selection"]').click();document.getElementById('participants-all').click();
const out={searched,selected:selected('participants'),hidden:document.querySelector('.participant-checkbox[value="2"]').closest('.select-row').classList.contains('hidden'),requests:requests.length,draft:saved()};`, {paged: false});
        assert.deepEqual(result.searched, ['2']);
        assert.deepEqual(result.selected, ['1', '2']);
        assert.equal(result.hidden, true);
        assert.equal(result.requests, 0);
        assert.deepEqual(result.draft.labelFilters.participants, ['7']);
    });

}
