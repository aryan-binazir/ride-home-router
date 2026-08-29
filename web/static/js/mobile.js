(() => {
    document.addEventListener('click', async event => {
        const copyButton = event.target.closest('[data-copy-target]');
        if (copyButton) {
            const source = document.getElementById(copyButton.dataset.copyTarget);
            if (!source) return;
            await navigator.clipboard.writeText(source.value);
            const label = copyButton.textContent;
            copyButton.textContent = 'Copied';
            window.setTimeout(() => { copyButton.textContent = label; }, 1400);
            return;
        }

        const suggestion = event.target.closest('[data-address-suggestion]');
        if (!suggestion) return;
        const results = suggestion.closest('.mobile-address-results');
        const input = results?.previousElementSibling?.querySelector?.('input[name="address"]')
            || results?.previousElementSibling;
        if (input?.matches?.('input[name="address"]')) input.value = suggestion.dataset.addressSuggestion;
        if (results) results.replaceChildren();
    });
})();
