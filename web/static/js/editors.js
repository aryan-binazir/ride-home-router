(() => {
    let generation = 0;
    const requests = new WeakMap();
    const dialog = () => document.getElementById('route-editor-dialog');
    const target = () => document.getElementById('route-editor');
    const sessionId = () => document.querySelector('.routes-container, .mobile-routes')?.dataset.sessionId;
    window.closeRouteEditor = () => {
        generation++;
        dialog()?.close();
        target()?.replaceChildren();
    };
    document.addEventListener('DOMContentLoaded', () => {
        dialog()?.addEventListener('cancel', event => { event.preventDefault(); window.closeRouteEditor(); });
    });
    document.addEventListener('htmx:beforeRequest', event => {
        if (event.detail.target?.id === 'route-editor') generation++;
        const editorRequest = event.detail.target?.id === 'route-editor' || !!event.detail.elt?.closest('#route-editor');
        const path = event.detail.requestConfig?.path || '';
        const routeRequest = path.startsWith('/api/v1/routes/') || path.startsWith('/m/routes/') || !!event.detail.elt?.closest('.mobile-routes, [data-editor-session]');
        if (editorRequest || routeRequest) {
            requests.set(event.detail.xhr, {generation, sessionId: sessionId(), editorRequest, routeRequest});
        }
    });
    function staleTransientEditor(request, swapTarget) {
        const transientChoice = swapTarget?.id === 'route-editor' || swapTarget?.matches?.('.van-assignment-inline') || swapTarget?.id === 'event-activity-location-select';
        return request.editorRequest && transientChoice && request.generation !== generation;
    }
    function staleRouteSession(request) {
        return request.routeRequest && request.sessionId !== sessionId();
    }
    document.addEventListener('htmx:beforeSwap', event => {
        const request = requests.get(event.detail.xhr);
        const swapTarget = event.detail.target;
        if (request && (staleTransientEditor(request, swapTarget) || staleRouteSession(request))) {
            event.detail.shouldSwap = false;
        }
    });
    document.addEventListener('htmx:afterSwap', event => {
        if (event.detail.target?.id === 'event-activity-location-select') {
            document.getElementById('activity-location-native')?.dispatchEvent(new Event('change', {bubbles: true}));
            window.closeRouteEditor(); return;
        }
        if (['participants-list', 'drivers-list'].includes(event.detail.target?.id) && (event.detail.requestConfig?.elt || event.detail.elt)?.closest('[data-catalog-editor]')) {
            window.closeRouteEditor(); return;
        }
        if (event.detail.target?.matches?.('.van-assignment-inline')) {
            const select = document.getElementById(event.detail.target.id)?.querySelector('select');
            if (select?.matches('.org-vehicle-select')) window.updateCapacityDisplay?.(select);
            else {
                window.handleVanAssignmentChange?.();
                select?.dispatchEvent(new Event('change', {bubbles: true}));
            }
            window.closeRouteEditor();
            return;
        }
        if (event.detail.target?.id === 'mobile-route-updates' || event.detail.target?.matches?.('.mobile-routes')) {
            window.closeRouteEditor();
            return;
        }
        if (event.detail.target?.id !== 'route-editor' || !target()?.firstElementChild) return;
        if (!dialog().open) dialog().showModal();
        const heading = target().querySelector('h2');
        if (heading) { heading.tabIndex = -1; heading.focus(); }
    });
})();
