'use strict';

const test = require('node:test');
const assert = require('node:assert/strict');

const {
    createParticipantMoveBatcher,
    createRouteSessionOrchestrator,
    formatRouteText,
    generateMapsUrl,
    getStopEta,
    saveDraft,
} = require('./event-planner.js');

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
} = {}) {
    const rendered = [];
    const processed = [];
    const errors = [];
    let etaRefreshes = 0;
    let currentSessionId = activeSessionId;
    const resultsSection = {
        set innerHTML(html) { rendered.push(html); },
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
        getElementById: id => id === 'results-section' ? resultsSection : null,
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
        get etaRefreshes() { return etaRefreshes; },
        orchestrator,
        processed,
        rendered,
        resultsSection,
        setActiveSessionId: value => { currentSessionId = value; },
    };
}

test('route edit responses render only while their requested session is active', () => {
    const harness = createRouteSessionHarness();

    const currentResult = harness.orchestrator.applyEditResult({ requestedSessionId: 'session-a', ok: true, html: 'current routes' });
    harness.setActiveSessionId('session-b');
    const staleResult = harness.orchestrator.applyEditResult({ requestedSessionId: 'session-a', ok: true, html: 'stale routes' });

    assert.deepEqual({ currentResult, staleResult, rendered: harness.rendered, processed: harness.processed, etaRefreshes: harness.etaRefreshes }, {
        currentResult: true,
        staleResult: true,
        rendered: ['current routes'],
        processed: [harness.resultsSection],
        etaRefreshes: 1,
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

test('a queued save never restores or submits a replacement session form', async () => {
    let finishFlush;
    const flush = new Promise(resolve => { finishFlush = resolve; });
    const sessionAForm = createSaveForm({ eventDate: '2026-08-23', notes: 'Session A', sessionId: 'session-a' });
    const sessionBForm = createSaveForm({ eventDate: '2026-08-24', notes: 'Session B', sessionId: 'session-b' });
    let liveForm = sessionAForm;
    const harness = createRouteSessionHarness({
        getLiveForm: () => liveForm,
        hasPending: () => true,
        flush: () => flush,
    });

    const sessionASave = harness.orchestrator.submitSaveWithQueuedMoves(sessionAForm);
    harness.setActiveSessionId('session-b');
    liveForm = sessionBForm;
    const sessionBSave = harness.orchestrator.submitSaveWithQueuedMoves(sessionBForm);
    finishFlush(true);
    await Promise.all([sessionASave, sessionBSave]);

    assert.deepEqual({
        eventDate: sessionBForm.value('event_date'),
        notes: sessionBForm.value('notes'),
        sessionASubmits: sessionAForm.submitCount,
        sessionBSubmits: sessionBForm.submitCount,
    }, {
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

test('dropoff ETA includes two minutes of arrival slack', () => {
    const departure = new Date('2026-07-22T12:00:00.000Z');

    const eta = getStopEta(departure, 15 * 60, 30 * 60, 'dropoff', value => value.toISOString());

    assert.equal(eta, '2026-07-22T12:17:00.000Z');
});

test('pickup ETA counts backward from the required arrival time', () => {
    const arrival = new Date('2026-07-22T13:00:00.000Z');

    const eta = getStopEta(arrival, 10 * 60, 40 * 60, 'pickup', value => value.toISOString());

    assert.equal(eta, '2026-07-22T12:30:00.000Z');
});

test('pickup Maps URL starts at the driver, deduplicates stops, and ends at the activity', () => {
    const url = generateMapsUrl(
        { address: 'Church', lat: '40.4', lng: '-74.4' },
        { address: 'Driver', lat: '40.1', lng: '-74.1' },
        [
            { address: 'One', lat: '40.2', lng: '-74.2' },
            { address: 'Duplicate One', lat: '40.2000001', lng: '-74.2000001' },
            { address: 'Two', lat: '40.3', lng: '-74.3' },
        ],
        'pickup',
        { navigation: true },
    );

    assert.equal(
        url,
        'https://www.google.com/maps/dir/?api=1&travelmode=driving&destination=40.4%2C-74.4&dir_action=navigate&waypoints=40.2%2C-74.2%7C40.3%2C-74.3',
    );
});

test('parent copy text omits private addresses and the Maps link', () => {
    const text = formatRouteText(
        'Wednesday Night Church',
        { address: '1 Church Road', lat: '40.4', lng: '-74.4' },
        'Jordan Driver',
        { address: '9 Driver Lane', lat: '40.1', lng: '-74.1' },
        [{ name: 'Sam Rider', address: '5 Rider Street', time: '8:15 PM', lat: '40.2', lng: '-74.2' }],
        'dropoff',
        {
            includeParticipantAddresses: false,
            includeDriverAddress: false,
            includeMapsLink: false,
        },
    );

    assert.equal(
        text,
        'Activity Location: Wednesday Night Church\n1 Church Road\n\nDriver: Jordan Driver\n1. 8:15 PM - Sam Rider\n',
    );
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
