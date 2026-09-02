'use strict';

const test = require('node:test');
const assert = require('node:assert/strict');

const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');

const planner = require('./event-planner.js');
const {
    createParticipantMoveBatcher,
    createPlannerState,
    createRouteHandoff,
    applyLocalEventDate,
    createRouteSessionOrchestrator,
    installRouteResults,
    localISODate,
    sanitizeVanAssignments,
} = planner;

test('applyLocalEventDate overwrites the server date on injected forms but keeps user edits', () => {
    const makeInput = (value, userEdited) => ({
        value,
        dataset: userEdited ? { userEdited: '1' } : {},
        listeners: [],
        addEventListener(type, fn) { this.listeners.push([type, fn]); },
    });
    const serverDated = makeInput('2026-03-15', false);
    const edited = makeInput('2026-03-20', true);
    const scope = { querySelectorAll: () => [serverDated, edited] };

    assert.equal(applyLocalEventDate(scope, new Date(2026, 2, 14, 23, 30)), 2);
    assert.equal(serverDated.value, '2026-03-14');
    assert.equal(edited.value, '2026-03-20');
    assert.equal(serverDated.listeners[0][0], 'input');
});

test('localISODate uses the local calendar day, not the UTC one', () => {
    // toISOString reports the next date in zones west of UTC at this time.
    const lateEvening = new Date(2026, 2, 14, 23, 30);
    assert.equal(localISODate(lateEvening), '2026-03-14');
    assert.equal(localISODate(new Date(2026, 0, 5, 0, 10)), '2026-01-05');
});

test('installRouteResults installs HTML before processing and performs all result setup', () => {
    let installedHtml = '';
    let processedTarget = null;
    let etaRefreshes = 0;
    const dateInput = {
        value: 'server-date',
        dataset: {},
        listeners: [],
        addEventListener(type, fn) { this.listeners.push([type, fn]); },
    };
    const target = {
        set innerHTML(html) { installedHtml = html; },
        querySelector: () => null,
        querySelectorAll() {
            assert.notEqual(installedHtml, '');
            return [dateInput];
        },
    };

    installRouteResults({
        target,
        html: '<form>routes</form>',
        htmx: {
            process(element) {
                assert.notEqual(installedHtml, '');
                processedTarget = element;
            },
        },
        afterRender: () => {
            assert.notEqual(installedHtml, '');
            etaRefreshes += 1;
        },
    });

    assert.equal(installedHtml, '<form>routes</form>');
    assert.equal(processedTarget, target);
    assert.notEqual(dateInput.value, 'server-date');
    assert.equal(dateInput.listeners[0][0], 'input');
    assert.equal(etaRefreshes, 1);
});

test('planner exports its browser-independent test seams', () => {
    assert.deepEqual(Object.keys(planner).sort(), [
        'applyLocalEventDate',
        'createParticipantMoveBatcher',
        'createPlannerState',
        'createRouteHandoff',
        'createRouteSessionOrchestrator',
        'installRouteResults',
        'localISODate',
        'sanitizeVanAssignments',
    ]);
});

test('planner draft keeps the first selected driver for each van', () => {
    assert.deepEqual(
        sanitizeVanAssignments(
            ['10', '20', '30'],
            { 10: '7', 20: '7', 30: '8', 99: '9' },
        ),
        { 10: '7', 30: '8' },
    );
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

test('driver copy omits the ETA prefix when the route time is invalid', async () => {
    const copied = [];
    const { container, routeCard } = createRouteFixture();
    container.dataset.routeTime = 'not-a-time';
    const handoff = createRouteHandoff({
        platform: {
            copyText: async text => copied.push(text),
            openUrl: async () => {},
            notify: () => {},
        },
    });

    assert.equal(await handoff.copyRoute(routeCard), true);
    assert.deepEqual(copied, [
        'Activity Location: Wednesday Night Church\n1 Church Road\n\n' +
        'Driver: Jordan Driver\n9 Driver Lane\n' +
        '1. Sam Rider - 5 Rider Street\n\n' +
        'Maps: https://www.google.com/maps/dir/?api=1&travelmode=driving&destination=40.1%2C-74.1&dir_action=navigate&waypoints=40.2%2C-74.2\n',
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

test('copy all routes does nothing when there are no route cards', async () => {
    const copied = [];
    const { container } = createRouteFixture();
    container.querySelectorAll = () => nodeList([]);
    const handoff = createRouteHandoff({
        platform: {
            copyText: async text => copied.push(text),
            openUrl: async () => {},
            notify: () => {},
        },
    });

    assert.equal(await handoff.copyAllRoutes(container), false);
    assert.deepEqual(copied, []);
});

test('copy all routes preserves an empty Maps line when a route has no stops', async () => {
    const copied = [];
    const { container, routeCard } = createRouteFixture();
    routeCard.querySelectorAll = () => nodeList([]);
    const handoff = createRouteHandoff({
        platform: {
            copyText: async text => copied.push(text),
            openUrl: async () => {},
            notify: () => {},
        },
    });

    assert.equal(await handoff.copyAllRoutes(container), true);
    assert.deepEqual(copied, [
        'Activity Location: Wednesday Night Church\n1 Church Road\n\n' +
        'Driver: Jordan Driver\n9 Driver Lane\n\nMaps: \n',
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
    assert.deepEqual(notifications, [['Failed to copy to clipboard', 'error']]);
});

test('copy-all clipboard failure returns false and reports an error', async () => {
    const notifications = [];
    const { container } = createRouteFixture();
    const handoff = createRouteHandoff({
        platform: {
            copyText: async () => { throw new Error('clipboard unavailable'); },
            openUrl: async () => {},
            notify: (message, type) => notifications.push([message, type]),
        },
    });

    assert.equal(await handoff.copyAllRoutes(container), false);
    assert.deepEqual(notifications, [['Failed to copy to clipboard', 'error']]);
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
        'Could not build a valid Google Maps route for this trip.',
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
    assert.deepEqual(notifications, [['Failed to open browser', 'error']]);
});

test('preview ignores whatever openUrl resolves with', async () => {
    const notifications = [];
    const { routeCard } = createRouteFixture();
    const handoff = createRouteHandoff({
        platform: {
            copyText: async () => {},
            openUrl: async () => ({ ok: false }),
            notify: (message, type) => notifications.push([message, type]),
        },
    });

    assert.equal(await handoff.previewRoute(routeCard), true);
    assert.deepEqual(notifications, []);
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

test('ETA population clears stale values when the route time is invalid', () => {
    const { container, stopEta } = createRouteFixture();
    container.dataset.routeTime = 'not-a-time';
    stopEta.textContent = 'stale';
    const handoff = createRouteHandoff({
        platform: {
            copyText: async () => {},
            openUrl: async () => {},
            notify: () => {},
        },
    });

    handoff.populateEtas(container);

    assert.equal(stopEta.textContent, '');
});

test('route handoff ignores missing route and container elements', async () => {
    const { routeCard } = createRouteFixture();
    routeCard.closest = () => null;
    const handoff = createRouteHandoff({
        platform: {
            copyText: async () => { throw new Error('should not copy'); },
            openUrl: async () => { throw new Error('should not open'); },
            notify: () => { throw new Error('should not notify'); },
        },
    });

    assert.equal(await handoff.copyRoute(null), false);
    assert.equal(await handoff.copyRoute(routeCard), false);
    assert.equal(await handoff.copyAllRoutes(null), false);
    assert.equal(await handoff.previewRoute(null), false);
    assert.equal(await handoff.previewRoute(routeCard), false);
    assert.doesNotThrow(() => handoff.populateEtas(null));
});

function createSaveForm({
    eventDate = '',
    notes = '',
    sessionId = 'session-a',
    saveEnabled = true,
    submitButton = true,
} = {}) {
    const fields = {
        event_date: { value: eventDate },
        notes: { value: notes },
        session_id: { value: sessionId },
    };
    const form = {
        submitCount: 0,
        elements: { namedItem: name => fields[name] || null },
        getAttribute: name => name === 'hx-post' && saveEnabled ? '/api/v1/events' : null,
        querySelector: selector => selector === 'button[type="submit"]' && submitButton
            ? { disabled: !saveEnabled }
            : null,
        requestSubmit() { this.submitCount += 1; },
        value(name) { return fields[name]?.value; },
    };
    return form;
}

function createRouteSessionHarness({
    activeSessionId = 'session-a',
    getLiveForm = () => null,
    hasPending = () => true,
    flush = async () => true,
    hasResultsSection = true,
} = {}) {
    const rendered = [];
    const processed = [];
    const errors = [];
    let etaRefreshes = 0;
    let currentSessionId = activeSessionId;
    const dateInput = {
        value: 'server-date',
        dataset: {},
        addEventListener() {},
    };
    let dateApplications = 0;
    const resultsSection = {
        set innerHTML(html) { rendered.push(html); },
        querySelector: () => null,
        querySelectorAll() {
            dateApplications += 1;
            return [dateInput];
        },
    };
    const document = {
        querySelector(selector) {
            if (selector === '.routes-container') {
                return currentSessionId ? { dataset: { sessionId: currentSessionId } } : null;
            }
            if (selector === '#results-section form input[name="session_id"]') {
                const form = getLiveForm();
                return form ? { closest: () => form } : null;
            }
            return null;
        },
        getElementById: id => id === 'results-section' && hasResultsSection ? resultsSection : null,
    };
    const orchestrator = createRouteSessionOrchestrator({
        document,
        htmx: { process: element => processed.push(element) },
        moves: { hasPending, flush },
        reportError: html => errors.push(html),
        afterRender: () => { etaRefreshes += 1; },
    });

    return {
        errors,
        get dateApplications() { return dateApplications; },
        dateInput,
        get etaRefreshes() { return etaRefreshes; },
        orchestrator,
        processed,
        rendered,
        resultsSection,
        setActiveSessionId: value => { currentSessionId = value; },
    };
}

test('route edit responses install results only while their requested session is active', () => {
    const harness = createRouteSessionHarness();

    const currentResult = harness.orchestrator.applyEditResult({ requestedSessionId: 'session-a', ok: true, html: 'current routes' });
    harness.setActiveSessionId('session-b');
    const staleResult = harness.orchestrator.applyEditResult({ requestedSessionId: 'session-a', ok: true, html: 'stale routes' });

    assert.deepEqual({
        currentResult,
        staleResult,
        rendered: harness.rendered,
        processed: harness.processed,
        dateApplications: harness.dateApplications,
        etaRefreshes: harness.etaRefreshes,
    }, {
        currentResult: true,
        staleResult: true,
        rendered: ['current routes'],
        processed: [harness.resultsSection],
        dateApplications: 1,
        etaRefreshes: 1,
    });
    assert.notEqual(harness.dateInput.value, 'server-date');
});

test('route edit success remains handled when the results target is absent', () => {
    const harness = createRouteSessionHarness({ hasResultsSection: false });

    const handled = harness.orchestrator.applyEditResult({ requestedSessionId: 'session-a', ok: true, html: 'routes' });

    assert.deepEqual({
        handled,
        rendered: harness.rendered,
        processed: harness.processed,
        dateApplications: harness.dateApplications,
        etaRefreshes: harness.etaRefreshes,
    }, {
        handled: true,
        rendered: [],
        processed: [],
        dateApplications: 0,
        etaRefreshes: 0,
    });
});

test('route edit errors are reported even after the requested session becomes stale', () => {
    const harness = createRouteSessionHarness({ activeSessionId: 'session-b' });

    const succeeded = harness.orchestrator.applyEditResult({ requestedSessionId: 'session-a', ok: false, html: 'move failed' });

    assert.deepEqual({ succeeded, errors: harness.errors }, {
        succeeded: false,
        errors: ['move failed'],
    });
});

test('saving with queued moves submits the replacement form the flush rendered', async () => {
    const originalForm = createSaveForm({ eventDate: '2026-08-23', notes: 'Bring snacks' });
    let liveForm = originalForm;
    const harness = createRouteSessionHarness({
        getLiveForm: () => liveForm,
        flush: async () => {
            // installRouteResults carries the typed fields across each render.
            liveForm = createSaveForm({ eventDate: '2026-08-23', notes: 'Bring snacks', sessionId: 'session-a' });
            return true;
        },
    });

    const handled = await harness.orchestrator.submitSaveWithQueuedMoves(originalForm);

    assert.deepEqual({
        handled,
        eventDate: liveForm.value('event_date'),
        notes: liveForm.value('notes'),
        sessionId: liveForm.value('session_id'),
        originalSubmits: originalForm.submitCount,
        liveSubmits: liveForm.submitCount,
    }, {
        handled: true,
        eventDate: '2026-08-23',
        notes: 'Bring snacks',
        sessionId: 'session-a',
        originalSubmits: 0,
        liveSubmits: 1,
    });
});

test('saving after a move does not submit an unsaveable replacement form', async () => {
    const originalForm = createSaveForm({ eventDate: '2026-08-23', notes: 'Bring snacks' });
    const liveForm = createSaveForm({ eventDate: '2026-08-23', notes: 'Bring snacks', saveEnabled: false });
    const harness = createRouteSessionHarness({
        getLiveForm: () => liveForm,
    });

    await harness.orchestrator.submitSaveWithQueuedMoves(originalForm);

    assert.deepEqual({ eventDate: liveForm.value('event_date'), notes: liveForm.value('notes'), submits: liveForm.submitCount }, {
        eventDate: '2026-08-23',
        notes: 'Bring snacks',
        submits: 0,
    });
});

test('saving after a successful replacement tolerates the live form being absent', async () => {
    const originalForm = createSaveForm({ eventDate: '2026-08-23' });
    const harness = createRouteSessionHarness({ getLiveForm: () => null });

    const handled = await harness.orchestrator.submitSaveWithQueuedMoves(originalForm);

    assert.equal(handled, true);
});

test('saving after a partial move flush does not submit', async () => {
    const originalForm = createSaveForm({ eventDate: '2026-08-23', notes: 'Bring snacks' });
    const liveForm = createSaveForm({ eventDate: '2026-08-23', notes: 'Bring snacks' });
    const harness = createRouteSessionHarness({
        getLiveForm: () => liveForm,
        flush: async () => false,
    });

    await harness.orchestrator.submitSaveWithQueuedMoves(originalForm);

    assert.deepEqual({ eventDate: liveForm.value('event_date'), notes: liveForm.value('notes'), submits: liveForm.submitCount }, {
        eventDate: '2026-08-23',
        notes: 'Bring snacks',
        submits: 0,
    });
});

test('a second save attempt during the same move flush does not submit twice', async () => {
    let finishFlush;
    const flush = new Promise(resolve => { finishFlush = resolve; });
    const form = createSaveForm({ eventDate: '2026-08-23' });
    const harness = createRouteSessionHarness({
        getLiveForm: () => form,
        flush: () => flush,
    });

    const firstSave = harness.orchestrator.submitSaveWithQueuedMoves(form);
    const secondSave = harness.orchestrator.submitSaveWithQueuedMoves(form);
    finishFlush(true);
    await Promise.all([firstSave, secondSave]);

    assert.equal(form.submitCount, 1);
});

test('a queued save waits for its own session when an earlier session fails', async () => {
    let finishSessionA;
    const sessionAGate = new Promise(resolve => { finishSessionA = resolve; });
    const sent = [];
    const batcher = createParticipantMoveBatcher({
        schedule: () => 1,
        cancel: () => {},
        sendBatch: async payload => {
            sent.push(payload.session_id);
            if (payload.session_id === 'session-a') {
                await sessionAGate;
                return false;
            }
            return true;
        },
    });
    const sessionAForm = createSaveForm({ eventDate: '2026-08-23', notes: 'Session A', sessionId: 'session-a' });
    const sessionBForm = createSaveForm({ eventDate: '2026-08-24', notes: 'Session B', sessionId: 'session-b' });
    let liveForm = sessionAForm;
    const harness = createRouteSessionHarness({
        getLiveForm: () => liveForm,
        hasPending: sessionId => batcher.hasPendingFor(sessionId),
        flush: sessionId => batcher.flushFor(sessionId),
    });

    batcher.enqueue({ session_id: 'session-a', participant_id: 1 });
    const sessionASave = harness.orchestrator.submitSaveWithQueuedMoves(sessionAForm);
    harness.setActiveSessionId('session-b');
    liveForm = sessionBForm;
    batcher.enqueue({ session_id: 'session-b', participant_id: 2 });
    const sessionBSave = harness.orchestrator.submitSaveWithQueuedMoves(sessionBForm);
    finishSessionA();
    await Promise.all([sessionASave, sessionBSave]);

    assert.deepEqual({
        sent,
        eventDate: sessionBForm.value('event_date'),
        notes: sessionBForm.value('notes'),
        sessionASubmits: sessionAForm.submitCount,
        sessionBSubmits: sessionBForm.submitCount,
    }, {
        sent: ['session-a', 'session-b'],
        eventDate: '2026-08-24',
        notes: 'Session B',
        sessionASubmits: 0,
        sessionBSubmits: 1,
    });
});



test('participant moves flush sequentially in same-session batches with the existing payload contracts', async () => {
    const sent = [];
    const batcher = createParticipantMoveBatcher({
        schedule: () => 1,
        cancel: () => {},
        sendBatch: async payload => {
            sent.push(payload);
            await Promise.resolve();
            return true;
        },
    });
    batcher.enqueue({ session_id: 'session-a', participant_id: 1, from_route_index: 0, to_route_index: 1, insert_at_position: -1 });
    batcher.enqueue({ session_id: 'session-a', participant_id: 2, from_route_index: 1, to_route_index: 0, insert_at_position: -1 });
    batcher.enqueue({ session_id: 'session-b', participant_id: 3, from_route_index: 0, to_route_index: 2, insert_at_position: -1 });

    assert.equal(batcher.hasPendingFor('session-a'), true);
    assert.equal(batcher.hasPendingFor('session-b'), true);
    assert.equal(batcher.hasPendingFor('session-c'), false);
    assert.equal(await batcher.flush(), true);
    assert.equal(batcher.hasPendingFor('session-a'), false);
    assert.equal(batcher.hasPendingFor('session-b'), false);
    assert.deepEqual(sent, [
        {
            session_id: 'session-a',
            moves: [
                { participant_id: 1, from_route_index: 0, to_route_index: 1, insert_at_position: -1 },
                { participant_id: 2, from_route_index: 1, to_route_index: 0, insert_at_position: -1 },
            ],
        },
        { session_id: 'session-b', participant_id: 3, from_route_index: 0, to_route_index: 2, insert_at_position: -1 },
    ]);
});

test('flushing a session preserves an earlier failure across later successful batches', async () => {
    let calls = 0;
    const batcher = createParticipantMoveBatcher({
        batchLimit: 1,
        schedule: () => 1,
        cancel: () => {},
        sendBatch: async () => {
            calls += 1;
            return calls !== 1;
        },
    });
    batcher.enqueue({ session_id: 'session-a', participant_id: 1 });
    batcher.enqueue({ session_id: 'session-a', participant_id: 2 });
    batcher.enqueue({ session_id: 'session-a', participant_id: 3 });

    assert.equal(await batcher.flushFor('session-a'), false);
    assert.equal(calls, 3);
    assert.equal(batcher.hasPendingFor('session-a'), false);
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


// --- Minimal DOM good enough to boot the planner --------------------------

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

// Models the server render: the old nodes go away and fresh ones take their place.
function replaceOnRender(target, buildFields) {
    Object.defineProperty(target, 'innerHTML', {
        set() {
            this.children.forEach(child => { child.parentNode = null; });
            this.children = [];
            buildFields().forEach(field => this.appendChild(field));
        },
    });
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

// Boots the real planner against the fixture below so the invalidation rule is
// exercised end to end, not through a re-implementation of it.
function bootPlanner({ mode = 'dropoff', storedSession = null } = {}) {
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
    const outOfBalanceCopy = domNode('button', { dataset: { sessionAction: 'copy' }, disabled: true });
    const saveForm = domNode('form', {
        form: true,
        classes: ['save-event-card'],
        attributes: { 'hx-post': '/api/v1/events' },
        children: [domNode('input', { name: 'session_id', value: 'session-1' })],
    });
    // The server re-renders the save fields from its own defaults every time.
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
        children: [banner, copyButton, outOfBalanceCopy, saveForm],
    });
    const routesContainer = domNode('div', {
        classes: ['routes-container'],
        dataset: { sessionId: 'session-1', routeMode: mode },
        children: [resultsBody],
    });
    let renderedSessionCount = 0;
    const resultsSection = domNode('div', { id: 'results-section', children: [routesContainer] });
    // Manual edits re-render the same session's markup; an empty render clears it.
    Object.defineProperty(resultsSection, 'innerHTML', {
        set(html) {
            this.children.forEach(child => { child.parentNode = null; });
            this.children = [];
            if (html) this.appendChild(routesContainer);
        },
    });

    // The capacity-shortage pane's own recalculation form.
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
    const root = domNode('html', { children: [body] });

    const document = {
        readyState: 'complete',
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
    if (storedSession) {
        storage.setItem('ride-home-router:active-session:v2', JSON.stringify(storedSession));
    }
    const context = {
        document,
        console,
        HTMLFormElement,
        Event: class { constructor(type) { return fakeEvent(type); } },
        AbortController: class { constructor() { this.signal = {}; } abort() { this.aborted = true; } },
        htmx: { process() {} },
        fetch: async () => ({ ok: true, status: 200, text: async () => '<div>routes</div>' }),
        setTimeout: () => 0,
        clearTimeout: () => {},
        JSON,
        Intl,
        window: {
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
        // The restore fetch resolves over a few microtask turns.
        async settleRestore() {
            for (let turn = 0; turn < 5; turn += 1) await Promise.resolve();
        },
        saveSucceeded() {
            const saveResult = domNode('div', {
                id: 'save-result',
                children: [domNode('div', { classes: ['alert-success'] })],
            });
            body.appendChild(saveResult);
            body.dispatchEvent(Object.assign(fakeEvent('htmx:afterSwap'), { detail: { target: saveResult } }));
        },
        // The full htmx lifecycle: request, snapshot, server render, swap, settle.
        // htmx reuses one detail object per request, so the same xhr identifies
        // the calculation from beforeRequest through afterSwap.
        startCalculation(xhr = {}, elt = { id: 'calculate-btn' }) {
            body.dispatchEvent(Object.assign(fakeEvent('htmx:beforeRequest'), { detail: { elt, xhr } }));
            return xhr;
        },
        failCalculation(xhr, elt = { id: 'calculate-btn' }) {
            body.dispatchEvent(Object.assign(fakeEvent('htmx:responseError'), { detail: { elt, xhr } }));
        },
        finishCalculation(xhr, { hasRoutes = true } = {}) {
            body.dispatchEvent(Object.assign(fakeEvent('htmx:beforeSwap'), { detail: { target: resultsSection, xhr } }));
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
        // Disabled by the server for being over capacity; staleness must not clear that.
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

test('a route edit render carries the entered event date and notes onto the replacement form', () => {
    const savedFields = () => [
        domNode('input', { type: 'date', name: 'event_date', value: '2026-09-01' }),
        domNode('textarea', { name: 'notes', value: '' }),
    ];
    const target = domNode('div', { id: 'results-section', children: savedFields() });
    const eventDate = target.querySelector('input[name="event_date"]');
    const notes = target.querySelector('textarea[name="notes"]');
    eventDate.value = '2026-10-04';
    eventDate.dataset.userEdited = '1';
    notes.value = 'Two vans, meet at the flagpole';

    replaceOnRender(target, savedFields);

    installRouteResults({
        target,
        html: '<form>routes</form>',
        htmx: { process() {} },
        afterRender() {},
    });

    assert.deepEqual({
        eventDate: target.querySelector('input[name="event_date"]').value,
        userEdited: target.querySelector('input[name="event_date"]').dataset.userEdited,
        notes: target.querySelector('textarea[name="notes"]').value,
    }, {
        eventDate: '2026-10-04',
        userEdited: '1',
        notes: 'Two vans, meet at the flagpole',
    });
});

test('a render without entered fields keeps the server defaults', () => {
    const target = domNode('div', {
        children: [
            domNode('input', { type: 'date', name: 'event_date', value: '2026-09-01' }),
            domNode('textarea', { name: 'notes', value: '' }),
        ],
    });
    replaceOnRender(target, () => [
        domNode('input', { type: 'date', name: 'event_date', value: '2026-09-02' }),
        domNode('textarea', { name: 'notes', value: '' }),
    ]);

    installRouteResults({ target, html: '', htmx: { process() {} }, afterRender() {} });

    assert.deepEqual({
        userEdited: target.querySelector('input[name="event_date"]').dataset.userEdited,
        notes: target.querySelector('textarea[name="notes"]').value,
    }, { userEdited: undefined, notes: '' });
    assert.equal(target.querySelector('input[name="event_date"]').value, localISODate(new Date()));
});

test('planner state stays stale until a recalculation and never leaves saved for current', () => {
    const changes = [];
    let fingerprint = 'plan-a';
    const state = createPlannerState({
        readFingerprint: () => fingerprint,
        onChange: snapshot => changes.push(`${snapshot.status}:${snapshot.sessionId}`),
    });

    const beforeCalculation = state.refresh();
    state.markCalculated('session-1');
    fingerprint = 'plan-b';
    const afterEdit = state.refresh();
    // A calculation requested before the edit still lands stale.
    state.markCalculated('session-2', 'plan-a');
    const afterLateSwap = state.getSnapshot();
    fingerprint = 'plan-b';
    state.markCalculated('session-3');
    state.markSaved();
    fingerprint = 'plan-c';
    const afterSavedEdit = state.refresh();
    // Reverting the edit must not hand back a second save of the same session.
    fingerprint = 'plan-b';
    const afterSavedRevert = state.refresh();

    assert.deepEqual({
        beforeCalculation: beforeCalculation.status,
        afterEdit: { status: afterEdit.status, canSave: afterEdit.canSave },
        afterLateSwap: afterLateSwap.status,
        afterSavedEdit: { status: afterSavedEdit.status, canSave: afterSavedEdit.canSave },
        afterSavedRevert: { status: afterSavedRevert.status, canSave: afterSavedRevert.canSave },
        changes,
    }, {
        beforeCalculation: 'empty',
        afterEdit: { status: 'stale', canSave: false },
        afterLateSwap: 'stale',
        afterSavedEdit: { status: 'stale', canSave: false },
        afterSavedRevert: { status: 'saved', canSave: false },
        changes: [
            'current:session-1',
            'stale:session-1',
            'stale:session-2',
            'current:session-3',
            'saved:session-3',
            'stale:session-3',
            'saved:session-3',
        ],
    });
});

test('clearing planner state drops the session and stops tracking the fingerprint', () => {
    const changes = [];
    let fingerprint = 'plan-a';
    const state = createPlannerState({
        readFingerprint: () => fingerprint,
        onChange: snapshot => changes.push(snapshot.status),
    });

    state.markCalculated('session-1');
    state.clear();
    fingerprint = 'plan-b';

    assert.deepEqual({ snapshot: state.refresh(), changes }, {
        snapshot: { status: 'empty', sessionId: null, canSave: false, hasBeenSaved: false },
        changes: ['current', 'empty'],
    });
});

test('a results swap with no session clears the planner state', () => {
    const changes = [];
    const state = createPlannerState({
        readFingerprint: () => 'plan-a',
        onChange: snapshot => changes.push(snapshot.status),
    });

    state.markCalculated('session-1');
    const cleared = state.markCalculated(null);

    assert.deepEqual({ cleared, changes }, {
        cleared: { status: 'empty', sessionId: null, canSave: false, hasBeenSaved: false },
        changes: ['current', 'empty'],
    });
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
    const afterSave = {
        saveDisabled: planner.saveButton.disabled,
        copyDisabled: planner.copyButton.disabled,
        banner: planner.banner.textContent,
        saveBlocked: planner.submitSave().defaultPrevented,
        storedSession: storedSessionId(planner),
    };

    // Editing the plan after saving is stale, not saved: copying is wrong too.
    planner.driver.checked = false;
    planner.change(planner.driver);

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

test('a saved event does not seed the next event after its planner inputs change', () => {
    const planner = bootPlanner();

    planner.calculate();
    const entered = planner.saveFields();
    entered.eventDate.value = '2026-10-04';
    entered.eventDate.dataset.userEdited = '1';
    entered.notes.value = 'Two vans, meet at the flagpole';
    planner.saveSucceeded();
    planner.participants[1].checked = false;
    planner.change(planner.participants[1]);
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

    // The plan form now describes the routes, so editing it still goes stale.
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
    // The recalc form still carries both participants; the plan form no longer does.
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

    // Both requests start; the plan changes between them.
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

test('a restored session whose inputs moved on is restored stale', async () => {
    const planner = bootPlanner({
        storedSession: { id: 'session-1', fingerprint: 'a plan that no longer matches these inputs' },
    });

    await planner.settleRestore();

    assert.deepEqual({
        saveDisabled: planner.saveButton.disabled,
        banner: planner.banner.hidden ? '' : planner.banner.textContent,
        storedSession: storedSessionId(planner),
    }, {
        saveDisabled: true,
        banner: 'Plan changed — recalculate routes before copying or saving them.',
        storedSession: null,
    });
});
