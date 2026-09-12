/* Clerk owns session refresh; every backend request independently checks access. */
(async function () {
    const message = document.getElementById('auth-message');
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
        document.querySelectorAll('[data-sign-out]').forEach(button => {
            button.hidden = !window.Clerk.user;
            button.addEventListener('click', () => window.Clerk.signOut({redirectUrl: '/sign-in'}));
        });
        if (location.pathname !== '/sign-in') return;
        if (window.Clerk.user) {
            const check = await fetch('/api/v1/settings', {cache: 'no-store'});
            if (check.ok) { location.replace('/'); return; }
            message.textContent = check.status === 403
                ? 'Your account is not approved. Contact an administrator or sign out to use another account.'
                : 'Unable to verify your access. Try again or sign out.';
            return;
        }
        window.Clerk.mountSignIn(document.getElementById('clerk-sign-in'), {
            routing: 'hash',
            forceRedirectUrl: '/',
            withSignUp: false,
            transferable: false,
        });
    } catch (_) {
        if (message) message.textContent = 'Sign in is unavailable. Please try again later.';
    }
})();
