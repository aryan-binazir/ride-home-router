'use strict';

const test = require('node:test');
const assert = require('node:assert/strict');

const planner = require('./event-planner.js');
const {
    createParticipantMoveBatcher,
    createRouteHandoff,
    saveDraft,
} = planner;

test('planner exposes the route handoff instead of its internal helpers', () => {
    assert.deepEqual(Object.keys(planner).sort(), [
        'createParticipantMoveBatcher',
        'createRouteHandoff',
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

test('preview preserves resolved non-success responses from the open endpoint', async () => {
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
