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

test('route edit responses render only while their requested session is active', () => {
    let activeSessionId = 'session-a';
    const rendered = [];
    const orchestrator = createRouteSessionOrchestrator({
        getActiveSessionId: () => activeSessionId,
        results: {
            render: html => rendered.push(html),
            reportError: () => {},
        },
    });

    orchestrator.applyEditResult({ requestedSessionId: 'session-a', ok: true, html: 'current routes' });
    activeSessionId = 'session-b';
    orchestrator.applyEditResult({ requestedSessionId: 'session-a', ok: true, html: 'stale routes' });

    assert.deepEqual(rendered, ['current routes']);
});

test('route edit errors are reported even after the requested session becomes stale', () => {
    const errors = [];
    const orchestrator = createRouteSessionOrchestrator({
        getActiveSessionId: () => 'session-b',
        results: {
            render: () => {},
            reportError: html => errors.push(html),
        },
    });

    orchestrator.applyEditResult({ requestedSessionId: 'session-a', ok: false, html: 'move failed' });

    assert.deepEqual(errors, ['move failed']);
});

test('saving with queued moves restores typed fields and submits the replacement form', async () => {
    const originalForm = { event_date: '2026-08-23', notes: 'Bring snacks', session_id: 'session-a' };
    let liveForm = originalForm;
    const submitted = [];
    const orchestrator = createRouteSessionOrchestrator({
        getActiveSessionId: () => 'session-a',
        results: { render: () => {}, reportError: () => {} },
        moves: {
            hasPending: () => true,
            flush: async () => {
                liveForm = { event_date: '', notes: '', session_id: 'session-a-fresh' };
                return true;
            },
        },
        saveForm: {
            snapshot: form => ({ event_date: form.event_date, notes: form.notes }),
            findLive: () => liveForm,
            canSubmit: () => true,
            restore: (form, values) => Object.assign(form, values),
            submit: form => submitted.push(form),
        },
    });

    const handled = await orchestrator.submitSaveWithQueuedMoves(originalForm);

    assert.deepEqual({ handled, liveForm, submitted }, {
        handled: true,
        liveForm: { event_date: '2026-08-23', notes: 'Bring snacks', session_id: 'session-a-fresh' },
        submitted: [liveForm],
    });
});

test('saving after a move restores fields without submitting an unsaveable replacement form', async () => {
    const originalForm = { event_date: '2026-08-23', notes: 'Bring snacks' };
    const liveForm = { event_date: '', notes: '' };
    let submitted = false;
    const orchestrator = createRouteSessionOrchestrator({
        getActiveSessionId: () => 'session-a',
        results: { render: () => {}, reportError: () => {} },
        moves: { hasPending: () => true, flush: async () => true },
        saveForm: {
            snapshot: form => ({ ...form }),
            findLive: () => liveForm,
            canSubmit: () => false,
            restore: (form, values) => Object.assign(form, values),
            submit: () => { submitted = true; },
        },
    });

    await orchestrator.submitSaveWithQueuedMoves(originalForm);

    assert.deepEqual({ liveForm, submitted }, {
        liveForm: { event_date: '2026-08-23', notes: 'Bring snacks' },
        submitted: false,
    });
});

test('saving after an error replacement tolerates the live form being absent', async () => {
    let submitted = false;
    const orchestrator = createRouteSessionOrchestrator({
        getActiveSessionId: () => 'session-a',
        results: { render: () => {}, reportError: () => {} },
        moves: { hasPending: () => true, flush: async () => false },
        saveForm: {
            snapshot: () => ({ event_date: '2026-08-23', notes: '' }),
            findLive: () => null,
            canSubmit: () => true,
            restore: () => {},
            submit: () => { submitted = true; },
        },
    });

    const handled = await orchestrator.submitSaveWithQueuedMoves({});

    assert.deepEqual({ handled, submitted }, { handled: true, submitted: false });
});

test('saving after a partial move flush restores fields but does not submit', async () => {
    const liveForm = { event_date: '', notes: '' };
    let submitted = false;
    const orchestrator = createRouteSessionOrchestrator({
        getActiveSessionId: () => 'session-a',
        results: { render: () => {}, reportError: () => {} },
        moves: { hasPending: () => true, flush: async () => false },
        saveForm: {
            snapshot: () => ({ event_date: '2026-08-23', notes: 'Bring snacks' }),
            findLive: () => liveForm,
            canSubmit: () => true,
            restore: (form, values) => Object.assign(form, values),
            submit: () => { submitted = true; },
        },
    });

    await orchestrator.submitSaveWithQueuedMoves({});

    assert.deepEqual({ liveForm, submitted }, {
        liveForm: { event_date: '2026-08-23', notes: 'Bring snacks' },
        submitted: false,
    });
});

test('a second save attempt during the same move flush does not submit twice', async () => {
    let finishFlush;
    const flush = new Promise(resolve => { finishFlush = resolve; });
    let submissions = 0;
    const orchestrator = createRouteSessionOrchestrator({
        getActiveSessionId: () => 'session-a',
        results: { render: () => {}, reportError: () => {} },
        moves: { hasPending: () => true, flush: () => flush },
        saveForm: {
            snapshot: () => ({ event_date: '2026-08-23', notes: '' }),
            findLive: () => ({}),
            canSubmit: () => true,
            restore: () => {},
            submit: () => { submissions += 1; },
        },
    });

    const firstSave = orchestrator.submitSaveWithQueuedMoves({});
    const secondSave = orchestrator.submitSaveWithQueuedMoves({});
    finishFlush(true);
    await Promise.all([firstSave, secondSave]);

    assert.equal(submissions, 1);
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

    assert.equal(await batcher.flush(), true);
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
