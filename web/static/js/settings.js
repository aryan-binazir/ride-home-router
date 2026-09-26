(function () {
    async function loadSettings() {
        try {
            const sections = [
                ['google-key-slot', '/api/v1/settings/google-maps-key'],
                ['access-slot', '/api/v1/access'],
                ['admins-slot', '/api/v1/access/admins'],
            ];
            const html = await Promise.all(sections.map(async ([, url]) => {
                const response = await window.authFetch(url, {
                    headers: {'HX-Request': 'true'},
                    signal: AbortSignal.timeout(15000),
                });
                if (!response.ok) throw new Error('Settings unavailable');
                return response.text();
            }));
            sections.forEach(([id], index) => {
                document.getElementById(id).innerHTML = html[index];
                htmx.process(document.getElementById(id));
            });
            document.documentElement.classList.remove('settings-restoring');
        } catch (_) {
            document.documentElement.classList.add('settings-load-failed');
        }
    }
    if (document.readyState !== 'complete') document.addEventListener('DOMContentLoaded', loadSettings);
    else loadSettings();
})();
