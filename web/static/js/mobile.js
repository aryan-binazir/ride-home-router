(() => {
    let selectedSeats;
    let visibleSeats = 0;
    function initializePage() {
        captureSeatCount();
        const today = new Date();
        const localDate = `${today.getFullYear()}-${String(today.getMonth() + 1).padStart(2, '0')}-${String(today.getDate()).padStart(2, '0')}`;
        document.querySelectorAll('input[type="date"][name="event_date"]').forEach(input => {
            if (input.value === input.defaultValue) input.value = localDate;
        });
    }

    if (document.readyState === 'complete' || document.readyState === 'interactive') {
        initializePage();
    } else {
        document.addEventListener('DOMContentLoaded', initializePage, { once: true });
    }

    let failedElement;
    function showRequestError(message, type = 'error') {
        const alert = document.getElementById('mobile-request-error');
        if (!alert) return;
        alert.setAttribute('role', type === 'success' ? 'status' : 'alert');
        alert.className = type === 'success' ? 'mobile-notice' : 'mobile-alert';
        alert.textContent = message;
        alert.hidden = false;
    }
    document.addEventListener('showToast', event => {
        if (event.detail?.message) showRequestError(event.detail.message, event.detail.type);
    });
    for (const name of ['htmx:sendError', 'htmx:timeout', 'htmx:responseError']) {
        document.addEventListener(name, event => {
            const xhr = event.detail?.xhr;
            if (name === 'htmx:responseError' && xhr?.getResponseHeader?.('HX-Trigger')) return;
            failedElement = event.detail?.elt;
            showRequestError(name !== 'htmx:responseError'
                ? 'Could not reach the server. Check your connection and try again.'
                : ({413: 'That file is too large. Choose a smaller file and try again.',
                    403: 'You no longer have access. Sign in again.',
                    503: 'The service is temporarily unavailable. Try again in a minute.'}[xhr?.status]
                    || 'An error occurred. Please try again.'));
        });
    }
    document.addEventListener('htmx:afterRequest', event => {
        if (!event.detail?.successful || !failedElement || event.detail.elt !== failedElement) return;
        const alert = document.getElementById('mobile-request-error');
        if (alert) alert.hidden = true;
        failedElement = null;
    });
    const submitting = new Set();
    const confirmed = new WeakSet();
    document.addEventListener('auth:submitFailed', event => confirmed.delete(event.target));
    document.addEventListener('submit', async event => {
        const form = event.target;
        if (event.defaultPrevented || !form.matches?.('.mobile-shell form[method="post"]')) return;
        if (submitting.has(form)) { event.preventDefault(); return; }
        const confirmation = form.getAttribute('data-confirm');
        if (confirmation && !confirmed.delete(form)) {
            event.preventDefault();
            if (await window.showConfirmDialog(confirmation)) {
                confirmed.add(form);
                if (event.submitter) form.requestSubmit(event.submitter);
                else form.requestSubmit();
            }
            return;
        }
        submitting.add(form);
        const action = form.getAttribute('action');
        form.querySelectorAll('button[type="submit"]').forEach(button => {
            if (button.disabled) return;
            button.dataset.submitLabel = button.textContent;
            // Retain successful-control data when a submitter has a name.
            if (button === event.submitter && button.name) {
                const input = document.createElement('input');
                input.type = 'hidden'; input.name = button.name; input.value = button.value;
                input.dataset.submitValue = 'true'; form.appendChild(input);
            }
            button.disabled = true;
            button.textContent = action === '/m/calculate' ? 'Calculating…'
                : action === '/m/routes/save' ? 'Saving…' : action?.startsWith('/m/routes/') ? 'Updating…' : 'Saving…';
        });
    });
    window.addEventListener?.('pageshow', () => {
        for (const form of submitting) {
            form.querySelectorAll('[data-submit-label]').forEach(button => {
                button.disabled = false; button.textContent = button.dataset.submitLabel;
                delete button.dataset.submitLabel;
            });
            form.querySelectorAll('[data-submit-value]').forEach(input => input.remove());
            form.querySelectorAll('select[data-submit-disabled]').forEach(select => {
                select.disabled = select.dataset.submitDisabled === 'true';
                delete select.dataset.submitDisabled;
            });
        }
        submitting.clear();
    });

    function countVisibleSeats(defaults = false) {
        let total = 0;
        const vans = new Set(Array.from(document.querySelectorAll?.('#mobile-driver-picker input[type="hidden"][name^="org_vehicle_"]') || [], input => input.value));
        document.querySelectorAll?.('#mobile-driver-picker .mobile-driver-choice').forEach(row => {
            const checkbox = row.querySelector('input[name="driver_ids"]');
            if (!(defaults ? checkbox?.defaultChecked : checkbox?.checked)) return;
            const select = row.querySelector('select');
            const option = defaults ? Array.from(select?.options || []).find(option => option.defaultSelected) || select?.options[0] : select?.selectedOptions[0];
            const van = option?.value;
            if (van && vans.has(van)) {
                total += Number(checkbox.dataset.capacity || 0);
                return;
            }
            if (van) vans.add(van);
            total += Number(option?.dataset.capacity || checkbox.dataset.capacity || 0);
        });
        return total;
    }
    function captureSeatCount() {
        const count = document.getElementById?.('mobile-selected-seats');
        if (!count) return;
        visibleSeats = countVisibleSeats();
        selectedSeats ??= Number(count.dataset.seats) + visibleSeats - countVisibleSeats(true);
        count.textContent = `${selectedSeats} seat${selectedSeats === 1 ? '' : 's'} selected`;
    }
    document.addEventListener('htmx:afterSwap', captureSeatCount);
    document.addEventListener('change', event => {
        if (!event.target.closest?.('#mobile-driver-picker')) return;
        const count = document.getElementById?.('mobile-selected-seats');
        if (!count) return;
        const next = countVisibleSeats();
        selectedSeats = (selectedSeats ?? Number(count.dataset.seats)) + next - visibleSeats;
        visibleSeats = next;
        count.textContent = `${selectedSeats} seat${selectedSeats === 1 ? '' : 's'} selected`;
    });

    async function copyText(source) {
        if (navigator.clipboard?.writeText) {
            try {
                await navigator.clipboard.writeText(source.value);
                return true;
            } catch (_) {
                // Plain HTTP and denied permissions fall through to the legacy copy path.
            }
        }

        const wasHidden = source.hidden === true;
        try {
            if (wasHidden) {
                source.hidden = false;
                source.style.position = 'fixed';
                source.style.opacity = '0';
            }
            source.select();
            return document.execCommand('copy');
        } catch (_) {
            return false;
        } finally {
            if (wasHidden) {
                source.hidden = true;
                source.style.removeProperty('position');
                source.style.removeProperty('opacity');
            }
        }
    }

    // Filter responses carry request-time selections. Merge the current form
    // immediately before swapping so edits made during the request survive.
    document.addEventListener('htmx:beforeSwap', event => {
        const detail = event.detail;
        const form = detail.target?.closest?.('#mobile-rider-picker, #mobile-driver-picker');
        if (!form || !detail.shouldSwap) return;
        const response = new DOMParser().parseFromString(detail.serverResponse, 'text/html');
        const results = response.getElementById(detail.target.id);
        if (!results) return;
        const name = form.id === 'mobile-rider-picker' ? 'participant_ids' : 'driver_ids';
        const current = new FormData(form);
        const selected = new Set(current.getAll(name));
        results.querySelectorAll('input[type="hidden"]').forEach(input => input.remove());
        const visible = new Set();
        results.querySelectorAll(`input[name="${name}"]`).forEach(input => {
            visible.add(input.value);
            input.toggleAttribute('checked', selected.has(input.value));
        });
        results.querySelectorAll('select[name^="org_vehicle_"]').forEach(select => {
            const id = select.name.slice('org_vehicle_'.length);
            const value = selected.has(id) ? current.get(select.name) || '' : '';
            Array.from(select.options).forEach(option => option.toggleAttribute('selected', option.value === value));
        });
        for (const id of selected) {
            if (visible.has(id)) continue;
            const input = response.createElement('input');
            input.type = 'hidden';
            input.name = name;
            input.value = id;
            results.append(input);
            const vehicle = current.get(`org_vehicle_${id}`);
            if (name === 'driver_ids' && vehicle) {
                const assignment = response.createElement('input');
                assignment.type = 'hidden';
                assignment.name = `org_vehicle_${id}`;
                assignment.value = vehicle;
                results.append(assignment);
            }
        }
        detail.serverResponse = results.outerHTML;
    });

    document.addEventListener('change', event => {
        const checkbox = event.target;
        if (checkbox.matches?.('input[name="mode"]')) {
            const pickup = checkbox.value === 'pickup';
            const label = pickup ? 'Arrive at activity location by' : 'Depart activity location at';
            document.getElementById('route-time-label').textContent = label;
            document.getElementById('route-time-help').textContent = pickup
                ? 'Used to back-calculate the expected arrival time at each stop in copied driver and parent lists.'
                : 'Used to calculate the expected arrival time at each stop in copied driver and parent lists.';
            checkbox.closest('form')?.querySelector('input[name="route_time"]')?.setAttribute('aria-label', label);
            return;
        }
        if (!checkbox.matches?.('input[name="driver_ids"]') || checkbox.checked) return;
        const select = checkbox.closest('.mobile-driver-choice')?.querySelector('select[name^="org_vehicle_"]');
        if (select) select.value = '';
    });

    document.addEventListener('submit', event => {
        const form = event.target;
        if (event.defaultPrevented || !form.matches?.('#mobile-driver-picker')) return;
        const selected = new Set(
            Array.from(form.querySelectorAll('input[name="driver_ids"]'))
                .filter(input => input.checked)
                .map(input => input.value),
        );
        for (const select of form.querySelectorAll('select[name^="org_vehicle_"]')) {
            const driverID = select.name.slice('org_vehicle_'.length);
            select.dataset.submitDisabled = String(select.disabled);
            select.disabled = !selected.has(driverID) || !select.value;
        }
    });

    document.addEventListener('click', async event => {
        const copyButton = event.target.closest('[data-copy-target]');
        if (copyButton) {
            const source = document.getElementById(copyButton.dataset.copyTarget);
            if (!source) return;
            copyButton.dataset.copyOriginalLabel ||= copyButton.textContent;
            const label = copyButton.dataset.copyOriginalLabel;
            copyButton.textContent = await copyText(source) ? 'Copied' : 'Copy failed';
            window.setTimeout(() => { copyButton.textContent = label; }, 1400);
            return;
        }

        const suggestion = event.target.closest('[data-address-suggestion], [data-address]');
        if (!suggestion) return;
        const results = suggestion.closest('.mobile-address-results');
        const input = results?.previousElementSibling?.querySelector?.('input[name="address"]')
            || results?.previousElementSibling;
        if (input?.matches?.('input[name="address"]')) {
            input.value = suggestion.dataset.addressSuggestion || suggestion.dataset.address;
        }
        if (results) results.replaceChildren();
    });
})();
