const assert = require('node:assert/strict');
const {test} = require('node:test');
const vm = require('node:vm');
const fs = require('node:fs');
const path = require('node:path');
const source = fs.readFileSync(path.join(__dirname, 'auth.js'), 'utf8');

async function run({signedIn = false, status = 200, pathname = '/sign-in', configFails = false, responses = [], refreshToken = 'fresh-token'} = {}) {
    const state = {listeners: {}, message: {textContent: ''}, retry: {hidden: true, addEventListener: () => {}}, button: {hidden: true}, requests: []};
    const clerk = {
        session: signedIn ? {getToken: async () => { state.refreshes = (state.refreshes || 0) + 1; return refreshToken; }} : null,
        user: signedIn ? {id: 'user_test'} : null,
        load: async () => { state.loaded = true; },
        mountSignIn: (_, options) => { state.options = options; },
        signOut: options => { state.signOut = options; },
    };
    state.button.addEventListener = (_, callback) => { state.click = callback; };
    await vm.runInNewContext(source, {
        window: state.window = {Clerk: clerk},
        URL, Headers,
        location: {pathname, href: 'https://app.example'+pathname, origin: 'https://app.example', replace: value => { state.redirect = value; }},
        document: {
            getElementById: id => id === 'auth-retry' ? state.retry : {},
            addEventListener: (name, listener) => { state.listeners[name] = listener; },
            body: {prepend: node => { state.recovery = node; }},
            createElement: () => ({dataset: {}, setAttribute: () => {}, appendChild: node => { state.recoveryLink = node; }}),
            head: {appendChild: script => { state.script = script; script.onload(); }},
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
test('Clerk configuration failure offers retry without error copy or mounting sign-in', async () => {
    const state = await run({configFails: true});
    assert.equal(state.retry.hidden, false);
    assert.equal(state.message.textContent, '');
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
