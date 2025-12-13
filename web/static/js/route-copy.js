// Route copy and editing functionality for ride-home-router

// ============= Toast Notification System =============

/**
 * Gets or creates the toast container
 */
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

/**
 * Shows a toast notification
 * @param {string} message - The message to display
 * @param {string} type - 'error' (default), 'warning', or 'success'
 * @param {number} duration - Auto-dismiss time in ms (default 5000)
 */
function showToast(message, type = 'error', duration = 5000) {
    const container = getToastContainer();

    const toast = document.createElement('div');
    toast.className = 'toast' + (type === 'warning' ? ' toast-warning' : type === 'success' ? ' toast-success' : '');

    // Icon based on type
    const iconSvg = type === 'success'
        ? '<svg class="toast-icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M20 6L9 17l-5-5"/></svg>'
        : '<svg class="toast-icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><circle cx="12" cy="12" r="10"/><line x1="12" y1="8" x2="12" y2="12"/><line x1="12" y1="16" x2="12.01" y2="16"/></svg>';

    toast.innerHTML = `
        ${iconSvg}
        <span class="toast-message">${message}</span>
        <span class="toast-close">&times;</span>
    `;

    // Click to dismiss
    toast.addEventListener('click', () => dismissToast(toast));

    container.appendChild(toast);

    // Auto-dismiss
    if (duration > 0) {
        setTimeout(() => dismissToast(toast), duration);
    }

    return toast;
}

/**
 * Dismisses a toast with animation
 */
function dismissToast(toast) {
    if (!toast || toast.classList.contains('toast-out')) return;
    toast.classList.add('toast-out');
    setTimeout(() => toast.remove(), 200);
}

/**
 * Extracts error message from HTML or JSON response
 */
function extractErrorMessage(response) {
    // If it looks like HTML with an alert div, extract the text
    if (response.includes('class="alert')) {
        const match = response.match(/<div[^>]*class="alert[^"]*"[^>]*>([^<]+)</);
        if (match) return match[1].trim();
    }
    // Try to parse as JSON
    try {
        const json = JSON.parse(response);
        if (json.error && json.error.message) return json.error.message;
        if (json.message) return json.message;
    } catch (e) {
        // Not JSON, use as-is
    }
    // Strip HTML tags as fallback
    return response.replace(/<[^>]*>/g, '').trim() || 'An error occurred';
}

/**
 * Shows an error toast from an API response
 */
function showRouteError(response) {
    const message = extractErrorMessage(response);
    showToast(message, 'error');
}

// ============= Helper Functions =============

// ============= Route Editing Functions =============

/**
 * Gets the session ID from the routes container
 */
function getSessionId() {
    const container = document.querySelector('.routes-container');
    return container ? container.dataset.sessionId : null;
}

/**
 * Moves a participant from one route to another
 */
async function moveParticipant(participantId, fromRouteIndex, toRouteIndex) {
    if (toRouteIndex === '' || toRouteIndex === null) return;

    const sessionId = getSessionId();
    if (!sessionId) {
        showToast('Session not found', 'error');
        return;
    }

    try {
        const response = await fetch('/api/v1/routes/edit/move-participant', {
            method: 'POST',
            headers: {
                'Content-Type': 'application/json',
                'HX-Request': 'true'
            },
            body: JSON.stringify({
                session_id: sessionId,
                participant_id: parseInt(participantId),
                from_route_index: parseInt(fromRouteIndex),
                to_route_index: parseInt(toRouteIndex),
                insert_at_position: -1
            })
        });

        const html = await response.text();
        const routeResults = document.getElementById('results-section');
        if (routeResults) {
            if (!response.ok) {
                // Show error inline above routes
                showRouteError(html);
            } else {
                routeResults.innerHTML = html;
            }
        }
    } catch (err) {
        console.error('Failed to move participant:', err);
        showRouteError('Failed to move participant: ' + err.message);
    }
}

/**
 * Swaps drivers between two routes
 */
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
        const routeResults = document.getElementById('results-section');
        if (routeResults) {
            if (!response.ok) {
                showRouteError(html);
            } else {
                routeResults.innerHTML = html;
            }
        }
    } catch (err) {
        console.error('Failed to swap drivers:', err);
        showRouteError('Failed to swap drivers: ' + err.message);
    }
}

/**
 * Resets routes to the original calculated values
 */
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
        const routeResults = document.getElementById('results-section');
        if (routeResults) {
            if (!response.ok) {
                showRouteError(html);
            } else {
                routeResults.innerHTML = html;
            }
        }
    } catch (err) {
        console.error('Failed to reset routes:', err);
        showRouteError('Failed to reset routes: ' + err.message);
    }
}

/**
 * Adds an unused driver to the routes as an empty route
 */
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
        const routeResults = document.getElementById('results-section');
        if (routeResults) {
            if (!response.ok) {
                showRouteError(html);
            } else {
                routeResults.innerHTML = html;
            }
        }
    } catch (err) {
        console.error('Failed to add driver:', err);
        showRouteError('Failed to add driver: ' + err.message);
    }
}

// ============= Route Copy Functions =============

/**
 * Encodes an address for use in Google Maps URL
 */
function encodeAddressForMaps(address) {
    return encodeURIComponent(address.trim());
}

/**
 * Generates Google Maps directions URL for a route
 */
function generateMapsUrl(instituteAddress, stops) {
    if (!stops || stops.length === 0) {
        return '';
    }

    const addresses = [instituteAddress, ...stops.map(s => s.address)];
    const encodedAddresses = addresses.map(encodeAddressForMaps);

    return `https://www.google.com/maps/dir/${encodedAddresses.join('/')}`;
}

/**
 * Formats a single route for copying
 */
function formatRouteText(activityLocationName, activityLocationAddress, driverName, stops) {
    let text = `Activity Location: ${activityLocationName}\n${activityLocationAddress}\n\n`;
    text += `Driver: ${driverName}\n`;

    stops.forEach((stop, index) => {
        text += `${index + 1}. ${stop.name} - ${stop.address}\n`;
    });

    const mapsUrl = generateMapsUrl(activityLocationAddress, stops);
    text += `\nMaps: ${mapsUrl}\n`;

    return text;
}

/**
 * Extracts stop data from a route card
 */
function getStopsFromRouteCard(routeCard) {
    const stopItems = routeCard.querySelectorAll('.stop-item');
    return Array.from(stopItems).map(item => ({
        name: item.dataset.participantName,
        address: item.dataset.participantAddress
    }));
}

/**
 * Copies a single route to clipboard
 */
async function copyRoute(button) {
    const routeCard = button.closest('.route-card');
    const container = routeCard.closest('.routes-container');
    const activityLocationName = container.dataset.activityLocationName;
    const activityLocationAddress = container.dataset.activityLocationAddress;
    const driverName = routeCard.dataset.driverName;
    const stops = getStopsFromRouteCard(routeCard);

    const text = formatRouteText(activityLocationName, activityLocationAddress, driverName, stops);

    try {
        await navigator.clipboard.writeText(text);

        // Show feedback
        const originalText = button.textContent;
        button.textContent = 'Copied!';
        button.classList.add('btn-success');
        button.classList.remove('btn-outline');

        setTimeout(() => {
            button.textContent = originalText;
            button.classList.remove('btn-success');
            button.classList.add('btn-outline');
        }, 2000);
    } catch (err) {
        console.error('Failed to copy route:', err);
        showToast('Failed to copy to clipboard', 'error');
    }
}

/**
 * Copies all routes to clipboard
 */
async function copyAllRoutes() {
    const container = document.querySelector('.routes-container');
    const routeCards = container.querySelectorAll('.route-card');
    if (routeCards.length === 0) {
        return;
    }

    const activityLocationName = container.dataset.activityLocationName;
    const activityLocationAddress = container.dataset.activityLocationAddress;
    let allText = `Activity Location: ${activityLocationName}\n${activityLocationAddress}\n\n`;

    routeCards.forEach((routeCard, cardIndex) => {
        const driverName = routeCard.dataset.driverName;
        const stops = getStopsFromRouteCard(routeCard);

        if (cardIndex > 0) {
            allText += '\n';
        }

        allText += `Driver: ${driverName}\n`;
        stops.forEach((stop, index) => {
            allText += `${index + 1}. ${stop.name} - ${stop.address}\n`;
        });

        const mapsUrl = generateMapsUrl(activityLocationAddress, stops);
        allText += `Maps: ${mapsUrl}\n`;
    });

    try {
        await navigator.clipboard.writeText(allText);

        // Show feedback
        const button = document.getElementById('copy-all-btn');
        const originalText = button.textContent;
        button.textContent = 'Copied!';
        button.classList.add('btn-success');
        button.classList.remove('btn-secondary');

        setTimeout(() => {
            button.textContent = originalText;
            button.classList.remove('btn-success');
            button.classList.add('btn-secondary');
        }, 2000);
    } catch (err) {
        console.error('Failed to copy all routes:', err);
        showToast('Failed to copy to clipboard', 'error');
    }
}
