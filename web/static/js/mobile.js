(() => {
    function initializeEventDate() {
        const today = new Date();
        const localDate = `${today.getFullYear()}-${String(today.getMonth() + 1).padStart(2, '0')}-${String(today.getDate()).padStart(2, '0')}`;
        document.querySelectorAll('input[type="date"][name="event_date"]').forEach(input => {
            if (input.value === input.defaultValue) input.value = localDate;
        });
    }

    if (document.readyState === 'complete' || document.readyState === 'interactive') {
        initializeEventDate();
    } else {
        document.addEventListener('DOMContentLoaded', initializeEventDate, { once: true });
    }

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
        if (!form.matches?.('#mobile-driver-picker')) return;
        const selected = new Set(
            Array.from(form.querySelectorAll('input[name="driver_ids"]'))
                .filter(input => input.checked)
                .map(input => input.value),
        );
        for (const select of form.querySelectorAll('select[name^="org_vehicle_"]')) {
            const driverID = select.name.slice('org_vehicle_'.length);
            select.disabled = !selected.has(driverID) || !select.value;
        }
    }, true);

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
