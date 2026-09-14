// Paging keeps only selected IDs locally, never rows or a choice catalog.
(() => {
    const selections = new Map();
    const timers = new Map();
    const originalCount = window.updateBulkSelectionCount;
    const originalClear = window.clearTableSelection;
    const originalFilter = window.filterTable;
    const formFor = kind => document.querySelector(`[data-roster-kind="${kind}"]`);
    const selectedFor = kind => {
        if (!selections.has(kind)) selections.set(kind, new Set());
        return selections.get(kind);
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
