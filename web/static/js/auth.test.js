const assert = require('node:assert/strict');
const {test} = require('node:test');
const vm = require('node:vm');
const fs = require('node:fs');
const path = require('node:path');
const source = fs.readFileSync(path.join(__dirname, 'auth.js'), 'utf8');

async function run({signedIn = false, status = 200, pathname = '/sign-in', configFails = false, responses = [], refreshToken = 'fresh-token', search = '', clerkFails = false, scriptFails = false} = {}) {
    const state = {listeners: {}, message: {textContent: ''}, retry: {hidden: true, addEventListener: () => {}}, button: {hidden: true}, requests: []};
    const clerk = {
        session: signedIn ? {getToken: async () => { state.refreshes = (state.refreshes || 0) + 1; return refreshToken; }} : null,
        user: signedIn ? {id: 'user_test', primaryEmailAddress: {emailAddress: 'ar@example.com'}} : null,
        load: async () => { if (clerkFails) throw new Error('offline'); state.loaded = true; },
        mountSignIn: (_, options) => { state.options = options; },
        signOut: options => { state.signOut = options; },
    };
    state.button.addEventListener = (_, callback) => { state.click = callback; };
    await vm.runInNewContext(source, {
        window: state.window = {Clerk: clerk},
        URL, Headers, Event,
        location: {pathname, search, href: 'https://app.example'+pathname+search, origin: 'https://app.example', replace: value => { state.redirect = value; }},
        document: {
            getElementById: id => id === 'auth-retry' ? state.retry : id === 'auth-status' ? state.message : {},
            addEventListener: (name, listener) => { state.listeners[name] = listener; },
            body: {prepend: node => { state.recovery = node; }},
            createElement: () => ({dataset: {}, setAttribute: () => {}, appendChild: node => { state.recoveryLink = node; }}),
            head: {appendChild: script => { state.script = script; if (scriptFails) script.onerror(); else script.onload(); }},
            querySelectorAll: () => [state.button],
        },
        fetch: async (url, options) => {
            state.lastOptions = options;
            state.requests.push(url);
            if (url === '/auth/config') return {ok: !configFails, json: async () => ({publishableKey: 'pk_test_fixture', scriptURL: 'https://fixture.clerk.accounts.dev/clerk.js'})};
            const nextStatus = responses.shift() ?? status;
            return {ok: nextStatus === 200, status: nextStatus};
        },
    });
    return state;
}

test('sign-in allows Google account creation and returns new accounts to the protected entry point', async () => {
    const state = await run();
    assert.equal(state.options.withSignUp, true);
    assert.equal(state.options.transferable, true);
    assert.equal(state.options.routing, 'hash');
    assert.equal(state.options.signUpForceRedirectUrl, '/');
    assert.equal(state.script.dataset.clerkPublishableKey, 'pk_test_fixture');
});
test('only successful backend admission redirects a signed-in user to protected data', async () => {
    assert.equal((await run({signedIn: true})).redirect, '/');
    for (const status of [401, 403, 503]) {
        const state = await run({signedIn: true, status});
        assert.equal(state.redirect, undefined);
        assert.equal(state.options, undefined);
        assert.equal(state.retry.hidden, status === 403);
        assert.equal(state.button.hidden, false);
    }
});
test('protected pages load Clerk for refresh and expose sign-out', async () => {
    const state = await run({signedIn: true, pathname: '/m'});
    assert.equal(state.loaded, true);
    assert.equal(state.options, undefined);
    assert.deepEqual(state.requests, ['/auth/config']);
    state.click();
    assert.equal(state.signOut.redirectUrl, '/sign-in');
});
test('Clerk configuration failure explains configuration failure and offers retry', async () => {
    const state = await run({configFails: true});
    assert.equal(state.retry.hidden, false);
    assert.equal(state.message.textContent, 'Sign-in is temporarily unavailable. Try again.');
    assert.equal(state.loaded, undefined);
    assert.equal(state.options, undefined);
});


test('planner requests refresh and retry once only after a denied 401', async () => {
    const state = await run({signedIn: true, pathname: '/', responses: [401, 200]});
    const response = await state.window.authFetch('/api/v1/routes/edit/test', {method: 'POST', body: 'unchanged'});
    assert.equal(response.status, 200);
    assert.equal(state.refreshes, 1);
    assert.equal(state.lastOptions.body, 'unchanged');
    assert.equal(state.lastOptions.headers.get('Authorization'), 'Bearer fresh-token');
});
test('planner denial preserves current page and offers sign-in separately', async () => {
    const state = await run({signedIn: true, pathname: '/', responses: [401, 401]});
    await assert.rejects(state.window.authFetch('/api/v1/routes/edit/test'), /not saved/);
    assert.equal(state.refreshes, 1);
    assert.equal(state.redirect, undefined);
    assert.equal(state.recoveryLink.target, '_blank');
});
test('auth fetch never retries 403 or 503 and never sends credentials off-site', async () => {
    for (const status of [403, 503]) {
        const state = await run({signedIn: true, pathname: '/', status});
        if (status === 403) await assert.rejects(state.window.authFetch('/api/v1/routes/edit/test'));
        else assert.equal((await state.window.authFetch('/api/v1/routes/edit/test')).status, 503);
        assert.equal(state.refreshes, undefined);
        assert.equal(state.requests.length, 2);
        await assert.rejects(state.window.authFetch('https://attacker.example'), /this site/);
        assert.equal(state.requests.length, 2);
    }
});
test('mobile submission refreshes first and retains input when refresh fails', async () => {
    for (const refreshToken of ['fresh-token', null]) {
        const state = await run({signedIn: true, pathname: '/m/people', refreshToken});
        let prevented = false;
        let submitted = 0;
        const button = {};
        const form = {matches: () => true, requestSubmit: submitter => { assert.equal(submitter, button); submitted++; }};
        await state.listeners.submit({target: form, submitter: button, preventDefault: () => {prevented = true;}, stopImmediatePropagation: () => {}});
        assert.equal(prevented, true);
        assert.equal(state.refreshes, 1);
        assert.equal(submitted, refreshToken ? 1 : 0);
        assert.equal(state.redirect, undefined);
        if (!refreshToken) assert.match(state.recovery.textContent, /not been submitted/);
    }
});

test('mobile Enter submission preserves the absence of a submitter', async () => {
    const state = await run({signedIn: true, pathname: '/m/people'});
    let submitted = false;
    const form = {matches: () => true, requestSubmit: (...args) => { assert.equal(args.length, 0); submitted = true; }};
    await state.listeners.submit({target: form, submitter: null, preventDefault: () => {}, stopImmediatePropagation: () => {}});
    assert.equal(submitted, true);
});

test('denied accounts see their email and an explanation', async () => {
    for (const options of [{status: 403}, {status: 503, search: '?denied=1'}]) {
        const state = await run({signedIn: true, ...options});
        assert.equal(state.message.textContent, "This account isn't approved yet. Ask your organizer to add ar@example.com.");
        assert.equal(state.message.hidden, false);
        assert.equal(state.button.hidden, false);
    }
});
test('app configuration failure explains that changes may not save', async () => {
    const state = await run({pathname: '/m', configFails: true});
    assert.match(state.recovery.textContent, /Could not reach the sign-in service. Your changes may not save./);
});

test('denied query explains missing approval even before choosing a signed-in account', async () => {
 const state=await run({search:'?denied=1'});
 assert.equal(state.message.textContent, "This account isn't approved yet. Ask your organizer for access.");
});

test('Clerk script and load failures explain sign-in unavailability', async () => {
 for (const options of [{clerkFails:true},{scriptFails:true}]) {
  const signIn=await run(options);
  assert.equal(signIn.message.textContent,'Sign-in is temporarily unavailable. Try again.');
  const app=await run({...options,pathname:'/m'});
  assert.match(app.recovery.textContent,/Your changes may not save/);
 }
});

test('failed renewal tells a form to discard its pending confirmation', async()=>{
 const state=await run({signedIn:true,pathname:'/m',refreshToken:null});let failed;
 const form={matches:()=>true,dispatchEvent:event=>{failed=event.type;}};
 await state.listeners.submit({target:form,preventDefault(){},stopImmediatePropagation(){}});
 assert.equal(failed,'auth:submitFailed');
});
