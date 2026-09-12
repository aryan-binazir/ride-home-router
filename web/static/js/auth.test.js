const assert = require('node:assert/strict');
const {test} = require('node:test');
const vm = require('node:vm');
const fs = require('node:fs');
const path = require('node:path');
const source = fs.readFileSync(path.join(__dirname, 'auth.js'), 'utf8');

async function run({signedIn = false, status = 200, pathname = '/sign-in', configFails = false} = {}) {
    const state = {message: {textContent: ''}, button: {hidden: true}, requests: []};
    const clerk = {
        user: signedIn ? {id: 'user_test'} : null,
        load: async () => { state.loaded = true; },
        mountSignIn: (_, options) => { state.options = options; },
        signOut: options => { state.signOut = options; },
    };
    state.button.addEventListener = (_, callback) => { state.click = callback; };
    await vm.runInNewContext(source, {
        window: {Clerk: clerk},
        location: {pathname, replace: value => { state.redirect = value; }},
        document: {
            getElementById: id => id === 'auth-message' ? state.message : {},
            createElement: () => ({dataset: {}}),
            head: {appendChild: script => { state.script = script; script.onload(); }},
            querySelectorAll: () => [state.button],
        },
        fetch: async url => {
            state.requests.push(url);
            if (url === '/auth/config') return {ok: !configFails, json: async () => ({publishableKey: 'pk_test_fixture', scriptURL: 'https://fixture.clerk.accounts.dev/clerk.js'})};
            return {ok: status === 200, status};
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
        assert.ok(state.message.textContent.length > 0);
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
test('Clerk configuration failure displays a generic error without mounting sign-in', async () => {
    const state = await run({configFails: true});
    assert.match(state.message.textContent, /unavailable/);
    assert.equal(state.loaded, undefined);
    assert.equal(state.options, undefined);
});
