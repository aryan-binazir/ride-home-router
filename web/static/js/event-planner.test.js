'use strict';

const test = require('node:test');
const assert = require('node:assert/strict');

const planner = require('./event-planner.js');
const {
    createParticipantMoveBatcher,
    createRouteHandoff,
    applyLocalEventDate,
    createRouteSessionOrchestrator,
    installRouteResults,
    localISODate,
    saveDraft,
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
    // 23:30 local on 14 March: in any zone west of UTC toISOString() reports
    // 15 March, which is the bug the event-date default must not have.
    const lateEvening = new Date(2026, 2, 14, 23, 30);
    assert.equal(localISODate(lateEvening), '2026-03-14');
    assert.equal(localISODate(new Date(2026, 0, 5, 0, 10)), '2026-01-05');
});

test('installRouteResults performs every manual result-installation step in order', () => {
    const calls = [];
    const dateInput = {
        value: '2026-03-15',
        dataset: {},
        listeners: [],
        addEventListener(type, fn) { this.listeners.push([type, fn]); },
    };
    const target = {
        set innerHTML(html) { calls.push(['html', html]); },
        querySelectorAll(selector) {
            calls.push(['date', selector]);
            return [dateInput];
        },
    };

    installRouteResults({
        target,
        html: '<form>routes</form>',
        htmx: { process: element => calls.push(['htmx', element]) },
        refreshEtas: () => calls.push(['etas']),
    });

    assert.deepEqual(calls, [
        ['html', '<form>routes</form>'],
        ['htmx', target],
        ['date', 'input[type="date"][name="event_date"]'],
        ['etas'],
    ]);
    assert.equal(dateInput.value, localISODate(new Date()));
    assert.equal(dateInput.listeners[0][0], 'input');
});

test('installRouteResults preserves user-edited dates', () => {
    const dateInput = {
        value: '2026-03-20',
        dataset: { userEdited: '1' },
        addEventListener() {},
    };
    const target = { querySelectorAll: () => [dateInput] };

    installRouteResults({
        target,
        html: 'routes',
        htmx: { process() {} },
        refreshEtas() {},
    });

    assert.equal(dateInput.value, '2026-03-20');
});

test('installRouteResults still processes and refreshes a target without date-query support', () => {
    const calls = [];
    const target = { set innerHTML(html) { calls.push(['html', html]); } };

    installRouteResults({
        target,
        html: 'routes',
        htmx: { process: element => calls.push(['htmx', element]) },
        refreshEtas: () => calls.push(['etas']),
    });

    assert.deepEqual(calls, [
        ['html', 'routes'],
        ['htmx', target],
        ['etas'],
    ]);
});

test('planner exposes the route handoff instead of its internal helpers', () => {
    assert.deepEqual(Object.keys(planner).sort(), [
        'applyLocalEventDate',
        'createParticipantMoveBatcher',
        'createRouteHandoff',
        'createRouteSessionOrchestrator',
        'installRouteResults',
        'localISODate',
        'saveDraft',
    ]);
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
        refreshEtas: () => { etaRefreshes += 1; },
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

test('route edit responses install dates only while their requested session is active', () => {
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
        date: harness.dateInput.value,
        etaRefreshes: harness.etaRefreshes,
    }, {
        currentResult: true,
        staleResult: true,
        rendered: ['current routes'],
        processed: [harness.resultsSection],
        dateApplications: 1,
        date: localISODate(new Date()),
        etaRefreshes: 1,
    });
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

test('saving with queued moves restores typed fields and submits the replacement form', async () => {
    const originalForm = createSaveForm({ eventDate: '2026-08-23', notes: 'Bring snacks' });
    let liveForm = originalForm;
    const harness = createRouteSessionHarness({
        getLiveForm: () => liveForm,
        flush: async () => {
            liveForm = createSaveForm({ sessionId: 'session-a' });
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

test('saving after a move restores fields without submitting an unsaveable replacement form', async () => {
    const originalForm = createSaveForm({ eventDate: '2026-08-23', notes: 'Bring snacks' });
    const liveForm = createSaveForm({ saveEnabled: false });
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

test('saving after a partial move flush restores fields but does not submit', async () => {
    const originalForm = createSaveForm({ eventDate: '2026-08-23', notes: 'Bring snacks' });
    const liveForm = createSaveForm();
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

test('saving a draft aborts an in-flight restore before clearing the active session', () => {
    const actions = [];

    saveDraft({
        abortRestore: () => actions.push('abort restore'),
        clearActiveSession: () => actions.push('clear active session'),
        writeDraft: () => actions.push('write draft'),
    });

    assert.deepEqual(actions, [
        'abort restore',
        'clear active session',
        'write draft',
    ]);
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
