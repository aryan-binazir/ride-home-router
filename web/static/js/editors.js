// One transient server-rendered editor. No catalogs or application data cache.
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
    window.restorePlannerLocation = async id => {
        const response = await (window.authFetch || fetch)('/api/v1/planner/location-editor', {method: 'POST', headers: {'HX-Request': 'true', 'Content-Type': 'application/x-www-form-urlencoded'}, body: new URLSearchParams({location_id: id}), signal: AbortSignal.timeout(15000)});
        if (!response.ok) return false;
        const template = document.createElement('template'); template.innerHTML = await response.text();
        const replacement = template.content.querySelector('#event-activity-location-select');
        const previous = document.getElementById('event-activity-location-select');
        if (!replacement || !previous) return false;
        previous.replaceWith(replacement); htmx.process(replacement); return true;
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
    document.addEventListener('htmx:beforeSwap', event => {
        const request = requests.get(event.detail.xhr);
        const target = event.detail.target;
        // Closing/replacing an editor cancels transient choices, not a route
        // mutation already committed by the server. Session ownership still applies.
        const transient = target?.id === 'route-editor' || target?.matches?.('.van-assignment-inline') || target?.id === 'event-activity-location-select';
        if (request && ((transient && request.editorRequest && request.generation !== generation) || (request.routeRequest && request.sessionId !== sessionId()))) {
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
        // Focus the title on paging/search rather than stealing a typed query.
        const heading = target().querySelector('h2');
        if (heading) { heading.tabIndex = -1; heading.focus(); }
    });
})();
