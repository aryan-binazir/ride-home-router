/* Clerk owns session refresh; every backend request independently checks access. */
(async function () {
    const retry = document.getElementById('auth-retry');
    if (retry) retry.addEventListener('click', () => location.reload());
    let recovery;
    function showRecovery(text) {
        if (!recovery) {
            recovery = document.createElement('p');
            recovery.setAttribute('role', 'alert');
            document.body.prepend(recovery);
        }
        recovery.textContent = text + ' ';
        const link = document.createElement('a');
        link.href = '/sign-in';
        link.target = '_blank';
        link.rel = 'noopener';
        link.textContent = 'Sign in in another tab, then retry here.';
        recovery.appendChild(link);
    }
    // Retry only an explicit 401: the backend rejects it before any mutation.
    // Never retry network errors or 5xx, where a mutation might have committed.
    window.authFetch = async function (url, options = {}) {
        if (new URL(url, location.href).origin !== location.origin) {
            throw new Error('Authenticated requests must stay on this site.');
        }
        let response = await fetch(url, options);
        if (response.status === 401 && window.Clerk?.session) {
            let token;
            try {
                token = await window.Clerk.session.getToken({skipCache: true});
            } catch (_) { /* Keep the current edit when refresh is unavailable. */ }
            if (token) {
                const headers = new Headers(options.headers);
                headers.set('Authorization', 'Bearer ' + token);
                response = await fetch(url, {...options, headers});
            }
        }
        if (response.status === 401 || response.status === 403) {
            const text = response.status === 401 ? 'Your session needs refreshing.' : 'Your account does not have access.';
            showRecovery(text);
            throw new Error(text + ' Your change was not saved.');
        }
        return response;
    };
    try {
        const response = await fetch('/auth/config', {cache: 'no-store'});
        if (!response.ok) throw new Error('Authentication unavailable');
        const config = await response.json();
        await new Promise((resolve, reject) => {
            const script = document.createElement('script');
            script.src = config.scriptURL;
            script.crossOrigin = 'anonymous';
            script.dataset.clerkPublishableKey = config.publishableKey;
            script.onload = resolve;
            script.onerror = reject;
            document.head.appendChild(script);
        });
        await window.Clerk.load();
        const readyForms = new WeakSet();
        const pendingForms = new WeakSet();
        document.addEventListener('submit', async event => {
            const form = event.target;
            if (!form.matches('.mobile-shell form[method="post"]')) return;
            if (readyForms.delete(form)) return;
            event.preventDefault();
            event.stopImmediatePropagation();
            if (pendingForms.has(form)) return;
            pendingForms.add(form);
            try {
                // A phone may wake with an expired cookie. Refresh before its
                // plain HTML POST so the browser keeps unsent input on failure.
                const token = await window.Clerk.session?.getToken({skipCache: true});
                if (!token) throw new Error('No active session');
                readyForms.add(form);
                if (event.submitter) form.requestSubmit(event.submitter);
                else form.requestSubmit();
            } catch (_) {
                showRecovery('Unable to refresh your session. Your form has not been submitted.');
            } finally {
                readyForms.delete(form);
                pendingForms.delete(form);
            }
        }, true);
        document.querySelectorAll('[data-sign-out]').forEach(button => {
            button.hidden = !window.Clerk.user;
            button.addEventListener('click', () => window.Clerk.signOut({redirectUrl: '/sign-in'}));
        });
        if (location.pathname !== '/sign-in') return;
        if (window.Clerk.user) {
            const check = await fetch('/api/v1/settings', {cache: 'no-store'});
            if (check.ok) { location.replace('/'); return; }
            if (retry && check.status !== 403) retry.hidden = false;
            return;
        }
        window.Clerk.mountSignIn(document.getElementById('clerk-sign-in'), {
            appearance: {
                variables: {
                    colorPrimary: 'var(--accent)',
                    colorText: 'var(--ink)',
                    colorTextSecondary: 'var(--ink-2)',
                    colorBackground: 'var(--pane)',
                    colorInputBackground: 'var(--field)',
                    colorInputText: 'var(--ink)',
                    colorNeutral: 'var(--ink)',
                    colorDanger: 'var(--danger)',
                    fontFamily: 'inherit',
                    fontSize: '0.9375rem',
                    borderRadius: '0.5rem',
                },
                elements: {
                    rootBox: 'login-clerk-root',
                    cardBox: 'login-clerk-box',
                    card: 'login-clerk-card',
                    headerTitle: 'login-clerk-title',
                    headerSubtitle: 'login-clerk-subtitle',
                    socialButtonsBlockButton: 'login-google-button',
                    footer: 'login-clerk-footer',
                },
            },
            routing: 'hash',
            forceRedirectUrl: '/',
            signUpForceRedirectUrl: '/',
            withSignUp: true,
            transferable: true,
        });
    } catch (_) {
        if (retry) retry.hidden = false;
    }
})();
