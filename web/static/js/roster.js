(() => {
    const selectedIds = new Map();
    const timers = new Map();
    const originalCount = window.updateBulkSelectionCount;
    const originalClear = window.clearTableSelection;
    const originalFilter = window.filterTable;
    const formFor = kind => document.querySelector(`[data-roster-kind="${kind}"]`);
    const selectedFor = kind => {
        if (!selectedIds.has(kind)) selectedIds.set(kind, new Set());
        return selectedIds.get(kind);
    };
    function capture(kind) {
        const selected = selectedFor(kind);
        formFor(kind)?.querySelectorAll('input[data-bulk-row]').forEach(input => {
            if (input.checked) selected.add(input.value); else selected.delete(input.value);
        });
        return selected;
    }
    window.updateBulkSelectionCount = tbodyId => {
        const kind = tbodyId.replace('-tbody', '');
        const form = formFor(kind);
        if (!form) return originalCount(tbodyId);
        const selected = capture(kind);
        const visible = new Set(Array.from(form.querySelectorAll('input[data-bulk-row]'), input => input.value));
        const hidden = form.querySelector('[data-roster-selected]');
        hidden.replaceChildren();
        for (const id of selected) {
            if (visible.has(id)) continue;
            const input = document.createElement('input'); input.type = 'checkbox'; input.name = kind === 'drivers' ? 'driver_ids' : 'participant_ids'; input.value = id; input.checked = true; hidden.append(input);
        }
        const count = document.getElementById(`${kind}-bulk-selected-count`);
        if (count) count.textContent = String(selected.size);
    };
    window.clearTableSelection = tbodyId => {
        const kind = tbodyId.replace('-tbody', '');
        selectedFor(kind).clear();
        originalClear(tbodyId);
        window.updateBulkSelectionCount(tbodyId);
    };
    window.filterTable = (input, tbodyId) => {
        const kind = tbodyId.replace('-tbody', '');
        const form = formFor(kind);
        if (!form) return originalFilter(input, tbodyId);
        const applied = form.querySelector('[data-roster-page]')?.dataset.searchQuery || '';
        if (input.value.trim() === applied) return;
        clearTimeout(timers.get(kind));
        timers.set(kind, setTimeout(() => {
            timers.delete(kind);
            htmx.ajax('GET', `/api/v1/${kind}?${new URLSearchParams({search: input.value.trim()})}`, {source: form, target: `#${kind}-list`, swap: 'innerHTML'});
        }, 250));
    };
    document.addEventListener('htmx:configRequest', event => {
        const target = event.detail.target;
        const kind = target?.id?.replace('-list', '');
        const form = formFor(kind);
        if (!form || !['participants-list', 'drivers-list'].includes(target.id)) return;
        const input = document.getElementById(`${kind}-search`);
        event.detail.parameters.search = input?.value || '';
        if (event.detail.verb !== 'get') event.detail.parameters.offset = form.querySelector('[data-roster-page]')?.dataset.offset || '0';
    });
    document.addEventListener('htmx:beforeSwap', event => {
        const detail = event.detail;
        if (!detail.shouldSwap || !['participants-list', 'drivers-list'].includes(detail.target?.id)) return;
        const kind = detail.target.id.replace('-list', '');
        if (!formFor(kind)) return;
        const selected = capture(kind);
        if (detail.requestConfig?.verb === 'delete') {
            const id = detail.requestConfig.path.match(/\/(\d+)$/)?.[1];
            if (id) selected.delete(id);
        }
        const response = new DOMParser().parseFromString(detail.serverResponse, 'text/html');
        response.querySelectorAll('input[data-bulk-row]').forEach(input => input.toggleAttribute('checked', selected.has(input.value)));
        detail.serverResponse = response.body.innerHTML;
    });
})();

(() => {
    if (typeof document === 'undefined') return;
    let dialog = null;
    let marker = null;
    const copy = {
        guessed: {title: 'Address needs a look', note: "Google couldn't find this exactly, so this is its closest guess."},
        confirmed: {title: 'Address confirmed', note: "You confirmed Google's match for this address."},
        verified: {title: 'Address verified', note: 'Google matched this address exactly.'},
    };
    function ensureDialog() {
        if (dialog) return dialog;
        dialog = document.createElement('dialog');
        dialog.className = 'address-match-dialog';
        dialog.setAttribute('aria-labelledby', 'address-match-title');
        dialog.innerHTML = `
      <div class="address-match-body">
        <h3 id="address-match-title" class="address-match-title"></h3>
        <p class="address-match-line" data-address-entered><span>You entered</span><strong data-address-entered-value></strong></p>
        <p class="address-match-line"><span data-address-matched-label>We matched it to</span><strong data-address-matched-value></strong></p>
        <p class="address-match-note" data-address-note></p>
      </div>
      <div class="address-match-actions">
        <button type="button" class="btn btn-outline" data-address-action="close">Close</button>
        <button type="button" class="btn btn-outline" data-address-action="fix">Fix address</button>
        <button type="button" class="btn btn-primary" data-address-action="confirm">Looks right</button>
      </div>`;
        dialog.addEventListener('click', event => {
            const action = event.target.closest('[data-address-action]')?.dataset.addressAction;
            if (action === 'close') dialog.close();
            else if (action === 'fix') fixAddress();
            else if (action === 'confirm') confirmAddress();
            else if (event.target === dialog) dialog.close();
        });
        document.body.append(dialog);
        return dialog;
    }
    function fixAddress() {
        dialog.close();
        document.getElementById(marker.dataset.row)?.querySelector('button[hx-get$="/edit"]')?.click();
    }
    function confirmAddress() {
        dialog.close();
        const {kind, id} = marker.dataset;
        htmx.ajax('POST', `/api/v1/${kind}/${id}/address/confirm`, {source: marker, target: `#${kind}-list`, swap: 'innerHTML'});
    }
    function open(button) {
        marker = button;
        const box = ensureDialog();
        const match = copy[button.dataset.match] ? button.dataset.match : 'verified';
        const guessed = match === 'guessed';
        const matched = button.dataset.matched || button.dataset.address;
        box.querySelector('#address-match-title').textContent = copy[match].title;
        box.querySelector('#address-match-title').classList.toggle('is-guessed', guessed);
        box.querySelector('[data-address-entered]').hidden = !guessed;
        box.querySelector('[data-address-entered-value]').textContent = button.dataset.address;
        box.querySelector('[data-address-matched-label]').textContent = guessed ? 'We matched it to' : 'Address';
        box.querySelector('[data-address-matched-value]').textContent = matched;
        box.querySelector('[data-address-note]').textContent = copy[match].note;
        box.querySelector('[data-address-action="close"]').hidden = guessed;
        box.querySelector('[data-address-action="fix"]').hidden = !guessed;
        box.querySelector('[data-address-action="confirm"]').hidden = !guessed;
        box.showModal();
    }
    document.addEventListener('click', event => {
        const button = event.target.closest('[data-address-marker]');
        if (button) open(button);
    });
})();
