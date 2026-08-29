'use strict';

const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');

const mobileScript = fs.readFileSync(path.join(__dirname, 'mobile.js'), 'utf8');

function createClipboardFixture({ clipboard, execCommandResult = false } = {}) {
    let clickHandler;
    const timers = [];
    const source = {
        value: 'Route handoff text',
        selectCalls: 0,
        select() { this.selectCalls += 1; },
    };
    const button = {
        dataset: { copyTarget: 'route-copy' },
        textContent: 'Copy route',
    };
    const document = {
        execCommandCalls: [],
        addEventListener(type, handler) {
            if (type === 'click') clickHandler = handler;
        },
        execCommand(command) {
            this.execCommandCalls.push(command);
            return execCommandResult;
        },
        getElementById(id) {
            return id === 'route-copy' ? source : null;
        },
    };
    const context = {
        document,
        navigator: clipboard === undefined ? {} : { clipboard },
        window: {
            setTimeout(callback, delay) {
                timers.push([callback, delay]);
            },
        },
    };
    vm.runInNewContext(mobileScript, context, { filename: 'mobile.js' });

    return {
        button,
        document,
        source,
        timers,
        async clickCopy() {
            await clickHandler({
                target: {
                    closest(selector) {
                        return selector === '[data-copy-target]' ? button : null;
                    },
                },
            });
        },
    };
}

test('copy button uses the Clipboard API and shows success', async () => {
    const copied = [];
    const fixture = createClipboardFixture({
        clipboard: { writeText: async value => copied.push(value) },
    });

    await fixture.clickCopy();

    assert.deepEqual(copied, ['Route handoff text']);
    assert.equal(fixture.button.textContent, 'Copied');
    assert.equal(fixture.source.selectCalls, 0);
    assert.equal(fixture.document.execCommandCalls.length, 0);
});

test('copy button selects the textarea and falls back to execCommand', async () => {
    const fixture = createClipboardFixture({ execCommandResult: true });

    await fixture.clickCopy();

    assert.equal(fixture.source.selectCalls, 1);
    assert.deepEqual(fixture.document.execCommandCalls, ['copy']);
    assert.equal(fixture.button.textContent, 'Copied');
});

test('copy button shows a visible failure state when both copy methods fail', async () => {
    const fixture = createClipboardFixture({
        clipboard: { writeText: async () => { throw new Error('clipboard unavailable'); } },
        execCommandResult: false,
    });

    await fixture.clickCopy();

    assert.equal(fixture.source.selectCalls, 1);
    assert.deepEqual(fixture.document.execCommandCalls, ['copy']);
    assert.equal(fixture.button.textContent, 'Copy failed');
});
