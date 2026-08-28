(function (root, factory) {
    'use strict';

    const planner = factory(root);
    if (typeof module === 'object' && module.exports) {
        module.exports = planner;
    }
})(typeof globalThis !== 'undefined' ? globalThis : this, function (root) {
    'use strict';

    function saveDraft({ abortRestore, clearActiveSession, writeDraft }) {
        abortRestore();
        clearActiveSession();
        writeDraft();
    }

    const SAVE_EVENT_ENDPOINT = '/api/v1/events';

    function createRouteSessionOrchestrator({ document, htmx, moves, reportError, refreshEtas }) {
        const savesInFlight = new Set();

        function getActiveSessionId() {
            const container = document.querySelector('.routes-container');
            return container ? container.dataset.sessionId : null;
        }

        function renderResults(html) {
            const resultsSection = document.getElementById('results-section');
            if (!resultsSection) return;

            resultsSection.innerHTML = html;
            htmx.process(resultsSection);
            applyLocalEventDate(resultsSection);
            refreshEtas();
        }

        function applyEditResult({ requestedSessionId, ok, html }) {
            if (!ok) {
                reportError(html);
                return false;
            }
            if (getActiveSessionId() !== requestedSessionId) {
                // The request succeeded, but its HTML no longer owns the view.
                return true;
            }

            renderResults(html);
            return true;
        }

        function getSaveFormSessionId(form) {
            return form.elements.namedItem('session_id')?.value || null;
        }

        function hasQueuedMoves(form) {
            const sessionId = getSaveFormSessionId(form);
            return Boolean(sessionId)
                && getActiveSessionId() === sessionId
                && moves.hasPending(sessionId);
        }

        function snapshotSaveForm(form) {
            return {
                event_date: form.elements.namedItem('event_date')?.value || '',
                notes: form.elements.namedItem('notes')?.value || '',
            };
        }

        function findLiveSaveForm() {
            const sessionInput = document.querySelector('#results-section form input[name="session_id"]');
            return sessionInput ? sessionInput.closest('form') : null;
        }

        function restoreSaveForm(form, values) {
            const eventDate = form.elements.namedItem('event_date');
            const notes = form.elements.namedItem('notes');
            if (eventDate) eventDate.value = values.event_date;
            if (notes) notes.value = values.notes;
        }

        function canSubmitSaveForm(form) {
            const submitButton = form.querySelector('button[type="submit"]');
            return form.getAttribute('hx-post') === SAVE_EVENT_ENDPOINT
                && Boolean(submitButton)
                && !submitButton.disabled;
        }

        function submitSaveForm(form) {
            if (typeof form.requestSubmit === 'function') {
                form.requestSubmit();
                return;
            }

            const submitEvent = new Event('submit', { bubbles: true, cancelable: true });
            if (form.dispatchEvent(submitEvent)) form.submit();
        }

        async function submitSaveWithQueuedMoves(form) {
            const requestedSessionId = getSaveFormSessionId(form);
            if (!requestedSessionId || getActiveSessionId() !== requestedSessionId) return false;
            if (savesInFlight.has(requestedSessionId)) return true;
            if (!moves.hasPending(requestedSessionId)) return false;

            const values = snapshotSaveForm(form);
            savesInFlight.add(requestedSessionId);
            let flushed = false;
            let liveForm = null;
            try {
                flushed = await moves.flush(requestedSessionId);
            } finally {
                if (getActiveSessionId() === requestedSessionId) {
                    const candidate = findLiveSaveForm();
                    if (candidate && getSaveFormSessionId(candidate) === requestedSessionId) {
                        liveForm = candidate;
                        restoreSaveForm(liveForm, values);
                    }
                }
                // requestSubmit re-enters the capture listener, so unlock first.
                savesInFlight.delete(requestedSessionId);
            }

            if (flushed && liveForm && canSubmitSaveForm(liveForm)) {
                submitSaveForm(liveForm);
            }
            return true;
        }

        return { applyEditResult, hasQueuedMoves, submitSaveWithQueuedMoves };
    }

    const DROPOFF_ETA_SLACK_SECS = 2 * 60;

    function formatClockTime(value) {
        if (!(value instanceof Date) || Number.isNaN(value.getTime())) {
            return '';
        }

        return new Intl.DateTimeFormat(undefined, {
            hour: 'numeric',
            minute: '2-digit',
        }).format(value);
    }

    function getStopEta(baseTime, cumulativeDurationSecs, routeDurationSecs, mode, formatTime) {
        if (!(baseTime instanceof Date) || Number.isNaN(baseTime.getTime())) {
            return '';
        }
        if (cumulativeDurationSecs === null) {
            return '';
        }

        let offsetSecs = cumulativeDurationSecs;
        if (mode === 'pickup') {
            if (routeDurationSecs === null) {
                return '';
            }
            offsetSecs = cumulativeDurationSecs - routeDurationSecs;
        } else {
            offsetSecs += DROPOFF_ETA_SLACK_SECS;
        }

        return formatTime(new Date(baseTime.getTime() + (offsetSecs * 1000)));
    }

    function parseNumber(value) {
        const num = Number.parseFloat(value);
        return Number.isFinite(num) ? num : null;
    }

    function getLocationValue(location) {
        const lat = parseNumber(location?.lat);
        const lng = parseNumber(location?.lng);
        if (lat !== null && lng !== null) {
            return `${lat},${lng}`;
        }

        const address = (location?.address || '').trim();
        return address || null;
    }

    function getLocationDedupKey(location) {
        const lat = parseNumber(location?.lat);
        const lng = parseNumber(location?.lng);
        if (lat !== null && lng !== null) {
            return `${lat.toFixed(5)},${lng.toFixed(5)}`;
        }

        const address = (location?.address || '').trim();
        return address ? address.toLowerCase() : null;
    }

    function dedupeStopsByLocation(stops) {
        const seen = new Set();
        return stops.filter(stop => {
            const dedupKey = getLocationDedupKey(stop);
            if (!dedupKey) return true;
            if (seen.has(dedupKey)) return false;
            seen.add(dedupKey);
            return true;
        });
    }

    function formatDisplayAddress(location) {
        const address = (location?.address || '').trim();
        const addressName = (location?.addressName || '').trim();
        return addressName ? `${addressName} (${address})` : address;
    }

    function generateMapsUrl(activityLocation, driverLocation, stops, mode = 'dropoff', options = {}) {
        if (!stops || stops.length === 0) return '';

        const uniqueStops = dedupeStopsByLocation(stops);
        const locations = mode === 'pickup'
            ? [driverLocation, ...uniqueStops, activityLocation]
            : [activityLocation, ...uniqueStops, driverLocation];
        const resolvedLocations = locations.map(getLocationValue);
        if (resolvedLocations.some(location => !location) || resolvedLocations.length < 2) return '';

        const [origin, ...rest] = resolvedLocations;
        const destination = rest[rest.length - 1];
        const waypoints = rest.slice(0, -1);
        const params = new URLSearchParams({
            api: '1',
            travelmode: 'driving',
            destination,
        });

        if (options.navigation === true) {
            params.set('dir_action', 'navigate');
        } else {
            params.set('origin', origin);
        }
        if (waypoints.length > 0) {
            params.set('waypoints', waypoints.join('|'));
        }

        return `https://www.google.com/maps/dir/?${params.toString()}`;
    }

    function createRouteHandoff({ platform, formatTime = formatClockTime }) {
        function parseRouteTime(value) {
            if (typeof value !== 'string') return null;

            const match = value.trim().match(/^(\d{2}):(\d{2})$/);
            if (!match) return null;

            const baseTime = new Date();
            baseTime.setHours(Number.parseInt(match[1], 10), Number.parseInt(match[2], 10), 0, 0);
            return baseTime;
        }

        function readStops(routeCard, context, includeEtas = true) {
            const routeDurationSecs = parseNumber(routeCard.dataset.routeDurationSecs);
            return Array.from(routeCard.querySelectorAll('.stop-item')).map(item => ({
                element: item,
                name: item.dataset.participantName,
                address: item.dataset.participantAddress,
                addressName: item.dataset.participantAddressName,
                lat: item.dataset.participantLat,
                lng: item.dataset.participantLng,
                time: includeEtas ? getStopEta(
                    context.baseTime,
                    parseNumber(item.dataset.stopCumulativeDurationSecs),
                    routeDurationSecs,
                    context.mode,
                    formatTime,
                ) : '',
            }));
        }

        function readContainerContext(container) {
            return {
                activityLocationName: container.dataset.activityLocationName,
                activityLocation: {
                    address: container.dataset.activityLocationAddress,
                    lat: container.dataset.activityLocationLat,
                    lng: container.dataset.activityLocationLng,
                },
                baseTime: parseRouteTime(container.dataset.routeTime),
                mode: container.dataset.routeMode || 'dropoff',
            };
        }

        function readRouteCard(routeCard, context, includeEtas = true) {
            return {
                driverName: routeCard.dataset.driverName,
                driverLocation: {
                    address: routeCard.dataset.driverAddress,
                    addressName: routeCard.dataset.driverAddressName,
                    lat: routeCard.dataset.driverLat,
                    lng: routeCard.dataset.driverLng,
                },
                stops: readStops(routeCard, context, includeEtas),
            };
        }

        function formatRouteSection(context, route, audience) {
            const isParentCopy = audience === 'parent';
            let text = `Driver: ${route.driverName}\n`;
            if (!isParentCopy) {
                text += `${formatDisplayAddress(route.driverLocation)}\n`;
            }

            route.stops.forEach((stop, index) => {
                const prefix = stop.time ? `${stop.time} - ` : '';
                text += `${index + 1}. ${prefix}${stop.name}`;
                if (!isParentCopy && stop.address) {
                    text += ` - ${formatDisplayAddress(stop)}`;
                }
                text += '\n';
            });

            if (!isParentCopy) {
                const mapsUrl = generateMapsUrl(
                    context.activityLocation,
                    route.driverLocation,
                    route.stops,
                    context.mode,
                    { navigation: true },
                );
                text += `\nMaps: ${mapsUrl}\n`;
            }

            return text;
        }

        function formatHandoffText(context, routes, audience) {
            const header = `Activity Location: ${context.activityLocationName}\n${context.activityLocation.address || ''}\n\n`;
            return header + routes
                .map(route => formatRouteSection(context, route, audience))
                .join('\n');
        }

        async function copyToClipboard(text) {
            try {
                await platform.copyText(text);
                return true;
            } catch {
                platform.notify('Failed to copy to clipboard', 'error');
                return false;
            }
        }

        async function copyRoute(routeCard, audience = 'driver') {
            if (!routeCard) return false;

            const container = routeCard.closest('.routes-container');
            if (!container) return false;

            const context = readContainerContext(container);
            const text = formatHandoffText(context, [readRouteCard(routeCard, context)], audience);

            return copyToClipboard(text);
        }

        async function copyAllRoutes(container) {
            if (!container) return false;

            const routeCards = Array.from(container.querySelectorAll('.route-card'));
            if (routeCards.length === 0) return false;

            const context = readContainerContext(container);
            const routes = routeCards.map(routeCard => readRouteCard(routeCard, context));
            return copyToClipboard(formatHandoffText(context, routes, 'driver'));
        }

        async function previewRoute(routeCard) {
            if (!routeCard) return false;

            const container = routeCard.closest('.routes-container');
            if (!container) return false;

            const context = readContainerContext(container);
            const route = readRouteCard(routeCard, context, false);
            const mapsUrl = generateMapsUrl(
                context.activityLocation,
                route.driverLocation,
                route.stops,
                context.mode,
            );
            if (!mapsUrl) {
                platform.notify('Could not build a valid Google Maps route for this trip.', 'warning');
                return false;
            }

            try {
                await platform.openUrl(mapsUrl);
                return true;
            } catch {
                platform.notify('Failed to open browser', 'error');
                return false;
            }
        }

        function populateEtas(container) {
            if (!container) return;

            const context = readContainerContext(container);
            if (!context.baseTime) {
                container.querySelectorAll('.stop-eta').forEach(element => {
                    element.textContent = '';
                });
                return;
            }

            container.querySelectorAll('.route-card').forEach(routeCard => {
                readStops(routeCard, context).forEach(stop => {
                    const etaElement = stop.element.querySelector('.stop-eta');
                    if (etaElement) etaElement.textContent = stop.time || '';
                });
            });
        }

        return { copyAllRoutes, copyRoute, populateEtas, previewRoute };
    }

    function createParticipantMoveBatcher({
        sendBatch,
        schedule = callback => setTimeout(callback, 500),
        cancel = timeout => clearTimeout(timeout),
        batchLimit = 64,
    }) {
        const queue = [];
        let timeout = null;
        let flushPromise = null;
        let activeSessionId = null;

        function scheduleFlush() {
            if (timeout !== null) cancel(timeout);
            timeout = schedule(flush);
        }

        function enqueue(move) {
            queue.push(move);
            scheduleFlush();
        }

        function takeBatch() {
            const sessionId = queue[0]?.session_id;
            if (!sessionId) {
                queue.shift();
                return [];
            }

            const moves = [];
            while (queue.length > 0 && moves.length < batchLimit && queue[0]?.session_id === sessionId) {
                moves.push(queue.shift());
            }
            return moves;
        }

        function toPayload(moves) {
            if (moves.length === 1) return moves[0];
            return {
                session_id: moves[0].session_id,
                moves: moves.map(move => ({
                    participant_id: move.participant_id,
                    from_route_index: move.from_route_index,
                    to_route_index: move.to_route_index,
                    insert_at_position: move.insert_at_position,
                })),
            };
        }

        async function run() {
            const sessionOutcomes = new Map();
            while (queue.length > 0) {
                const moves = takeBatch();
                if (moves.length === 0) return { succeeded: false, sessionOutcomes };

                const sessionId = moves[0].session_id;
                activeSessionId = sessionId;
                let succeeded;
                try {
                    succeeded = await sendBatch(toPayload(moves));
                } catch (error) {
                    queue.unshift(...moves);
                    throw error;
                } finally {
                    activeSessionId = null;
                }
                sessionOutcomes.set(sessionId, (sessionOutcomes.get(sessionId) ?? true) && succeeded);
                if (!succeeded) return { succeeded: false, sessionOutcomes };
            }
            return { succeeded: true, sessionOutcomes };
        }

        async function flushDetailed() {
            if (timeout !== null) {
                cancel(timeout);
                timeout = null;
            }
            if (flushPromise) return flushPromise;

            flushPromise = run();
            try {
                return await flushPromise;
            } finally {
                flushPromise = null;
                if (queue.length > 0 && timeout === null) scheduleFlush();
            }
        }

        async function flush() {
            const result = await flushDetailed();
            return result.succeeded;
        }

        function hasPending() {
            return queue.length > 0 || timeout !== null || flushPromise !== null;
        }

        function hasPendingFor(sessionId) {
            return activeSessionId === sessionId || queue.some(move => move.session_id === sessionId);
        }

        async function flushFor(sessionId) {
            let succeeded = true;
            while (hasPendingFor(sessionId)) {
                const result = await flushDetailed();
                if (result.sessionOutcomes.get(sessionId) === false) succeeded = false;
            }
            return succeeded;
        }

        return { enqueue, flush, flushFor, hasPending, hasPendingFor };
    }

    function bootBrowser() {
        function getToastContainer() {
            let container = document.getElementById('toast-container');
            if (!container) {
                container = document.createElement('div');
                container.id = 'toast-container';
                container.className = 'toast-container';
                document.body.appendChild(container);
            }
            return container;
        }

        function showToast(message, type = 'error', duration = 5000) {
            const container = getToastContainer();

            const toast = document.createElement('div');
            toast.className = 'toast' + (type === 'warning' ? ' toast-warning' : type === 'success' ? ' toast-success' : '');

            const iconSvg = type === 'success'
                ? '<svg class="toast-icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M20 6L9 17l-5-5"/></svg>'
                : '<svg class="toast-icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><circle cx="12" cy="12" r="10"/><line x1="12" y1="8" x2="12" y2="12"/><line x1="12" y1="16" x2="12.01" y2="16"/></svg>';

            toast.innerHTML = iconSvg;

            const messageSpan = document.createElement('span');
            messageSpan.className = 'toast-message';
            messageSpan.textContent = message;
            toast.appendChild(messageSpan);

            const closeSpan = document.createElement('span');
            closeSpan.className = 'toast-close';
            closeSpan.innerHTML = '&times;';
            toast.appendChild(closeSpan);

            toast.addEventListener('click', () => dismissToast(toast));

            container.appendChild(toast);

            if (duration > 0) {
                setTimeout(() => dismissToast(toast), duration);
            }

            return toast;
        }

        function dismissToast(toast) {
            if (!toast || toast.classList.contains('toast-out')) return;
            toast.classList.add('toast-out');
            setTimeout(() => toast.remove(), 200);
        }

        document.addEventListener('showToast', function(evt) {
            const detail = evt.detail;
            if (detail && detail.message) {
                showToast(detail.message, detail.type || 'success', detail.duration || 5000);
            }
        });

        function extractErrorMessage(response) {
            if (response.includes('class="alert')) {
                const match = response.match(/<div[^>]*class="alert[^"]*"[^>]*>([^<]+)</);
                if (match) return match[1].trim();
            }
            try {
                const json = JSON.parse(response);
                if (json.error && json.error.message) return json.error.message;
                if (json.message) return json.message;
            } catch (e) {
                // Ignore the parse failure; strip HTML below.
            }
            return response.replace(/<[^>]*>/g, '').trim() || 'An error occurred';
        }

        function showRouteError(response) {
            const message = extractErrorMessage(response);
            showToast(message, 'error');
        }

        const routeHandoff = createRouteHandoff({
            platform: {
                copyText: async text => {
                    try {
                        await navigator.clipboard.writeText(text);
                    } catch (error) {
                        console.error('Failed to copy to clipboard:', error);
                        throw error;
                    }
                },
                openUrl: async url => {
                    // Open synchronously; noopener returns null even on success.
                    window.open(url, '_blank', 'noopener,noreferrer');
                },
                notify: showToast,
            },
        });

        function refreshEtas() {
            routeHandoff.populateEtas(document.querySelector('.routes-container'));
        }

        function getSessionId() {
            const container = document.querySelector('.routes-container');
            return container ? container.dataset.sessionId : null;
        }

        let participantMoveBatcher;
        const routeSessionOrchestrator = createRouteSessionOrchestrator({
            document,
            htmx,
            moves: {
                hasPending: function(sessionId) {
                    return participantMoveBatcher.hasPendingFor(sessionId);
                },
                flush: function(sessionId) {
                    return participantMoveBatcher.flushFor(sessionId);
                },
            },
            reportError: showRouteError,
            refreshEtas,
        });

        participantMoveBatcher = createParticipantMoveBatcher({
            sendBatch: async function(payload) {
                try {
                    const response = await fetch('/api/v1/routes/edit/move-participant', {
                        method: 'POST',
                        headers: {
                            'Content-Type': 'application/json',
                            'HX-Request': 'true'
                        },
                        body: JSON.stringify(payload)
                    });

                    const html = await response.text();
                    return routeSessionOrchestrator.applyEditResult({
                        requestedSessionId: payload.session_id,
                        ok: response.ok,
                        html,
                    });
                } catch (err) {
                    console.error('Failed to move participant:', err);
                    showRouteError('Failed to move participant: ' + err.message);
                    return false;
                }
            }
        });

        async function moveParticipant(participantId, fromRouteIndex, toRouteIndex) {
            if (toRouteIndex === '' || toRouteIndex === null) return;

            const sessionId = getSessionId();
            if (!sessionId) {
                showToast('Session not found', 'error');
                return;
            }

            participantMoveBatcher.enqueue({
                session_id: sessionId,
                participant_id: parseInt(participantId),
                from_route_index: parseInt(fromRouteIndex),
                to_route_index: parseInt(toRouteIndex),
                insert_at_position: -1
            });
        }

        function isSaveEventForm(form) {
            return form instanceof HTMLFormElement && form.getAttribute('hx-post') === SAVE_EVENT_ENDPOINT;
        }

        document.addEventListener('submit', async function(evt) {
            const form = evt.target;
            if (!isSaveEventForm(form) || !routeSessionOrchestrator.hasQueuedMoves(form)) {
                return;
            }

            evt.preventDefault();
            evt.stopImmediatePropagation();
            await routeSessionOrchestrator.submitSaveWithQueuedMoves(form);
        }, true);

        async function swapDrivers(routeIndex1) {
            const selectElement = document.getElementById('swap-select-' + routeIndex1);
            const routeIndex2 = selectElement ? selectElement.value : null;

            if (!routeIndex2) {
                showToast('Please select a driver to swap with', 'warning');
                return;
            }

            const sessionId = getSessionId();
            if (!sessionId) {
                showToast('Session not found', 'error');
                return;
            }

            try {
                const response = await fetch('/api/v1/routes/edit/swap-drivers', {
                    method: 'POST',
                    headers: {
                        'Content-Type': 'application/json',
                        'HX-Request': 'true'
                    },
                    body: JSON.stringify({
                        session_id: sessionId,
                        route_index_1: parseInt(routeIndex1),
                        route_index_2: parseInt(routeIndex2)
                    })
                });

                const html = await response.text();
                routeSessionOrchestrator.applyEditResult({
                    requestedSessionId: sessionId,
                    ok: response.ok,
                    html,
                });
            } catch (err) {
                console.error('Failed to swap drivers:', err);
                showRouteError('Failed to swap drivers: ' + err.message);
            }
        }

        async function resetRoutes() {
            const sessionId = getSessionId();
            if (!sessionId) {
                showToast('Session not found', 'error');
                return;
            }

            try {
                const response = await fetch('/api/v1/routes/edit/reset?session_id=' + encodeURIComponent(sessionId), {
                    method: 'POST',
                    headers: {
                        'HX-Request': 'true'
                    }
                });

                const html = await response.text();
                routeSessionOrchestrator.applyEditResult({
                    requestedSessionId: sessionId,
                    ok: response.ok,
                    html,
                });
            } catch (err) {
                console.error('Failed to reset routes:', err);
                showRouteError('Failed to reset routes: ' + err.message);
            }
        }

        async function addUnusedDriver(driverId) {
            const sessionId = getSessionId();
            if (!sessionId) {
                showToast('Session not found', 'error');
                return;
            }

            try {
                const response = await fetch('/api/v1/routes/edit/add-driver', {
                    method: 'POST',
                    headers: {
                        'Content-Type': 'application/json',
                        'HX-Request': 'true'
                    },
                    body: JSON.stringify({
                        session_id: sessionId,
                        driver_id: parseInt(driverId)
                    })
                });

                const html = await response.text();
                routeSessionOrchestrator.applyEditResult({
                    requestedSessionId: sessionId,
                    ok: response.ok,
                    html,
                });
            } catch (err) {
                console.error('Failed to add driver:', err);
                showRouteError('Failed to add driver: ' + err.message);
            }
        }

        function showCopied(button, baseClass) {
            if (!button) return;

            const originalText = button.textContent;
            const originalWidth = button.style.width;
            button.style.width = `${button.offsetWidth}px`;
            button.textContent = 'Copied!';
            button.classList.add('btn-success');
            button.classList.remove(baseClass);

            setTimeout(() => {
                button.textContent = originalText;
                button.classList.remove('btn-success');
                button.classList.add(baseClass);
                button.style.width = originalWidth;
            }, 2000);
        }

        async function copyRoute(button, audience) {
            const copied = await routeHandoff.copyRoute(button?.closest('.route-card'), audience);
            if (copied) showCopied(button, 'btn-outline');
            return copied;
        }

        async function copyAllRoutes() {
            const copied = await routeHandoff.copyAllRoutes(document.querySelector('.routes-container'));
            if (copied) showCopied(document.getElementById('copy-all-btn'), 'btn-secondary');
            return copied;
        }

        function previewRoute(button) {
            return routeHandoff.previewRoute(button?.closest('.route-card'));
        }

        refreshEtas();

        document.addEventListener('htmx:afterSwap', function(event) {
            if (event.detail && event.detail.target && event.detail.target.id === 'results-section') {
                refreshEtas();
            }
        });

        const EVENT_PLANNER_DRAFT_KEY = 'ride-home-router:event-planner-draft:v1';
        const ACTIVE_SESSION_KEY = 'ride-home-router:active-session-id';
        const EVENT_PLANNER_MODES = new Set(['dropoff', 'pickup']);
        let isRestoringEventPlannerDraft = false;
        let restoreController = null;

        function saveActiveSessionId(id) {
            try { window.localStorage.setItem(ACTIVE_SESSION_KEY, id || ''); } catch (e) {}
        }

        function getActiveSessionId() {
            try { return window.localStorage.getItem(ACTIVE_SESSION_KEY) || ''; } catch (e) { return ''; }
        }

        function clearActiveSessionId() {
            try { window.localStorage.removeItem(ACTIVE_SESSION_KEY); } catch (e) {}
        }

        function getCheckedInputs(selector) {
            return Array.from(document.querySelectorAll(selector)).filter(input => input.checked);
        }

        function getEventForm() {
            return document.getElementById('event-form');
        }

        function getEventPlannerDraft() {
            try {
                const raw = window.localStorage.getItem(EVENT_PLANNER_DRAFT_KEY);
                if (!raw) return null;

                const parsed = JSON.parse(raw);
                return parsed && typeof parsed === 'object' ? parsed : null;
            } catch (err) {
                console.warn('Failed to read event planner draft', err);
                return null;
            }
        }

        function saveEventPlannerDraft() {
            if (isRestoringEventPlannerDraft) return;

            saveDraft({
                abortRestore: function() {
                    if (restoreController) restoreController.abort();
                },
                clearActiveSession: clearActiveSessionId,
                writeDraft: function() {
                    const form = getEventForm();
                    if (!form) return;

                    const activityLocation = form.querySelector('select[name="activity_location_id"]');
                    const selectedMode = form.querySelector('input[name="mode"]:checked');
                    const routeTime = form.querySelector('input[name="route_time"]');
                    const draft = {
                        activityLocationId: activityLocation ? activityLocation.value : '',
                        participantIds: getCheckedInputs('.participant-checkbox').map(input => input.value),
                        driverIds: getCheckedInputs('.driver-checkbox').map(input => input.value),
                        mode: selectedMode ? selectedMode.value : 'dropoff',
                        routeTime: routeTime ? routeTime.value : '',
                        vanAssignments: getVanAssignments(),
                        labelFilters: getPlannerLabelFilters(),
                    };

                    try {
                        window.localStorage.setItem(EVENT_PLANNER_DRAFT_KEY, JSON.stringify(draft));
                    } catch (err) {
                        console.warn('Failed to save event planner draft', err);
                    }
                },
            });
        }

        function clearEventPlannerDraft() {
            try {
                window.localStorage.removeItem(EVENT_PLANNER_DRAFT_KEY);
            } catch (err) {
                console.warn('Failed to clear event planner draft', err);
            }
        }

        function applyCheckedValues(selector, selectedValues) {
            const values = new Set((selectedValues || []).map(String));
            document.querySelectorAll(selector).forEach(input => {
                const checked = values.has(String(input.value));
                input.checked = checked;
                const row = input.closest('.select-row');
                if (row) row.classList.toggle('is-selected', checked);
            });
        }

        function restoreEventPlannerDraft() {
            const draft = getEventPlannerDraft();
            const form = getEventForm();
            if (!draft || !form) return;

            isRestoringEventPlannerDraft = true;
            try {
                const activityLocation = form.querySelector('select[name="activity_location_id"]');
                if (activityLocation && typeof draft.activityLocationId === 'string') {
                    activityLocation.value = draft.activityLocationId;
                }

                const mode = typeof draft.mode === 'string' && EVENT_PLANNER_MODES.has(draft.mode)
                    ? draft.mode
                    : null;
                if (mode) {
                    const modeInput = form.querySelector(`input[name="mode"][value="${mode}"]`);
                    if (modeInput) modeInput.checked = true;
                }

                const routeTime = form.querySelector('input[name="route_time"]');
                if (routeTime && typeof draft.routeTime === 'string') {
                    routeTime.value = draft.routeTime;
                }

                applyCheckedValues('.participant-checkbox', draft.participantIds);
                applyCheckedValues('.driver-checkbox', draft.driverIds);
                applyPlannerLabelFilters(draft.labelFilters);
                recomputeSelectListVisibility('participants-selection');
                recomputeSelectListVisibility('drivers-selection');

                renderVanAssignmentsPanel();

                if (draft.vanAssignments && typeof draft.vanAssignments === 'object') {
                    Object.entries(draft.vanAssignments).forEach(([driverId, vehicleId]) => {
                        const select = document.getElementById(`van-assignment-${driverId}`);
                        if (select) {
                            select.value = String(vehicleId);
                        }
                    });
                    handleVanAssignmentChange();
                } else {
                    updateEventStats();
                }
            } finally {
                updateRouteTimeCopy();
                ensureDefaultRouteTime();
                isRestoringEventPlannerDraft = false;
            }
        }

        function updateRouteTimeCopy() {
            const form = getEventForm();
            if (!form) return;

            const selectedMode = form.querySelector('input[name="mode"]:checked');
            const mode = selectedMode ? selectedMode.value : 'dropoff';
            const label = document.getElementById('route-time-label');
            const help = document.getElementById('route-time-help');
            const input = form.querySelector('input[name="route_time"]');

            if (mode === 'pickup') {
                if (label) label.textContent = 'Arrive at activity location by';
                if (help) help.textContent = 'Used to back-calculate the expected arrival time at each stop in copied driver and parent lists.';
                if (input) input.setAttribute('aria-label', 'Arrive at activity location by');
                return;
            }

            if (label) label.textContent = 'Depart activity location at';
            if (help) help.textContent = 'Used to calculate the expected arrival time at each stop in copied driver and parent lists.';
            if (input) input.setAttribute('aria-label', 'Depart activity location at');
        }

        function getDefaultRouteTimeValue() {
            const now = new Date();
            const rounded = Math.ceil(now.getMinutes() / 15) * 15;
            now.setSeconds(0, 0);
            if (rounded === 60) {
                now.setHours(now.getHours() + 1, 0, 0, 0);
            } else {
                now.setMinutes(rounded, 0, 0);
            }

            const hours = String(now.getHours()).padStart(2, '0');
            const minutes = String(now.getMinutes()).padStart(2, '0');
            return `${hours}:${minutes}`;
        }

        function ensureDefaultRouteTime() {
            const form = getEventForm();
            if (!form) return;

            const input = form.querySelector('input[name="route_time"]');
            if (!input || input.value) return;

            input.value = getDefaultRouteTimeValue();
        }

        function escapeHtml(value) {
            return String(value || '')
                .replace(/&/g, '&amp;')
                .replace(/</g, '&lt;')
                .replace(/>/g, '&gt;')
                .replace(/"/g, '&quot;')
                .replace(/'/g, '&#39;');
        }

        function getOrgVehicles() {
            const el = document.getElementById('event-org-vehicles');
            if (!el) return [];

            try {
                const parsed = JSON.parse(el.textContent || '[]');
                return Array.isArray(parsed) ? parsed : [];
            } catch (err) {
                console.error('Failed to parse vans JSON', err);
                return [];
            }
        }

        function getVanAssignments() {
            const assignments = {};
            document.querySelectorAll('.van-assignment-select').forEach(select => {
                if (!select.disabled && select.value) {
                    assignments[select.dataset.driverId] = select.value;
                }
            });
            return assignments;
        }

        function updateVanSelectionOptions() {
            const selects = document.querySelectorAll('.van-assignment-select');
            const assignedVehicles = new Map();

            selects.forEach(select => {
                if (!select.disabled && select.value) {
                    assignedVehicles.set(select.value, select.dataset.driverId);
                }
            });

            selects.forEach(select => {
                Array.from(select.options).forEach(option => {
                    if (!option.value) {
                        option.disabled = false;
                        return;
                    }

                    const owner = assignedVehicles.get(option.value);
                    option.disabled = !!owner && owner !== select.dataset.driverId;
                });
            });
        }

        function handleVanAssignmentChange() {
            updateVanSelectionOptions();
            updateEventStats();
            saveEventPlannerDraft();
        }

        function renderVanAssignmentsPanel() {
            const orgVehicles = getOrgVehicles();
            const existingAssignments = getVanAssignments();
            document.querySelectorAll('.driver-checkbox').forEach((checkbox) => {
                const driverId = checkbox.value;
                const row = checkbox.closest('.select-row');
                const inlineContainer = row ? row.querySelector('.van-assignment-inline') : null;
                const select = document.getElementById(`van-assignment-${driverId}`);
                const personalCapacity = parseInt(checkbox.dataset.capacity, 10) || 0;
                const selectedVehicleId = existingAssignments[driverId] || '';
                if (!row || !inlineContainer || !select) return;

                row.classList.toggle('has-van-assignment', checkbox.checked);
                inlineContainer.classList.toggle('hidden', !checkbox.checked);

                if (orgVehicles.length === 0) {
                    select.value = '';
                    select.disabled = true;
                    select.innerHTML = `<option value="" data-capacity="${personalCapacity}" selected>No vans saved yet</option>`;
                    return;
                }

                const options = orgVehicles.map((vehicle) => {
                    const selected = String(vehicle.id) === String(selectedVehicleId) ? ' selected' : '';
                    return `<option value="${vehicle.id}" data-capacity="${vehicle.capacity}"${selected}>${escapeHtml(vehicle.name)} (${vehicle.capacity} available seats)</option>`;
                }).join('');

                select.disabled = !checkbox.checked;
                select.innerHTML = `<option value="" data-capacity="${personalCapacity}">Personal vehicle</option>${options}`;
                select.value = checkbox.checked ? selectedVehicleId : '';
            });

            handleVanAssignmentChange();
        }

        function updateEventStats() {
            const participants = getCheckedInputs('.participant-checkbox');
            const drivers = getCheckedInputs('.driver-checkbox');

            let totalCapacity = 0;
            drivers.forEach(cb => {
                const select = document.getElementById(`van-assignment-${cb.value}`);
                if (select) {
                    totalCapacity += parseInt(select.options[select.selectedIndex].dataset.capacity, 10) || 0;
                    return;
                }
                totalCapacity += parseInt(cb.dataset.capacity, 10) || 0;
            });

            const participantsCount = participants.length;
            const driversCount = drivers.length;

            const participantsCountEl = document.getElementById('participants-selected-count');
            if (participantsCountEl) participantsCountEl.textContent = participantsCount;

            const driversCountEl = document.getElementById('drivers-selected-count');
            if (driversCountEl) driversCountEl.textContent = driversCount;

            const seatsEl = document.getElementById('drivers-selected-seats');
            if (seatsEl) seatsEl.textContent = totalCapacity;

            const statsEl = document.getElementById('selection-stats');
            if (statsEl) {
                const parts = [];
                parts.push(`${participantsCount} participant${participantsCount === 1 ? '' : 's'}`);
                parts.push(`${driversCount} driver${driversCount === 1 ? '' : 's'}`);
                parts.push(`${totalCapacity} seat${totalCapacity === 1 ? '' : 's'}`);
                statsEl.textContent = parts.join(' • ');

                statsEl.classList.toggle('text-danger', participantsCount > totalCapacity && driversCount > 0);
                statsEl.classList.toggle('text-muted', !(participantsCount > totalCapacity && driversCount > 0));
            }
        }

        function getActiveLabelFilters(listId) {
            return Array.from(document.querySelectorAll(`.label-filter-chip[data-list-id="${listId}"].is-active`))
                .map(button => button.dataset.labelId);
        }

        function getPlannerLabelFilters() {
            return {
                participants: getActiveLabelFilters('participants-selection'),
                drivers: getActiveLabelFilters('drivers-selection'),
            };
        }

        function applyPlannerLabelFilters(filters) {
            const participantFilters = Array.isArray(filters && filters.participants) ? filters.participants.map(String) : [];
            const driverFilters = Array.isArray(filters && filters.drivers) ? filters.drivers.map(String) : [];

            setActiveLabelFilters('participants-selection', participantFilters);
            setActiveLabelFilters('drivers-selection', driverFilters);
        }

        function setActiveLabelFilters(listId, activeIDs) {
            const active = new Set(activeIDs || []);
            document.querySelectorAll(`.label-filter-chip[data-list-id="${listId}"]`).forEach(button => {
                const isActive = active.has(String(button.dataset.labelId));
                button.classList.toggle('is-active', isActive);
                button.setAttribute('aria-pressed', isActive ? 'true' : 'false');
            });
        }

        function recomputeSelectListVisibility(listId) {
            const list = document.getElementById(listId);
            if (!list) return;

            const searchInput = document.querySelector(`input[data-filter-role="search"][data-list-id="${listId}"]`);
            const query = (searchInput ? searchInput.value : '').trim().toLowerCase();
            const activeLabels = new Set(getActiveLabelFilters(listId));
            const rows = list.querySelectorAll('.select-row');

            rows.forEach(row => {
                const haystack = (row.dataset.search || row.textContent || '').toLowerCase();
                const matchesSearch = query.length === 0 || haystack.includes(query);
                const rowLabels = (row.dataset.labels || '').trim();
                const labelIDs = rowLabels ? rowLabels.split(',').filter(Boolean) : [];
                const matchesLabels = activeLabels.size === 0 || labelIDs.some(id => activeLabels.has(id));
                row.classList.toggle('hidden', !(matchesSearch && matchesLabels));
            });
        }

        function filterSelectList(input, listId) {
            recomputeSelectListVisibility(listId);
            saveEventPlannerDraft();
        }

        function toggleLabelFilter(button) {
            const isActive = !button.classList.contains('is-active');
            button.classList.toggle('is-active', isActive);
            button.setAttribute('aria-pressed', isActive ? 'true' : 'false');
            recomputeSelectListVisibility(button.dataset.listId);
            saveEventPlannerDraft();
        }

        function clearPlannerFilters(listId) {
            setActiveLabelFilters(listId, []);
            const searchInput = document.querySelector(`input[data-filter-role="search"][data-list-id="${listId}"]`);
            if (searchInput) searchInput.value = '';
            recomputeSelectListVisibility(listId);
            saveEventPlannerDraft();
        }

        function clearAllPlannerFilters() {
            clearPlannerFilters('participants-selection');
            clearPlannerFilters('drivers-selection');
        }

        function selectAllParticipants() {
            document.querySelectorAll('.participant-checkbox').forEach(cb => {
                const row = cb.closest('.select-row');
                if (row && row.classList.contains('hidden')) return;
                cb.checked = true;
                if (row) row.classList.add('is-selected');
            });
            updateEventStats();
            saveEventPlannerDraft();
        }

        function selectAllDrivers() {
            document.querySelectorAll('.driver-checkbox').forEach(cb => {
                const row = cb.closest('.select-row');
                if (row && row.classList.contains('hidden')) return;
                cb.checked = true;
                if (row) row.classList.add('is-selected');
            });
            renderVanAssignmentsPanel();
            updateEventStats();
            saveEventPlannerDraft();
        }

        function clearSelections() {
            const form = getEventForm();
            document.querySelectorAll('.participant-checkbox, .driver-checkbox').forEach(cb => {
                cb.checked = false;
                const row = cb.closest('.select-row');
                if (row) row.classList.remove('is-selected');
            });
            if (form) {
                setActiveLabelFilters('participants-selection', []);
                setActiveLabelFilters('drivers-selection', []);
                form.querySelectorAll('input[data-filter-role="search"]').forEach(input => {
                    input.value = '';
                });
                recomputeSelectListVisibility('participants-selection');
                recomputeSelectListVisibility('drivers-selection');

                const activityLocation = form.querySelector('select[name="activity_location_id"]');
                if (activityLocation) {
                    activityLocation.value = '';
                    activityLocation.dispatchEvent(new Event('change', { bubbles: true }));
                }

                const defaultMode = form.querySelector('input[name="mode"][value="dropoff"]');
                if (defaultMode) defaultMode.checked = true;

                const routeTime = form.querySelector('input[name="route_time"]');
                if (routeTime) routeTime.value = getDefaultRouteTimeValue();
            }
            document.getElementById('results-section').innerHTML = `
        <div class="results-loading htmx-indicator calculate-indicator">
            <div class="loading-overlay">
                <span class="loading loading-lg"></span>
                <p>Calculating optimal routes…</p>
            </div>
        </div>
    `;
            renderVanAssignmentsPanel();
            updateEventStats();
            clearEventPlannerDraft();
            clearActiveSessionId();
        }

        function validateBeforeCalculate() {
            const participants = getCheckedInputs('.participant-checkbox');
            const drivers = getCheckedInputs('.driver-checkbox');
            const activityLocation = document.querySelector('select[name="activity_location_id"]');
            const routeTime = document.querySelector('input[name="route_time"]');

            if (activityLocation && !activityLocation.value) {
                showToast('Please choose an activity location for this event.', 'warning');
                return false;
            }
            if (routeTime && !routeTime.value) {
                const selectedMode = document.querySelector('input[name="mode"]:checked');
                const message = selectedMode && selectedMode.value === 'pickup'
                    ? 'Please choose the required arrival time at the activity location.'
                    : 'Please choose the departure time from the activity location.';
                showToast(message, 'warning');
                return false;
            }

            if (participants.length === 0 && drivers.length === 0) {
                showToast('Please select at least one participant and one driver.', 'warning');
                return false;
            }
            if (participants.length === 0) {
                showToast('Please select at least one participant.', 'warning');
                return false;
            }
            if (drivers.length === 0) {
                showToast('Please select at least one driver.', 'warning');
                return false;
            }

            return true;
        }

        function scrollResultsIntoView(target) {
            if (!target || target.id !== 'results-section') return;

            const hasVisibleResults = target.children.length > 0 && !target.querySelector('.calculate-indicator');
            if (!hasVisibleResults) return;

            const anchor = document.getElementById('calculate-btn');
            const scrollTarget = anchor || target;
            const prefersReducedMotion = window.matchMedia('(prefers-reduced-motion: reduce)').matches;
            window.requestAnimationFrame(() => {
                scrollTarget.scrollIntoView({
                    behavior: prefersReducedMotion ? 'auto' : 'smooth',
                    block: 'start',
                });
            });
        }

        function setCalculateButtonLoading(isLoading) {
            const button = document.getElementById('calculate-btn');
            if (!button) return;

            button.classList.toggle('is-loading', isLoading);
        }

        document.addEventListener('change', function(e) {
            if (!getEventForm()) return;

            if (e.target.classList.contains('driver-checkbox') || e.target.classList.contains('participant-checkbox')) {
                const row = e.target.closest('.select-row');
                if (row) row.classList.toggle('is-selected', e.target.checked);
                if (e.target.classList.contains('driver-checkbox')) {
                    renderVanAssignmentsPanel();
                }
                updateEventStats();
                saveEventPlannerDraft();
                return;
            }

            if (e.target.name === 'activity_location_id' || e.target.name === 'mode') {
                if (e.target.name === 'mode') {
                    updateRouteTimeCopy();
                }
                saveEventPlannerDraft();
                return;
            }

            if (e.target.name === 'route_time') {
                saveEventPlannerDraft();
            }
        });

        function restoreRouteSession(sessionId) {
            var resultsSection = document.getElementById('results-section');
            if (!resultsSection) return;

            if (restoreController) restoreController.abort();
            restoreController = new AbortController();

            // This same-origin HTML uses the same trusted templates as HTMX swaps.
            fetch('/api/v1/routes/session?session_id=' + encodeURIComponent(sessionId), {
                headers: { 'HX-Request': 'true' },
                signal: restoreController.signal
            })
            .then(function(response) {
                if (response.status === 204 || !response.ok) {
                    clearActiveSessionId();
                    return null;
                }
                return response.text();
            })
            .then(function(html) {
                if (html) {
                    resultsSection.innerHTML = html;
                    htmx.process(resultsSection);
                    applyLocalEventDate(resultsSection);
                    refreshEtas();
                }
            })
            .catch(function(err) {
                if (err.name !== 'AbortError') {
                    console.warn('Failed to restore route session', err);
                    clearActiveSessionId();
                }
            })
            .finally(function() {
                restoreController = null;
            });
        }

        if (getEventForm()) {
            restoreEventPlannerDraft();
            document.querySelectorAll('.participant-checkbox, .driver-checkbox').forEach(cb => {
                const row = cb.closest('.select-row');
                if (row) row.classList.toggle('is-selected', cb.checked);
            });
            updateEventStats();
            updateRouteTimeCopy();
            ensureDefaultRouteTime();

            var activeSessionId = getActiveSessionId();
            if (activeSessionId) {
                restoreRouteSession(activeSessionId);
            }

            document.body.addEventListener('htmx:beforeRequest', function(event) {
                const elt = event.detail && event.detail.elt;
                if (elt && elt.id === 'calculate-btn') {
                    if (restoreController) restoreController.abort();
                    setCalculateButtonLoading(true);
                }
            });

            document.body.addEventListener('htmx:afterRequest', function(event) {
                const elt = event.detail && event.detail.elt;
                if (elt && elt.id === 'calculate-btn') {
                    setCalculateButtonLoading(false);
                }
            });

            document.body.addEventListener('htmx:sendError', function(event) {
                const elt = event.detail && event.detail.elt;
                if (elt && elt.id === 'calculate-btn') {
                    setCalculateButtonLoading(false);
                }
            });

            document.body.addEventListener('htmx:responseError', function(event) {
                const elt = event.detail && event.detail.elt;
                if (elt && elt.id === 'calculate-btn') {
                    setCalculateButtonLoading(false);
                }
            });

            document.body.addEventListener('htmx:afterSwap', function(event) {
                const target = event.detail && event.detail.target;
                if (!target) return;

                if (target.id === 'results-section') {
                    scrollResultsIntoView(target);
                    var sid = getSessionId();
                    if (sid) saveActiveSessionId(sid);
                    refreshEtas();
                    return;
                }

                if (target.id !== 'save-result') return;

                if (target.querySelector('.alert-success')) {
                    clearEventPlannerDraft();
                    clearActiveSessionId();
                }
            });
        }

        root.showToast = showToast;
        root.moveParticipant = moveParticipant;
        root.swapDrivers = swapDrivers;
        root.resetRoutes = resetRoutes;
        root.addUnusedDriver = addUnusedDriver;
        root.copyRoute = copyRoute;
        root.copyAllRoutes = copyAllRoutes;
        root.previewRoute = previewRoute;
        document.addEventListener('DOMContentLoaded', () => applyLocalEventDate(document));
        document.addEventListener('htmx:afterSettle', event => applyLocalEventDate(event.target || document));
        root.handleVanAssignmentChange = handleVanAssignmentChange;
        root.filterSelectList = filterSelectList;
        root.toggleLabelFilter = toggleLabelFilter;
        root.clearPlannerFilters = clearPlannerFilters;
        root.selectAllParticipants = selectAllParticipants;
        root.selectAllDrivers = selectAllDrivers;
        root.clearSelections = clearSelections;
        root.validateBeforeCalculate = validateBeforeCalculate;

    }

    if (root && root.document) {
        if (root.document.readyState === 'loading') {
            root.document.addEventListener('DOMContentLoaded', bootBrowser, { once: true });
        } else {
            bootBrowser();
        }
    }


    // Avoid the UTC date shift caused by toISOString.
    function localISODate(date) {
        const pad = value => String(value).padStart(2, '0');
        return `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())}`;
    }

    // Replace the server date with the browser's calendar date after every swap.
    function applyLocalEventDate(scope, now = new Date()) {
        if (!scope || typeof scope.querySelectorAll !== 'function') return 0;
        const inputs = scope.querySelectorAll('input[type="date"][name="event_date"]');
        inputs.forEach(input => {
            if (!input.dataset.userEdited) input.value = localISODate(now);
            input.addEventListener('input', () => { input.dataset.userEdited = '1'; }, { once: true });
        });
        return inputs.length;
    }

    return {
        createParticipantMoveBatcher,
        createRouteHandoff,
        applyLocalEventDate,
        createRouteSessionOrchestrator,
        localISODate,
        saveDraft,
    };
});
