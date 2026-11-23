'use strict';

const test = require('node:test');
const assert = require('node:assert/strict');

const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');

const planner = require('./event-planner.js');
const { createRouteHandoff, createRouteSessionOrchestrator, localISODate } = planner;

test('localISODate uses the local calendar day, not the UTC one', () => {
    const lateEvening = new Date(2026, 2, 14, 23, 30);
    assert.equal(localISODate(lateEvening), '2026-03-14');
    assert.equal(localISODate(new Date(2026, 0, 5, 0, 10)), '2026-01-05');
});

function nodeList(items) {
    const list = {
        length: items.length,
        forEach: callback => items.forEach(callback),
        [Symbol.iterator]: () => items[Symbol.iterator](),
    };
    items.forEach((item, index) => {
        list[index] = item;
    });
    return list;
}

function createRouteFixture({ mode = 'dropoff' } = {}) {
    const stopEta = { textContent: '' };
    const stop = {
        dataset: {
            participantName: 'Sam Rider',
            participantAddress: '5 Rider Street',
            participantLat: '40.2',
            participantLng: '-74.2',
            stopCumulativeDurationSecs: '900',
        },
        querySelector: selector => selector === '.stop-eta' ? stopEta : null,
    };
    let container;
    const routeCard = {
        dataset: {
            driverName: 'Jordan Driver',
            driverAddress: '9 Driver Lane',
            driverLat: '40.1',
            driverLng: '-74.1',
            routeDurationSecs: '1800',
        },
        querySelectorAll: selector => selector === '.stop-item' ? nodeList([stop]) : nodeList([]),
        closest: selector => selector === '.routes-container' ? container : null,
    };
    container = {
        dataset: {
            activityLocationName: 'Wednesday Night Church',
            activityLocationAddress: '1 Church Road',
            activityLocationLat: '40.4',
            activityLocationLng: '-74.4',
            routeMode: mode,
            routeTime: '12:00',
        },
        querySelectorAll: selector => {
            if (selector === '.route-card') return nodeList([routeCard]);
            if (selector === '.stop-eta') return nodeList([stopEta]);
            return nodeList([]);
        },
    };

    return { container, routeCard, stop, stopEta };
}

test('driver copy defaults to the driver audience and copies the complete route', async () => {
    const copied = [];
    const { container, routeCard, stop } = createRouteFixture();
    delete container.dataset.routeMode;
    const secondStop = {
        dataset: {
            participantName: 'Riley Rider',
            participantAddress: '6 Rider Street',
            participantLat: '40.3',
            participantLng: '-74.3',
            stopCumulativeDurationSecs: '1200',
        },
        querySelector: () => null,
    };
    const thirdStop = {
        dataset: {
            participantName: 'Morgan Rider',
            participantAddress: '7 Rider Street',
            participantLat: '40.35',
            participantLng: '-74.35',
            stopCumulativeDurationSecs: '1500',
        },
        querySelector: () => null,
    };
    routeCard.querySelectorAll = selector => selector === '.stop-item'
        ? nodeList([stop, secondStop, thirdStop])
        : nodeList([]);
    const handoff = createRouteHandoff({
        platform: {
            copyText: async text => copied.push(text),
            openUrl: async () => {},
            notify: () => {},
        },
        formatTime: value => value.toTimeString().slice(0, 5),
    });

    assert.equal(await handoff.copyRoute(routeCard), true);
    assert.deepEqual(copied, [
        'Activity Location: Wednesday Night Church\n1 Church Road\n\n' +
        'Driver: Jordan Driver\n9 Driver Lane\n' +
        '1. 12:17 - Sam Rider - 5 Rider Street\n' +
        '2. 12:22 - Riley Rider - 6 Rider Street\n' +
        '3. 12:27 - Morgan Rider - 7 Rider Street\n\n' +
        'Maps: https://www.google.com/maps/dir/?api=1&travelmode=driving&destination=40.1%2C-74.1&dir_action=navigate&waypoints=40.2%2C-74.2%7C40.3%2C-74.3%7C40.35%2C-74.35\n',
    ]);
});

test('parent copy keeps the manifest and ETAs while omitting private route details', async () => {
    const copied = [];
    const { routeCard } = createRouteFixture();
    const handoff = createRouteHandoff({
        platform: {
            copyText: async text => copied.push(text),
            openUrl: async () => {},
            notify: () => {},
        },
        formatTime: value => value.toTimeString().slice(0, 5),
    });

    assert.equal(await handoff.copyRoute(routeCard, 'parent'), true);
    assert.deepEqual(copied, [
        'Activity Location: Wednesday Night Church\n1 Church Road\n\n' +
        'Driver: Jordan Driver\n' +
        '1. 12:17 - Sam Rider\n',
    ]);
});

test('copy all routes shares route formatting and omits the address separator when no address exists', async () => {
    const copied = [];
    const { container, routeCard } = createRouteFixture();
    const secondStop = {
        dataset: {
            participantName: 'Taylor Rider',
            participantAddress: '',
            participantLat: '40.5',
            participantLng: '-74.5',
            stopCumulativeDurationSecs: '1200',
        },
        querySelector: () => null,
    };
    const secondRouteCard = {
        dataset: {
            driverName: 'Casey Driver',
            driverAddress: '10 Driver Lane',
            driverLat: '40.6',
            driverLng: '-74.6',
            routeDurationSecs: '2400',
        },
        querySelectorAll: selector => selector === '.stop-item' ? nodeList([secondStop]) : nodeList([]),
        closest: selector => selector === '.routes-container' ? container : null,
    };
    container.querySelectorAll = selector => selector === '.route-card'
        ? nodeList([routeCard, secondRouteCard])
        : nodeList([]);
    const handoff = createRouteHandoff({
        platform: {
            copyText: async text => copied.push(text),
            openUrl: async () => {},
            notify: () => {},
        },
        formatTime: value => value.toTimeString().slice(0, 5),
    });

    assert.equal(await handoff.copyAllRoutes(container), true);
    assert.deepEqual(copied, [
        'Activity Location: Wednesday Night Church\n1 Church Road\n\n' +
        'Driver: Jordan Driver\n9 Driver Lane\n' +
        '1. 12:17 - Sam Rider - 5 Rider Street\n\n' +
        'Maps: https://www.google.com/maps/dir/?api=1&travelmode=driving&destination=40.1%2C-74.1&dir_action=navigate&waypoints=40.2%2C-74.2\n\n' +
        'Driver: Casey Driver\n10 Driver Lane\n' +
        '1. 12:22 - Taylor Rider\n\n' +
        'Maps: https://www.google.com/maps/dir/?api=1&travelmode=driving&destination=40.6%2C-74.6&dir_action=navigate&waypoints=40.5%2C-74.5\n',
    ]);
});

test('single-route clipboard failure returns false and reports an error', async () => {
    const notifications = [];
    const { routeCard } = createRouteFixture();
    const handoff = createRouteHandoff({
        platform: {
            copyText: async () => { throw new Error('clipboard unavailable'); },
            openUrl: async () => {},
            notify: (message, type) => notifications.push([message, type]),
        },
    });

    assert.equal(await handoff.copyRoute(routeCard), false);
    assert.deepEqual(notifications, [['Could not copy. Try again.', 'error']]);
});

test('preview opens a deduplicated pickup route without navigation mode', async () => {
    const opened = [];
    const { routeCard, stop } = createRouteFixture({ mode: 'pickup' });
    const duplicateStop = {
        dataset: {
            ...stop.dataset,
            participantName: 'Duplicate Rider',
            participantAddress: 'Another Label',
            participantLat: '40.2000001',
            participantLng: '-74.2000001',
        },
        querySelector: () => null,
    };
    routeCard.querySelectorAll = selector => selector === '.stop-item'
        ? nodeList([stop, duplicateStop])
        : nodeList([]);
    const handoff = createRouteHandoff({
        platform: {
            copyText: async () => {},
            openUrl: async url => opened.push(url),
            notify: () => {},
        },
    });

    assert.equal(await handoff.previewRoute(routeCard), true);
    assert.deepEqual(opened, [
        'https://www.google.com/maps/dir/?api=1&travelmode=driving&destination=40.4%2C-74.4&origin=40.1%2C-74.1&waypoints=40.2%2C-74.2',
    ]);
});

test('preview preserves ordered dropoff stops between the activity and driver', async () => {
    const opened = [];
    const { routeCard, stop } = createRouteFixture();
    const secondStop = {
        dataset: {
            participantName: 'Riley Rider',
            participantAddress: '6 Rider Street',
            participantLat: '40.3',
            participantLng: '-74.3',
            stopCumulativeDurationSecs: '1200',
        },
        querySelector: () => null,
    };
    routeCard.querySelectorAll = selector => selector === '.stop-item'
        ? nodeList([stop, secondStop])
        : nodeList([]);
    const handoff = createRouteHandoff({
        platform: {
            copyText: async () => {},
            openUrl: async url => opened.push(url),
            notify: () => {},
        },
    });

    assert.equal(await handoff.previewRoute(routeCard), true);
    assert.deepEqual(opened, [
        'https://www.google.com/maps/dir/?api=1&travelmode=driving&destination=40.1%2C-74.1&origin=40.4%2C-74.4&waypoints=40.2%2C-74.2%7C40.3%2C-74.3',
    ]);
});

test('preview reports a warning when no valid route can be built', async () => {
    const opened = [];
    const notifications = [];
    const { routeCard } = createRouteFixture();
    routeCard.querySelectorAll = () => nodeList([]);
    const handoff = createRouteHandoff({
        platform: {
            copyText: async () => {},
            openUrl: async url => opened.push(url),
            notify: (message, type) => notifications.push([message, type]),
        },
    });

    assert.equal(await handoff.previewRoute(routeCard), false);
    assert.deepEqual(opened, []);
    assert.deepEqual(notifications, [[
        'This route cannot be opened in Google Maps.',
        'warning',
    ]]);
});

test('preview open failure returns false and reports an error', async () => {
    const notifications = [];
    const { routeCard } = createRouteFixture();
    const handoff = createRouteHandoff({
        platform: {
            copyText: async () => {},
            openUrl: async () => { throw new Error('browser unavailable'); },
            notify: (message, type) => notifications.push([message, type]),
        },
    });

    assert.equal(await handoff.previewRoute(routeCard), false);
    assert.deepEqual(notifications, [['Could not open Google Maps. Check whether your browser blocked the new tab.', 'error']]);
});

test('ETA population applies dropoff slack through the route handoff', () => {
    const { container, stopEta } = createRouteFixture();
    const handoff = createRouteHandoff({
        platform: {
            copyText: async () => {},
            openUrl: async () => {},
            notify: () => {},
        },
        formatTime: value => value.toTimeString().slice(0, 5),
    });

    handoff.populateEtas(container);

    assert.equal(stopEta.textContent, '12:17');
});

test('ETA population counts backward for pickup routes', () => {
    const { container, stopEta } = createRouteFixture({ mode: 'pickup' });
    const handoff = createRouteHandoff({
        platform: {
            copyText: async () => {},
            openUrl: async () => {},
            notify: () => {},
        },
        formatTime: value => value.toTimeString().slice(0, 5),
    });

    handoff.populateEtas(container);

    assert.equal(stopEta.textContent, '11:45');
});

function createSaveForm({ sessionId = 'session-a', saveEnabled = true, submitButton = true } = {}) {
    const fields = { session_id: { value: sessionId } };
    return {
        submitCount: 0,
        elements: { namedItem: name => fields[name] || null },
        getAttribute: name => name === 'hx-post' && saveEnabled ? '/api/v1/events' : null,
        querySelector: selector => selector === 'button[type="submit"]' && submitButton
            ? { disabled: !saveEnabled }
            : null,
        requestSubmit() { this.submitCount += 1; },
    };
}

function createRouteSessionHarness({ activeSessionId = 'session-a', getLiveForm = () => null, request } = {}) {
    const sent = [], opened = [], notifications = [];
    let currentSessionId = activeSessionId, canSave = true;
    const resultsSection = { set innerHTML(html) {}, querySelector: () => null, querySelectorAll: () => [] };
    const document = {
        querySelector(selector) {
            if (selector === '.routes-container') return currentSessionId ? { dataset: { sessionId: currentSessionId, outOfBalance: 'false' } } : null;
            if (selector === '#results-section form input[name="session_id"]') {
                const form = getLiveForm();
                return form ? { closest: () => form } : null;
            }
            return null;
        },
        getElementById: id => id === 'results-section' ? resultsSection : null,
    };
    const scheduled = new Map();
    let nextTimer = 0;
    const orchestrator = createRouteSessionOrchestrator({
        document,
        htmx: { process() {}, ajax: async (method, url) => opened.push([method, url]) },
        readPlanState: () => ({ canSave, sessionId: currentSessionId }),
        request: async (url, options) => {
            sent.push({ url, options });
            return request ? request(url, options) : { ok: true, text: async () => 'routes' };
        },
        schedule: callback => { scheduled.set(++nextTimer, callback); return nextTimer; },
        cancel: timer => scheduled.delete(timer),
        reportError() {},
        notify: (...args) => notifications.push(args),
        reportSaveBlocked: status => notifications.push(status),
        afterRender() {},
        pendingChanged() {},
    });
    return {
        notifications, orchestrator, opened, sent,
        setActiveSessionId: value => { currentSessionId = value; },
        setSaveable: value => { canSave = value; },
    };
}

async function settleEdits() {
    for (let turn = 0; turn < 50; turn++) await Promise.resolve();
}

for (const [reason, liveForm] of [
    ['absent', null],
    ['disabled', createSaveForm({ saveEnabled: false })],
    ['missing its submit control', createSaveForm({ submitButton: false })],
    ['for another session', createSaveForm({ sessionId: 'session-b' })],
]) {
    test(`Save does not submit a replacement form that is ${reason}`, async () => {
        const original = createSaveForm();
        const harness = createRouteSessionHarness({ getLiveForm: () => liveForm });
        harness.orchestrator.move(1, 0, 1);
        assert.equal(harness.orchestrator.save(original), true);
        await settleEdits();
        assert.equal(original.submitCount, 0);
        assert.equal(liveForm?.submitCount || 0, 0);
    });
}

test('Save remembers a rejected batch even when later batches succeed', async () => {
    const form = createSaveForm();
    let count = 0;
    const harness = createRouteSessionHarness({ getLiveForm: () => form, request: async () => ({ ok: ++count !== 1, status: 400, text: async () => 'batch' }) });
    for (let id = 1; id <= 129; id++) harness.orchestrator.move(id, 0, 1);
    assert.equal(harness.orchestrator.save(form), true);
    await settleEdits();
    assert.equal(count, 3);
    assert.equal(form.submitCount, 0);
    assert.equal(harness.orchestrator.hasPending(), false);
    assert.deepEqual(harness.sent.map(({ options }) => {
        const payload = JSON.parse(options.body);
        return payload.moves?.length || 1;
    }), [64, 64, 1]);
});

test('Save waits for its own session after an older session fails', async () => {
    let finishOld;
    const formA = createSaveForm(), formB = createSaveForm({ sessionId: 'session-b' });
    let liveForm = formA;
    const harness = createRouteSessionHarness({
        getLiveForm: () => liveForm,
        request: async (url, options) => JSON.parse(options.body).session_id === 'session-a'
            ? new Promise(resolve => { finishOld = resolve; })
            : { ok: true, text: async () => 'session b' },
    });
    const owner = harness.orchestrator;
    owner.move(1, 0, 1);
    owner.save(formA);
    harness.setActiveSessionId('session-b'); liveForm = formB;
    owner.move(2, 0, 1); owner.save(formB);
    finishOld({ ok: false, status: 400, text: async () => 'session a failed' });
    await settleEdits();
    assert.deepEqual(harness.sent.map(({ options }) => JSON.parse(options.body).session_id), ['session-a', 'session-b']);
    assert.equal(formA.submitCount, 0);
    assert.equal(formB.submitCount, 1);
});

test('queued Move batches retain the wire payloads and dispatch headers', async () => {
    const harness = createRouteSessionHarness();
    const owner = harness.orchestrator;
    owner.move(1, 0, 1); owner.move(2, 1, 0);
    assert.equal(harness.sent.length, 0);
    await owner.openEditor('/editor');
    owner.move(3, 0, 2);
    await owner.openEditor('/editor');
    assert.deepEqual(harness.sent.map(({ options }) => JSON.parse(options.body)), [
        { session_id: 'session-a', moves: [
            { participant_id: 1, from_route_index: 0, to_route_index: 1, insert_at_position: -1 },
            { participant_id: 2, from_route_index: 1, to_route_index: 0, insert_at_position: -1 },
        ] },
        { session_id: 'session-a', participant_id: 3, from_route_index: 0, to_route_index: 2, insert_at_position: -1 },
    ]);
    assert.deepEqual(harness.sent[0].options.headers, { 'Content-Type': 'application/json', 'HX-Request': 'true', 'X-Route-Fragment': 'true', 'X-Route-Balance': 'false' });
    assert.equal(harness.sent[0].options.method, 'POST');
    assert.equal(owner.hasPending(), false);
    assert.deepEqual(harness.opened, [['GET', '/editor'], ['GET', '/editor']]);
});

test('driver copy shows friendly location names while Maps keeps real coordinates', async () => {
    const copied = [];
    const { container, routeCard } = createRouteFixture();
    routeCard.dataset.driverAddressName = 'Driver Home';
    const stopItems = routeCard.querySelectorAll('.stop-item');
    stopItems[0].dataset.participantAddressName = 'Collins Crossing';
    const handoff = createRouteHandoff({
        platform: {
            copyText: async text => copied.push(text),
            openUrl: async () => {},
            notify: () => {},
        },
        formatTime: value => value.toTimeString().slice(0, 5),
    });

    assert.equal(await handoff.copyRoute(routeCard), true);
    assert.match(copied[0], /Driver: Jordan Driver\nDriver Home \(9 Driver Lane\)\n/);
    assert.match(copied[0], /Sam Rider - Collins Crossing \(5 Rider Street\)/);
    assert.match(copied[0], /destination=40.1%2C-74.1/);
    assert.doesNotMatch(copied[0], /Driver\+Home/);
});

class HTMLFormElement {}

const SELECTOR_TOKEN = /^(?:([a-zA-Z][\w-]*)|#([\w-]+)|\.([\w-]+)|\[([\w-]+)(?:=["']([^"']*)["'])?\]|:([\w-]+))/;

function camel(name) {
    return name.replace(/-([a-z])/g, (_, letter) => letter.toUpperCase());
}

function attributeValue(element, name) {
    if (name.startsWith('data-')) return element.dataset[camel(name.slice(5))];
    if (element[name] !== undefined) return element[name];
    return element.attributes[name];
}

function matchesCompound(element, compound) {
    let rest = compound;
    while (rest.length > 0) {
        const token = SELECTOR_TOKEN.exec(rest);
        if (!token) throw new Error(`unsupported selector: ${compound}`);
        const [matched, tag, id, className, attribute, attributeValueWanted, pseudo] = token;
        if (tag && element.tagName !== tag) return false;
        if (id && element.id !== id) return false;
        if (className && !element.classList.contains(className)) return false;
        if (attribute) {
            const actual = attributeValue(element, attribute);
            if (actual === undefined || actual === null) return false;
            if (attributeValueWanted !== undefined && String(actual) !== attributeValueWanted) return false;
        }
        if (pseudo === 'checked' && element.checked !== true) return false;
        if (pseudo && pseudo !== 'checked') throw new Error(`unsupported pseudo: ${pseudo}`);
        rest = rest.slice(matched.length);
    }
    return true;
}

function matchesSelector(element, selector) {
    return selector.split(',').some(group => {
        const compounds = group.trim().split(/\s+/);
        const own = compounds.pop();
        if (!matchesCompound(element, own)) return false;

        let ancestor = element.parentNode;
        for (const compound of compounds.reverse()) {
            while (ancestor && !matchesCompound(ancestor, compound)) ancestor = ancestor.parentNode;
            if (!ancestor) return false;
            ancestor = ancestor.parentNode;
        }
        return true;
    });
}

function domNode(tagName, props = {}) {
    const { classes = [], children = [], form = false, ...rest } = props;
    const classSet = new Set(classes);
    const node = {
        tagName,
        id: '',
        attributes: {},
        dataset: {},
        children: [],
        parentNode: null,
        textContent: '',
        listeners: {},
        classList: {
            add: value => classSet.add(value),
            remove: value => classSet.delete(value),
            contains: value => classSet.has(value),
            toggle: (value, force) => {
                const next = force === undefined ? !classSet.has(value) : Boolean(force);
                if (next) classSet.add(value); else classSet.delete(value);
                return next;
            },
        },
        get className() { return [...classSet].join(' '); },
        set className(value) {
            classSet.clear();
            String(value).split(/\s+/).filter(Boolean).forEach(entry => classSet.add(entry));
        },
        get innerHTML() { return this._html || ''; },
        set innerHTML(value) {
            this._html = value;
            this.children.forEach(child => { child.parentNode = null; });
            this.children = [];
        },
        get firstChild() { return this.children[0] || null; },
        appendChild(child) {
            child.parentNode = this;
            this.children.push(child);
            return child;
        },
        insertBefore(child, reference) {
            child.parentNode = this;
            const index = reference ? this.children.indexOf(reference) : -1;
            this.children.splice(index < 0 ? this.children.length : index, 0, child);
            return child;
        },
        remove() {
            if (!this.parentNode) return;
            this.parentNode.children = this.parentNode.children.filter(child => child !== this);
            this.parentNode = null;
        },
        setAttribute(name, value) { this.attributes[name] = String(value); },
        getAttribute(name) { return this.attributes[name] ?? null; },
        removeAttribute(name) { delete this.attributes[name]; },
        matches(selector) { return matchesSelector(this, selector); },
        closest(selector) {
            let current = this;
            while (current) {
                if (matchesSelector(current, selector)) return current;
                current = current.parentNode;
            }
            return null;
        },
        querySelectorAll(selector) {
            const found = [];
            const walk = element => element.children.forEach(child => {
                if (matchesSelector(child, selector)) found.push(child);
                walk(child);
            });
            walk(this);
            return found;
        },
        querySelector(selector) { return this.querySelectorAll(selector)[0] || null; },
        addEventListener(type, handler) {
            (this.listeners[type] = this.listeners[type] || []).push(handler);
        },
        dispatchEvent(event) {
            event.target = event.target || this;
            let current = this;
            while (current && !event.stopped) {
                for (const handler of current.listeners[event.type] || []) {
                    handler(event);
                    if (event.stopped) break;
                }
                current = current.parentNode;
            }
            return !event.defaultPrevented;
        },
        scrollIntoView() {},
    };
    Object.assign(node, rest);
    children.forEach(child => node.appendChild(child));
    if (form) Object.setPrototypeOf(node, HTMLFormElement.prototype);
    return node;
}

function fakeEvent(type, detail) {
    return {
        type,
        detail,
        defaultPrevented: false,
        stopped: false,
        preventDefault() { this.defaultPrevented = true; },
        stopImmediatePropagation() { this.stopped = true; },
    };
}

function fakeLocalStorage() {
    const entries = new Map();
    return {
        entries,
        getItem: key => (entries.has(key) ? entries.get(key) : null),
        setItem: (key, value) => entries.set(key, String(value)),
        removeItem: key => entries.delete(key),
    };
}

function storedSessionId(planner) {
    const stored = planner.storage.getItem('ride-home-router:active-session:v2');
    return stored ? JSON.parse(stored).id : null;
}

const PLANNER_SOURCE = fs.readFileSync(path.join(__dirname, 'event-planner.js'), 'utf8');

function bootPlanner({ mode = 'dropoff', legacySessionId = null } = {}) {
    const initialSessionId = legacySessionId || 'session-1';
    const participants = ['1', '2'].map(value => domNode('input', {
        classes: ['participant-checkbox'],
        value,
        checked: true,
        dataset: { capacity: '0' },
    }));
    const driver = domNode('input', {
        classes: ['driver-checkbox'],
        value: '10',
        checked: true,
        dataset: { capacity: '4' },
    });
    const vanSelect = domNode('select', {
        id: 'van-assignment-10',
        classes: ['van-assignment-select'],
        dataset: { driverId: '10' },
        value: '',
        disabled: false,
        options: [{ value: '', dataset: { capacity: '4' } }, { value: '3', dataset: { capacity: '8' } }],
    });
    Object.defineProperty(vanSelect, 'selectedIndex', {
        get() { return Math.max(0, this.options.findIndex(option => option.value === this.value)); },
    });
    const driverRow = domNode('label', {
        classes: ['select-row'],
        children: [driver, domNode('span', { classes: ['van-assignment-inline'], children: [vanSelect] })],
    });
    const activityLocation = domNode('select', { name: 'activity_location_id', value: '7' });
    const dropoff = domNode('input', { name: 'mode', value: 'dropoff', checked: mode === 'dropoff' });
    const pickup = domNode('input', { name: 'mode', value: 'pickup', checked: mode === 'pickup' });
    const routeTime = domNode('input', { id: 'route-time', name: 'route_time', value: '15:30' });
    const search = domNode('input', {
        value: '',
        dataset: { filterRole: 'search', listId: 'participants-selection' },
    });
    const form = domNode('form', {
        id: 'event-form',
        form: true,
        children: [activityLocation, dropoff, pickup, routeTime, search,
            ...participants.map(input => domNode('label', { classes: ['select-row'], children: [input] })),
            driverRow],
    });

    const banner = domNode('div', { classes: ['alert', 'planner-plan-state-banner'], hidden: true });
    const saveButton = domNode('button', { type: 'submit', disabled: false, dataset: { sessionAction: 'save' } });
    const copyButton = domNode('button', { disabled: false, dataset: { sessionAction: 'copy' } });
    const previewButton = domNode('button', { disabled: false, dataset: { sessionAction: 'preview' } });
    const outOfBalanceCopy = domNode('button', { dataset: { sessionAction: 'copy' }, disabled: true });
    const saveForm = domNode('form', {
        form: true,
        classes: ['save-event-card'],
        attributes: { 'hx-post': '/api/v1/events' },
        children: [domNode('input', { name: 'session_id', value: initialSessionId })],
    });
    function renderServerSaveFields() {
        saveForm.querySelectorAll('input[name="event_date"], textarea[name="notes"]')
            .forEach(field => field.remove());
        saveForm.appendChild(domNode('input', { type: 'date', name: 'event_date', value: '2026-09-01' }));
        saveForm.appendChild(domNode('textarea', { name: 'notes', value: '' }));
        saveForm.appendChild(saveButton);
    }
    renderServerSaveFields();
    saveForm.elements = {
        namedItem: name => saveForm.querySelector(`[name="${name}"]`),
    };
    saveForm.requestSubmit = function() {
        const event = Object.assign(fakeEvent('submit'), { target: this });
        this.dispatchEvent(event);
        return event;
    };
    const resultsBody = domNode('div', {
        classes: ['results-body'],
        children: [banner, copyButton, previewButton, outOfBalanceCopy, saveForm],
    });
    const routesContainer = domNode('div', {
        classes: ['routes-container'],
        dataset: { sessionId: initialSessionId, routeMode: mode },
        children: [resultsBody],
    });
    let renderedSessionCount = 0;
    const resultsSection = domNode('div', { id: 'results-section', children: [routesContainer] });
    Object.defineProperty(resultsSection, 'innerHTML', {
        set(html) {
            this.children.forEach(child => { child.parentNode = null; });
            this.children = [];
            if (html) this.appendChild(routesContainer);
        },
    });

    const recalcVanSelect = domNode('select', {
        classes: ['org-vehicle-select'],
        dataset: { driverId: '10' },
        value: '',
    });
    const recalcForm = domNode('form', {
        id: 'recalc-form',
        form: true,
        children: [
            domNode('input', { type: 'hidden', name: 'participant_ids', value: '1' }),
            domNode('input', { type: 'hidden', name: 'participant_ids', value: '2' }),
            domNode('input', { type: 'hidden', name: 'driver_ids', value: '10' }),
            domNode('input', { type: 'hidden', name: 'activity_location_id', value: '7' }),
            domNode('input', { type: 'hidden', name: 'mode', value: mode }),
            domNode('input', { type: 'hidden', name: 'route_time', value: '15:30' }),
            recalcVanSelect,
        ],
    });

    const routeTimeLabel = domNode('label', { id: 'route-time-label', textContent: 'x' });
    const routeTimeHelp = domNode('p', { id: 'route-time-help', textContent: 'x' });
    const body = domNode('body', {
        children: [form, resultsSection, recalcForm, routeTimeLabel, routeTimeHelp,
            domNode('template', { id: 'results-empty-state-template' })],
    });
    const root = domNode('html', { children: [body], classes: ['planner-restoring'] });

    const document = {
        readyState: 'complete',
        documentElement: root,
        body,
        listeners: root.listeners,
        addEventListener: (type, handler) => root.addEventListener(type, handler),
        getElementById(id) {
            return root.querySelectorAll(`#${id}`)[0] || null;
        },
        querySelector: selector => root.querySelector(selector),
        querySelectorAll: selector => root.querySelectorAll(selector),
        createElement: tagName => domNode(tagName),
    };
    body.parentNode = root;

    const storage = fakeLocalStorage();
    if (legacySessionId) {
        storage.setItem('ride-home-router:active-session-id', legacySessionId);
    }
    const scheduledCallbacks = new Map();
    let nextTimeoutId = 0;
    const fetches = [];
    const context = {
        document,
        showConfirmDialog: async () => true,
        console,
        HTMLFormElement,
        Event: class { constructor(type) { return fakeEvent(type); } },
        AbortController,
        AbortSignal,
        htmx: { process() {} },
        fetch: async (url, options) => {
            fetches.push({ url, options });
            return { ok: true, status: 200, text: async () => '<div>routes</div>' };
        },
        setTimeout: callback => {
            nextTimeoutId += 1;
            scheduledCallbacks.set(nextTimeoutId, callback);
            return nextTimeoutId;
        },
        clearTimeout: timeoutId => { scheduledCallbacks.delete(timeoutId); },
        JSON,
        Intl,
        window: {
            showConfirmDialog: async () => true,
            localStorage: storage,
            matchMedia: () => ({ matches: true }),
            requestAnimationFrame: () => 0,
        },
    };
    vm.runInNewContext(PLANNER_SOURCE, context, { filename: 'event-planner.js' });

    return {
        activityLocation,
        banner,
        context,
        copyButton,
        previewButton,
        document,
        driver,
        dropoff,
        outOfBalanceCopy,
        participants,
        pickup,
        routeTime,
        recalcVanSelect,
        routeTimeLabel,
        routesContainer,
        vanSelect,
        resultsSection,
        saveButton,
        saveForm,
        storage,
        fetches,
        async flushTimers() {
            const callbacks = Array.from(scheduledCallbacks.values());
            scheduledCallbacks.clear();
            for (const callback of callbacks) await callback();
        },
        async settleRestore() {
            await new Promise(setImmediate);
        },
        saveSucceeded() {
            const saveResult = domNode('div', {
                id: 'save-result',
                children: [domNode('div', { classes: ['alert-success'] })],
            });
            body.appendChild(saveResult);
            body.dispatchEvent(Object.assign(fakeEvent('htmx:afterSwap'), { detail: { target: saveResult } }));
        },
        startCalculation(xhr = {}, elt = { id: 'calculate-btn' }) {
            body.dispatchEvent(Object.assign(fakeEvent('htmx:beforeRequest'), { detail: { elt, xhr } }));
            return xhr;
        },
        failCalculation(xhr, elt = { id: 'calculate-btn' }) {
            body.dispatchEvent(Object.assign(fakeEvent('htmx:responseError'), { detail: { elt, xhr } }));
        },
        finishCalculation(xhr, { hasRoutes = true } = {}) {
            const detail = { target: resultsSection, xhr, shouldSwap: true };
            body.dispatchEvent(Object.assign(fakeEvent('htmx:beforeSwap'), { detail }));
            if (!detail.shouldSwap) return;
            resultsSection.children.forEach(child => { child.parentNode = null; });
            resultsSection.children = [];
            if (hasRoutes) {
                renderedSessionCount += 1;
                const sessionId = `session-${renderedSessionCount}`;
                routesContainer.dataset.sessionId = sessionId;
                saveForm.elements.namedItem('session_id').value = sessionId;
                resultsSection.appendChild(routesContainer);
                renderServerSaveFields();
            }
            body.dispatchEvent(Object.assign(fakeEvent('htmx:afterSwap'), { detail: { target: resultsSection, xhr } }));
            root.dispatchEvent(Object.assign(fakeEvent('htmx:afterSettle'), { target: resultsSection }));
        },
        calculate({ elt = { id: 'calculate-btn' }, xhr = {} } = {}) {
            this.startCalculation(xhr, elt);
            this.finishCalculation(xhr);
        },
        saveFields() {
            return {
                eventDate: saveForm.querySelector('input[name="event_date"]'),
                notes: saveForm.querySelector('textarea[name="notes"]'),
            };
        },
        change(target) {
            root.dispatchEvent(Object.assign(fakeEvent('change'), { target }));
        },
        submitSave() {
            return saveForm.requestSubmit();
        },
    };
}

test('changing a route-defining input after calculating blocks saving until recalculation', () => {
    const planner = bootPlanner();

    planner.calculate();
    const afterCalculate = {
        saveDisabled: planner.saveButton.disabled,
        bannerHidden: planner.banner.hidden,
        storedSession: storedSessionId(planner),
    };

    planner.participants[1].checked = false;
    planner.change(planner.participants[1]);

    assert.deepEqual({
        afterCalculate,
        saveDisabled: planner.saveButton.disabled,
        banner: planner.banner.hidden ? '' : planner.banner.textContent,
        storedSession: storedSessionId(planner),
        saveBlocked: planner.submitSave().defaultPrevented,
    }, {
        afterCalculate: { saveDisabled: false, bannerHidden: true, storedSession: 'session-1' },
        saveDisabled: true,
        banner: 'Plan changed — recalculate routes before copying or saving them.',
        storedSession: null,
        saveBlocked: true,
    });

    planner.calculate();

    assert.deepEqual({
        saveDisabled: planner.saveButton.disabled,
        bannerHidden: planner.banner.hidden,
        saveBlocked: planner.submitSave().defaultPrevented,
    }, { saveDisabled: false, bannerHidden: true, saveBlocked: false });
});

test('requestSubmit rechecks live planner inputs even when no change event fired', () => {
    const planner = bootPlanner();

    planner.calculate();
    planner.routeTime.value = '16:00';

    assert.deepEqual({
        saveBlocked: planner.submitSave().defaultPrevented,
        saveDisabled: planner.saveButton.disabled,
        banner: planner.banner.hidden ? '' : planner.banner.textContent,
    }, {
        saveBlocked: true,
        saveDisabled: true,
        banner: 'Plan changed — recalculate routes before copying or saving them.',
    });
});

test('a queued participant move rechecks unannounced input changes before posting', async () => {
    const planner = bootPlanner();

    planner.calculate();
    planner.context.moveParticipant(1, 0, 1);
    planner.routeTime.value = '16:00';
    await planner.flushTimers();

    assert.deepEqual({
        movePosted: planner.fetches.some(request => request.url === '/api/v1/routes/edit/move-participant'),
        saveDisabled: planner.saveButton.disabled,
    }, { movePosted: false, saveDisabled: true });
});

test('reverting a route-defining input re-enables saving without touching server-disabled controls', () => {
    const planner = bootPlanner();

    planner.calculate();
    planner.routeTime.value = '16:00';
    planner.change(planner.routeTime);
    const whileStale = { saveDisabled: planner.saveButton.disabled, copyDisabled: planner.outOfBalanceCopy.disabled };

    planner.routeTime.value = '15:30';
    planner.change(planner.routeTime);

    assert.deepEqual({
        whileStale,
        saveDisabled: planner.saveButton.disabled,
        copyDisabled: planner.outOfBalanceCopy.disabled,
        bannerHidden: planner.banner.hidden,
    }, {
        whileStale: { saveDisabled: true, copyDisabled: true },
        saveDisabled: false,
        copyDisabled: true,
        bannerHidden: true,
    });
});

test('filtering the roster leaves a calculated plan current', () => {
    const planner = bootPlanner();

    planner.calculate();
    planner.context.filterSelectList(planner.document.querySelector('[data-filter-role="search"]'), 'participants-selection');

    assert.deepEqual({
        saveDisabled: planner.saveButton.disabled,
        storedSession: storedSessionId(planner),
    }, { saveDisabled: false, storedSession: 'session-1' });
});

test('clearing all selections restores the dropoff route-time copy', () => {
    const planner = bootPlanner({ mode: 'pickup' });

    assert.equal(planner.routeTimeLabel.textContent, 'Arrive at activity location by');

    planner.context.clearSelections();

    assert.deepEqual({
        label: planner.routeTimeLabel.textContent,
        ariaLabel: planner.routeTime.getAttribute('aria-label'),
        dropoffChecked: planner.dropoff.checked,
        pickupChecked: planner.pickup.checked,
    }, {
        label: 'Depart activity location at',
        ariaLabel: 'Depart activity location at',
        dropoffChecked: true,
        pickupChecked: false,
    });
});

test('clearing all ignores a calculation response that arrives afterward', () => {
    const planner = bootPlanner();

    const lateCalculation = planner.startCalculation();
    planner.context.clearSelections();
    planner.finishCalculation(lateCalculation);

    assert.equal(planner.resultsSection.querySelector('.routes-container'), null);
});

test('the calculate swap carries entered save fields across the native htmx render', () => {
    const planner = bootPlanner();

    planner.calculate();
    const entered = planner.saveFields();
    entered.eventDate.value = '2026-10-04';
    entered.eventDate.dataset.userEdited = '1';
    entered.notes.value = 'Two vans, meet at the flagpole';
    planner.driver.checked = false;
    planner.change(planner.driver);
    planner.calculate();

    const rendered = planner.saveFields();
    assert.notEqual(rendered.eventDate, entered.eventDate);
    assert.deepEqual({ eventDate: rendered.eventDate.value, notes: rendered.notes.value }, {
        eventDate: '2026-10-04',
        notes: 'Two vans, meet at the flagpole',
    });
});

test('a saved event locks re-saving and editing but leaves copying live', () => {
    const planner = bootPlanner();

    planner.calculate();
    planner.saveSucceeded();
    assert.equal(planner.previewButton.disabled, false);
    const afterSave = {
        saveDisabled: planner.saveButton.disabled,
        copyDisabled: planner.copyButton.disabled,
        banner: planner.banner.textContent,
        saveBlocked: planner.submitSave().defaultPrevented,
        storedSession: storedSessionId(planner),
    };

    planner.driver.checked = false;
    planner.change(planner.driver);

    assert.equal(planner.previewButton.disabled, false);
    assert.deepEqual({
        afterSave,
        copyDisabled: planner.copyButton.disabled,
        copyTitle: planner.copyButton.getAttribute('title'),
    }, {
        afterSave: {
            saveDisabled: true,
            copyDisabled: false,
            banner: 'Event saved. Recalculate to plan another event.',
            saveBlocked: true,
            storedSession: null,
        },
        copyDisabled: true,
        copyTitle: 'Plan changed — recalculate routes before copying or saving them.',
    });
});

test('a saved event does not seed the next event with its date and notes', () => {
    const planner = bootPlanner();

    planner.calculate();
    const entered = planner.saveFields();
    entered.eventDate.value = '2026-10-04';
    entered.eventDate.dataset.userEdited = '1';
    entered.notes.value = 'Two vans, meet at the flagpole';
    planner.saveSucceeded();
    planner.calculate();

    const rendered = planner.saveFields();
    assert.deepEqual({
        eventDate: rendered.eventDate.value,
        userEdited: rendered.eventDate.dataset.userEdited,
        notes: rendered.notes.value,
        saveDisabled: planner.saveButton.disabled,
    }, {
        eventDate: localISODate(new Date()),
        userEdited: undefined,
        notes: '',
        saveDisabled: false,
    });
});

test('a capacity-shortage recalculation adopts its own van assignments before fingerprinting', () => {
    const planner = bootPlanner();

    planner.calculate();
    planner.recalcVanSelect.value = '3';
    planner.calculate({ elt: { id: 'recalc-form' } });
    const afterRecalc = { saveDisabled: planner.saveButton.disabled, vanValue: planner.vanSelect.value };

    planner.vanSelect.value = '';
    planner.context.handleVanAssignmentChange();

    assert.deepEqual({ afterRecalc, saveDisabled: planner.saveButton.disabled }, {
        afterRecalc: { saveDisabled: false, vanValue: '3' },
        saveDisabled: true,
    });
});

test('a capacity-shortage round trip preserves an unsaved event date and notes', () => {
    const planner = bootPlanner();

    planner.calculate();
    const entered = planner.saveFields();
    entered.eventDate.value = '2026-10-04';
    entered.eventDate.dataset.userEdited = '1';
    entered.notes.value = 'Two vans, meet at the flagpole';

    const shortageRequest = planner.startCalculation();
    planner.finishCalculation(shortageRequest, { hasRoutes: false });
    const failedRetry = planner.startCalculation();
    planner.failCalculation(failedRetry);
    planner.calculate({ elt: { id: 'recalc-form' } });

    const rendered = planner.saveFields();
    assert.deepEqual({ eventDate: rendered.eventDate.value, notes: rendered.notes.value }, {
        eventDate: '2026-10-04',
        notes: 'Two vans, meet at the flagpole',
    });
});

test('clear all discards save fields buffered by a capacity shortage', () => {
    const planner = bootPlanner();

    planner.calculate();
    const entered = planner.saveFields();
    entered.eventDate.value = '2026-10-04';
    entered.eventDate.dataset.userEdited = '1';
    entered.notes.value = 'Two vans, meet at the flagpole';
    const shortageRequest = planner.startCalculation();
    planner.finishCalculation(shortageRequest, { hasRoutes: false });

    planner.context.clearSelections();
    planner.activityLocation.value = '7';
    planner.participants.forEach(participant => { participant.checked = true; });
    planner.driver.checked = true;
    planner.change(planner.driver);
    planner.calculate();

    const rendered = planner.saveFields();
    assert.deepEqual({
        eventDate: rendered.eventDate.value,
        userEdited: rendered.eventDate.dataset.userEdited,
        notes: rendered.notes.value,
    }, {
        eventDate: localISODate(new Date()),
        userEdited: undefined,
        notes: '',
    });
});

test('a capacity-shortage recalculation of a superseded plan is not saveable', () => {
    const planner = bootPlanner();

    planner.calculate();
    planner.participants[1].checked = false;
    planner.change(planner.participants[1]);
    planner.calculate({ elt: { id: 'recalc-form' } });

    assert.deepEqual({
        saveDisabled: planner.saveButton.disabled,
        banner: planner.banner.hidden ? '' : planner.banner.textContent,
        saveBlocked: planner.submitSave().defaultPrevented,
    }, {
        saveDisabled: true,
        banner: 'Plan changed — recalculate routes before copying or saving them.',
        saveBlocked: true,
    });
});

test('overlapping calculations each commit their own fingerprint', () => {
    const planner = bootPlanner();
    const first = {};
    const second = {};

    planner.startCalculation(first);
    planner.participants[1].checked = false;
    planner.change(planner.participants[1]);
    planner.startCalculation(second);

    planner.finishCalculation(second);
    const afterSecond = planner.saveButton.disabled;
    planner.finishCalculation(first);

    assert.deepEqual({ afterSecond, afterFirst: planner.saveButton.disabled }, {
        afterSecond: false,
        afterFirst: true,
    });
});

test('a results swap that belongs to no tracked calculation is not saveable', () => {
    const planner = bootPlanner();

    planner.calculate();
    planner.finishCalculation({});

    assert.deepEqual({
        saveDisabled: planner.saveButton.disabled,
        storedSession: storedSessionId(planner),
    }, { saveDisabled: true, storedSession: null });
});

test('a legacy active session is restored and migrated to the fingerprinted key', async () => {
    const planner = bootPlanner({ legacySessionId: 'legacy-session' });

    await planner.settleRestore();

    assert.deepEqual({
        restored: planner.fetches.some(request => request.url === '/api/v1/routes/session?session_id=legacy-session'),
        legacy: planner.storage.getItem('ride-home-router:active-session-id'),
        active: storedSessionId(planner),
    }, {
        restored: true,
        legacy: null,
        active: 'legacy-session',
    });
});

for (const mode of ['pickup', 'dropoff']) {
    for (const count of [4, 10]) {
        for (const action of ['copyRoute', 'copyAllRoutes']) {
            test(`${action} keeps desktop ${mode} handoffs within phone Maps limits for ${count} stops`, async () => {
                const { container, routeCard, stop } = createRouteFixture({ mode });
                const stops = Array.from({ length: count }, (_, index) => ({
                    ...stop,
                    dataset: {
                        ...stop.dataset,
                        participantLat: String(index === 2 ? 40.01 : 40 + (index + 1) / 100),
                        participantLng: '-74',
                    },
                }));
                routeCard.querySelectorAll = selector => selector === '.stop-item' ? nodeList(stops) : nodeList([]);
                let copied;
                const handoff = createRouteHandoff({ platform: { copyText: async text => { copied = text; }, notify: () => {} } });
                assert.equal(await handoff[action](action === 'copyRoute' ? routeCard : container), true);
                const urls = Array.from(copied.matchAll(/https:\/\/www.google.com\/maps\/dir\/\?[^\s]+/g), match => new URL(match[0]));
                const visited = [];
                urls.forEach((url, index) => {
                    const query = url.searchParams;
                    const waypoints = query.get('waypoints')?.split('|') || [];
                    assert.ok(waypoints.length <= 3, `unsupported ${waypoints.length} intermediate waypoints`);
                    assert.equal(query.get('dir_action'), 'navigate');
                    assert.equal(query.get('origin'), index === 0 ? null : urls[index - 1].searchParams.get('destination'));
                    visited.push(...waypoints, query.get('destination'));
                    if (urls.length > 1) assert.ok(copied.includes(`Maps leg ${index + 1} of ${urls.length}:`));
                });
                const expected = stops.map(item => `${item.dataset.participantLat},${item.dataset.participantLng}`);
                expected.push(mode === 'pickup' ? '40.4,-74.4' : '40.1,-74.1');
                assert.deepEqual(visited, expected, 'every ordered stop, including later visits, reaches the handoff');
            });
        }
    }
}

test('all htmx request failures show feedback and server toasts are not duplicated', () => {
    const app = bootPlanner();
    for (const type of ['htmx:sendError', 'htmx:timeout']) {
        app.document.body.dispatchEvent(fakeEvent(type, {elt: {id: 'other-action'}}));
    }
    for (const status of [413, 403, 503, 500]) {
        app.document.body.dispatchEvent(fakeEvent('htmx:responseError', {xhr: {status, getResponseHeader: () => null}}));
    }
    app.document.body.dispatchEvent(fakeEvent('htmx:responseError', {xhr: {status: 500, getResponseHeader: () => '{"showToast":{}}'}}));
    const container = app.document.getElementById('toast-container');
    assert.ok(container);
    assert.equal(container.getAttribute('role'), 'status');
    assert.equal(container.getAttribute('aria-live'), 'polite');
    assert.equal(container.children.length, 6);
    assert.equal(container.children[0].children[0].textContent, 'Could not reach the server. Check your connection and try again.');
    assert.equal(container.children[0].children[1].tagName, 'button');
});

test('route error extraction rejects empty, oversized and non-word responses', async () => {
    for (const response of ['', 'x'.repeat(241), '1234 !!!', '<html><body>'+ 'proxy '.repeat(100)+'</body></html>']) {
        const app = bootPlanner(); app.calculate();
        app.context.fetch = async () => ({ok:false,status:400,text:async()=>response});
        await app.context.addUnusedDriver(99);
        assert.equal(app.document.getElementById('toast-container').children[0].children[0].textContent, 'An error occurred. Please try again.');
    }
});

test('route edits use the server conflict explanation from HX-Trigger', async () => {
 const app=bootPlanner();app.calculate();
 app.context.fetch=async()=>({ok:false,status:409,headers:{get:()=>JSON.stringify({showToast:{message:'This route plan changed. Reload it and try again.'}})},text:async()=>''});
 assert.equal(await app.context.addUnusedDriver(99),false);
 assert.equal(app.document.getElementById('toast-container').children[0].children[0].textContent,'The route update could not be confirmed. Reload the page before saving or copying routes.');
});
test('cancelling reset leaves routes and pending edits untouched', async () => {
 const app=bootPlanner();app.calculate();app.context.showConfirmDialog=async()=>false;
 await app.context.resetRoutes();
 assert.equal(app.fetches.length,0);
 assert.equal(app.saveButton.disabled,false);
});

test('a failed save restores the current planner lock after htmx enables its button', () => {
 const app=bootPlanner();app.calculate();app.saveButton.disabled=true;
 app.routeTime.value='12:00';app.change(app.routeTime);
 app.saveButton.disabled=false;
 app.document.body.dispatchEvent(fakeEvent('htmx:afterRequest',{elt:app.saveForm,successful:false}));
 assert.equal(app.saveButton.disabled,true);
 assert.equal(app.submitSave().defaultPrevented,true);
});

test('queued moves from an old session cannot absorb moves for the installed session', async () => {
    const harness = createRouteSessionHarness();
    const owner = harness.orchestrator;
    owner.move(1, 0, 1);
    harness.setActiveSessionId('session-b');
    owner.move(2, 0, 1);
    await owner.openEditor('/editor');
    assert.deepEqual(harness.sent.map(({ options }) => JSON.parse(options.body)), [
        { session_id: 'session-b', participant_id: 2, from_route_index: 0, to_route_index: 1, insert_at_position: -1 },
    ]);
    assert.deepEqual(harness.opened, [['GET', '/editor']]);
});

for (const change of ['rejected move', 'installed session', 'Plan eligibility']) {
    test(`opening an editor after flushing rechecks ${change}`, async () => {
        let finish;
        const harness = createRouteSessionHarness({ request: () => new Promise(resolve => { finish = resolve; }) });
        const owner = harness.orchestrator;
        owner.move(1, 0, 1);
        const opening = owner.openEditor('/editor');
        if (change === 'installed session') harness.setActiveSessionId('session-b');
        if (change === 'Plan eligibility') harness.setSaveable(false);
        finish({ ok: change !== 'rejected move', text: async () => 'routes' });
        await opening;
        assert.deepEqual(harness.opened, []);
    });
}

test('an edit without an installed session reports its recovery action', () => {
    const harness = createRouteSessionHarness({ activeSessionId: null });
    harness.orchestrator.move(1, 0, 1);
    assert.deepEqual(harness.notifications, [['That route plan is no longer available. Calculate it again.', 'error']]);
    assert.equal(harness.orchestrator.hasPending(), false);
});

for (const failure of ['network', 'body', 'server']) {
    test(`ambiguous ${failure} edit blocks save and copy until reload`, async () => {
        const app = bootPlanner();
        app.calculate();
        app.context.fetch = async () => {
            if (failure === 'network') throw new Error('lost response after commit');
            return { ok: failure !== 'server', status: failure === 'server' ? 500 : 200,
                text: async () => { if (failure === 'body') throw new Error('truncated body'); return 'render failed'; } };
        };
        assert.equal(await app.context.addUnusedDriver(99), false);
        await app.settleRestore();
        assert.equal(app.copyButton.disabled, true);
        assert.equal(app.saveButton.disabled, true);
        assert.match(app.banner.textContent, /Reload the page/);
        assert.match(app.saveButton.getAttribute('title'), /Reload the page/);
    });
}

test('uncertain edits discard queued actions, block submit, and survive plan changes', async () => {
    let reject;
    const form = createSaveForm();
    const harness = createRouteSessionHarness({ getLiveForm: () => form, request: () => new Promise((_, fail) => { reject = fail; }) });
    const owner = harness.orchestrator;
    const first = owner.add(1);
    const queued = owner.add(2);
    assert.equal(owner.save(form), true);
    reject(new Error('response lost'));
    assert.equal(await first, false);
    assert.equal(await queued, false);
    await settleEdits();
    owner.planChanged({ status: 'stale', sessionId: 'session-a' });
    owner.planChanged({ status: 'current', sessionId: 'session-a' });
    assert.equal(owner.hasPending(), true);
    assert.equal(owner.save(form), true);
    await settleEdits();
    assert.equal(form.submitCount, 0);
    assert.equal(harness.sent.length, 1);
    assert.equal(await owner.add(3), false);
    harness.setActiveSessionId('session-b');
    assert.equal(owner.hasPending(), false);
});

test('failed timing reads and replaced-session failures do not lock the installed plan', async () => {
    const timing = createRouteSessionHarness({ request: async () => { throw new Error('timings unavailable'); } });
    assert.equal(await timing.orchestrator.timings(0), false);
    assert.equal(timing.orchestrator.hasPending(), false);
    let reject;
    const old = createRouteSessionHarness({ request: () => new Promise((_, fail) => { reject = fail; }) });
    const pending = old.orchestrator.add(1);
    old.setActiveSessionId('session-b');
    reject(new Error('old session response lost'));
    assert.equal(await pending, false);
    await settleEdits();
    assert.equal(old.orchestrator.hasPending(), false);
});

test('summarizeMeasuredCards adds up occupied cars only once every one of them has timings', () => {
    const { summarizeMeasuredCards } = planner;
    const measured = (totalMeters, detourSecs) => ({ timings: 'measured', hasStops: true, totalMeters: String(totalMeters), detourSecs: String(detourSecs) });
    const empty = { timings: 'empty', hasStops: false, totalMeters: '', detourSecs: '' };
    const stale = { timings: 'stale', hasStops: true, totalMeters: '', detourSecs: '' };

    assert.deepEqual(summarizeMeasuredCards([measured(8000, 300), measured(2000, 90), empty], true), {
        totalDistance: '6.21 mi', maxDetour: '5m', averageDetour: '3m 15s',
    });
    assert.deepEqual(summarizeMeasuredCards([measured(1500, 45)], false), {
        totalDistance: '1.50 km', maxDetour: '45s', averageDetour: '45s',
    });
    assert.deepEqual(summarizeMeasuredCards([measured(8125, 0)], false).totalDistance, '8.12 km', 'exact ties round to even like Go');
    assert.deepEqual(summarizeMeasuredCards([measured(8375, 0)], false).totalDistance, '8.38 km', 'odd eighths round up to even');
    assert.deepEqual(summarizeMeasuredCards([measured(2635, 0)], false).totalDistance, '2.63 km', '2.635 is below the tie in binary, as in Go');
    assert.deepEqual(summarizeMeasuredCards([measured(2645, 0)], false).totalDistance, '2.65 km');
    assert.deepEqual(summarizeMeasuredCards([measured('1000.400', '59.600')], false), {
        totalDistance: '1.00 km', maxDetour: '59s', averageDetour: '59s',
    }, 'fractional seconds truncate like the Go helper');
    assert.equal(summarizeMeasuredCards([measured(8000, 300), stale], true), null, 'one unmeasured car means no total');
    assert.equal(summarizeMeasuredCards([empty], true), null, 'nothing to add up');
    assert.equal(summarizeMeasuredCards([{ timings: 'measured', hasStops: true, totalMeters: 'x', detourSecs: '1' }], true), null, 'bad numbers never produce a total');
});

test('manual route edits reach the server in order and keep handoffs locked until settled', async () => {
    const app = bootPlanner();
    app.calculate();
    app.routesContainer.appendChild(domNode('select', { id: 'swap-select-0', value: '1' }));
    const requests = [];
    app.context.fetch = (url, options) => new Promise(resolve => requests.push({ url, options, resolve }));

    const swapping = app.context.swapDrivers(0);
    const resetting = app.context.resetRoutes();
    await app.settleRestore();
    assert.deepEqual(requests.map(request => request.url), ['/api/v1/routes/edit/swap-drivers']);
    assert.equal(app.copyButton.disabled, true);
    assert.equal(app.saveButton.disabled, true);
    assert.equal(await app.context.copyAllRoutes(), false);
    assert.equal(app.previewButton.disabled, true);
    assert.match(app.previewButton.getAttribute('title'), /Updating routes/);
    assert.equal(app.context.previewRoute(app.previewButton), false);

    requests[0].resolve({ ok: true, text: async () => '<div>swapped routes</div>' });
    await swapping;
    await app.settleRestore();
    assert.equal(requests[1].url, '/api/v1/routes/edit/reset?session_id=session-1');
    assert.equal(app.copyButton.disabled, true);
    requests[1].resolve({ ok: true, text: async () => '<div>reset routes</div>' });
    await resetting;
    await app.settleRestore();
    assert.equal(app.copyButton.disabled, false);
    assert.equal(app.saveButton.disabled, false);
    assert.equal(app.previewButton.disabled, false);
});

test('Plan changes cancel queued moves and Clear resolves queued manual actions', async () => {
    let finish;
    const harness = createRouteSessionHarness({ request: () => new Promise(resolve => { finish = resolve; }) });
    const owner = harness.orchestrator;
    owner.move(1, 0, 1);
    owner.planChanged({ status: 'stale', sessionId: 'session-a' });
    assert.equal(owner.hasPending(), false);
    assert.equal(harness.sent.length, 0);
    const active = owner.add(10);
    const queued = owner.swap(0, 1);
    owner.clear();
    assert.equal(await queued, false);
    assert.equal(owner.hasPending(), true);
    finish({ ok: true, text: async () => 'active' });
    await active; await settleEdits();
    assert.equal(owner.hasPending(), false);
});
