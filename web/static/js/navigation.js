(function () {
    const toggle = document.querySelector('.nav-toggle');
    const navigation = document.getElementById('primary-navigation');
    const backdrop = document.querySelector('.nav-backdrop');
    const closeButton = document.querySelector('.nav-close');
    if (!toggle || !navigation || !backdrop || !closeButton) return;
    const main = document.querySelector('.app-main');
    const narrow = window.matchMedia('(max-width: 1100px)');
    function close(returnFocus = false) {
        toggle.setAttribute('aria-expanded', 'false');
        navigation.classList.remove('is-open');
        document.body.classList.remove('nav-open');
        backdrop.hidden = true;
        if (main) main.inert = false;
        if (returnFocus) toggle.focus();
    }
    toggle.addEventListener('click', () => {
        if (toggle.getAttribute('aria-expanded') === 'true') { close(true); return; }
        toggle.setAttribute('aria-expanded', 'true');
        navigation.classList.add('is-open');
        document.body.classList.add('nav-open');
        backdrop.hidden = false;
        if (main) main.inert = true;
        closeButton.focus();
    });
    closeButton.addEventListener('click', () => close(true));
    backdrop.addEventListener('click', () => close(true));
    narrow.addEventListener('change', () => close());
    document.addEventListener('keydown', event => {
        if (toggle.getAttribute('aria-expanded') !== 'true') return;
        if (event.key === 'Escape') { close(true); return; }
        if (event.key !== 'Tab') return;
        const items = Array.from(navigation.querySelectorAll('a[href], button:not([hidden])'))
            .filter(item => item.getClientRects().length);
        const first = items[0], last = items[items.length - 1];
        if (event.shiftKey && document.activeElement === first) {
            event.preventDefault(); last.focus();
        } else if (!event.shiftKey && document.activeElement === last) {
            event.preventDefault(); first.focus();
        }
    });
})();
