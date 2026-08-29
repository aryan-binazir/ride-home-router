(() => {
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

    document.addEventListener('click', async event => {
        const copyButton = event.target.closest('[data-copy-target]');
        if (copyButton) {
            const source = document.getElementById(copyButton.dataset.copyTarget);
            if (!source) return;
            const label = copyButton.textContent;
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
