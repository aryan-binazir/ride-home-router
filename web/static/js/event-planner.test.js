'use strict';

const test = require('node:test');
const assert = require('node:assert/strict');

const {
    createParticipantMoveBatcher,
    formatRouteText,
    generateMapsUrl,
    getStopEta,
    saveDraft,
} = require('./event-planner.js');

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
