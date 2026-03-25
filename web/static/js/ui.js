// Minimal UI helpers (custom selects, etc.) for ride-home-router.

/**
 * Filter table rows based on search query.
 * Rows should have a data-search attribute containing searchable text.
 * @param {HTMLInputElement} input - The search input element
 * @param {string} tbodyId - The ID of the tbody to filter
 */
function filterTable(input, tbodyId) {
  const tbody = document.getElementById(tbodyId);
  if (!tbody) return;

  const query = (input.value || '').trim().toLowerCase();
  const rows = tbody.querySelectorAll('tr[data-search]');

  rows.forEach(row => {
    const haystack = (row.dataset.search || row.textContent || '').toLowerCase();
    row.classList.toggle('hidden', query.length > 0 && !haystack.includes(query));
  });
}

function updateBulkSelectionCount(tbodyId) {
  const tbody = document.getElementById(tbodyId);
  if (!tbody) return;

  const count = tbody.querySelectorAll('input[data-bulk-row]:checked').length;
  const countEl = document.getElementById(tbodyId.replace('-tbody', '-bulk-selected-count'));
  if (countEl) {
    countEl.textContent = String(count);
  }
}

function selectVisibleTableRows(tbodyId, shouldSelect) {
  const tbody = document.getElementById(tbodyId);
  if (!tbody) return;

  tbody.querySelectorAll('tr[data-search]').forEach((row) => {
    if (row.classList.contains('hidden')) return;

    const checkbox = row.querySelector('input[data-bulk-row]');
    if (checkbox) checkbox.checked = !!shouldSelect;
  });

  updateBulkSelectionCount(tbodyId);
}

function clearTableSelection(tbodyId) {
  const tbody = document.getElementById(tbodyId);
  if (!tbody) return;

  tbody.querySelectorAll('input[data-bulk-row]').forEach((checkbox) => {
    checkbox.checked = false;
  });

  updateBulkSelectionCount(tbodyId);
}

/**
 * Toggle event detail expansion in history view.
 * @param {HTMLElement} eventItem - The event item element
 * @param {number} eventId - The event ID
 */
function toggleEventDetail(eventItem, eventId) {
  const detailDiv = document.getElementById('event-detail-' + eventId);
  if (!detailDiv) return;

  if (detailDiv.innerHTML.trim()) {
    // Collapse - clear content
    detailDiv.innerHTML = '';
    eventItem.classList.remove('expanded');
  } else {
    // Expand - fetch content via HTMX
    eventItem.classList.add('expanded');
    htmx.ajax('GET', '/api/v1/events/' + eventId, { target: detailDiv, swap: 'innerHTML' });
  }
}

(function () {
  let confirmDialog = null;
  let confirmResolve = null;
  let confirmLastFocused = null;

  function ensureConfirmDialog() {
    if (confirmDialog) return confirmDialog;

    const overlay = document.createElement("div");
    overlay.className = "confirm-overlay";
    overlay.setAttribute("aria-hidden", "true");
    overlay.innerHTML = `
      <div class="confirm-dialog" role="alertdialog" aria-modal="true" aria-labelledby="confirm-dialog-title" aria-describedby="confirm-dialog-message">
        <div class="confirm-dialog-body">
          <h3 id="confirm-dialog-title" class="confirm-dialog-title">Confirm Action</h3>
          <p id="confirm-dialog-message" class="confirm-dialog-message" data-confirm-message></p>
        </div>
        <div class="confirm-dialog-actions">
          <button type="button" class="btn btn-outline" data-confirm-action="cancel">Cancel</button>
          <button type="button" class="btn btn-danger" data-confirm-action="confirm">Confirm</button>
        </div>
      </div>
    `;

    overlay.addEventListener("click", (e) => {
      if (e.target === overlay) {
        closeConfirmDialog(false);
        return;
      }

      const action = e.target.closest("[data-confirm-action]");
      if (!action) return;

      closeConfirmDialog(action.dataset.confirmAction === "confirm");
    });

    document.addEventListener("keydown", (e) => {
      if (!overlay.classList.contains("is-open")) return;
      if (e.key === "Escape") {
        e.preventDefault();
        closeConfirmDialog(false);
        return;
      }

      if (e.key !== "Tab") return;

      const focusable = Array.from(
        overlay.querySelectorAll('button:not([disabled]), [href], input:not([disabled]), select:not([disabled]), textarea:not([disabled]), [tabindex]:not([tabindex="-1"])')
      ).filter((el) => el.offsetParent !== null);

      if (focusable.length === 0) {
        e.preventDefault();
        return;
      }

      const first = focusable[0];
      const last = focusable[focusable.length - 1];
      const active = document.activeElement;

      if (e.shiftKey) {
        if (active === first || !overlay.contains(active)) {
          e.preventDefault();
          last.focus();
        }
        return;
      }

      if (active === last) {
        e.preventDefault();
        first.focus();
      }
    });

    document.body.appendChild(overlay);
    confirmDialog = overlay;
    return overlay;
  }

  function closeConfirmDialog(confirmed) {
    if (!confirmDialog || !confirmDialog.classList.contains("is-open")) return;

    confirmDialog.classList.remove("is-open");
    confirmDialog.setAttribute("aria-hidden", "true");

    const resolve = confirmResolve;
    confirmResolve = null;

    if (confirmLastFocused && typeof confirmLastFocused.focus === "function") {
      confirmLastFocused.focus();
    }
    confirmLastFocused = null;

    if (resolve) resolve(confirmed);
  }

  function showConfirmDialog(message) {
    const overlay = ensureConfirmDialog();
    const messageEl = overlay.querySelector("[data-confirm-message]");
    const confirmBtn = overlay.querySelector('[data-confirm-action="confirm"]');

    if (messageEl) {
      messageEl.textContent = message || "Are you sure you want to continue?";
    }

    confirmLastFocused = document.activeElement instanceof HTMLElement ? document.activeElement : null;
    overlay.classList.add("is-open");
    overlay.setAttribute("aria-hidden", "false");

    if (confirmBtn) {
      const schedule = typeof requestAnimationFrame === "function" ? requestAnimationFrame : setTimeout;
      schedule(() => confirmBtn.focus(), 0);
    }

    return new Promise((resolve) => {
      confirmResolve = resolve;
    });
  }

  document.addEventListener('change', (event) => {
    const checkbox = event.target;
    if (!(checkbox instanceof HTMLInputElement) || !checkbox.matches('input[data-bulk-row]')) return;

    const tbody = checkbox.closest('tbody');
    if (tbody && tbody.id) {
      updateBulkSelectionCount(tbody.id);
    }
  });

  document.body.addEventListener('htmx:afterSwap', (event) => {
    const target = event.detail && event.detail.target;
    if (!(target instanceof HTMLElement)) return;

    if (target.id === 'participants-list') {
      const searchInput = document.getElementById('participants-management-search');
      if (searchInput) filterTable(searchInput, 'participants-tbody');
      updateBulkSelectionCount('participants-tbody');
      return;
    }

    if (target.id === 'drivers-list') {
      const searchInput = document.getElementById('drivers-management-search');
      if (searchInput) filterTable(searchInput, 'drivers-tbody');
      updateBulkSelectionCount('drivers-tbody');
    }
  });

  function shouldEnhanceSelects() {
    const platform = (navigator.platform || "").toLowerCase();
    const uaPlatform = (navigator.userAgentData && navigator.userAgentData.platform
      ? navigator.userAgentData.platform
      : ""
    ).toLowerCase();
    const ua = (navigator.userAgent || "").toLowerCase();

    // Be conservative: only replace native selects on Linux where the native
    // dropdown popup is often unstyleable and can clash with app themes.
    return platform.includes("linux") || uaPlatform.includes("linux") || ua.includes("linux");
  }

  function closeSelect(container) {
    container.classList.remove("is-open");
    const trigger = container.querySelector(".ui-select-trigger");
    if (trigger) trigger.setAttribute("aria-expanded", "false");
  }

  function openSelect(container) {
    document.querySelectorAll(".ui-select.is-open").forEach((other) => {
      if (other !== container) closeSelect(other);
    });

    container.classList.add("is-open");
    const trigger = container.querySelector(".ui-select-trigger");
    if (trigger) trigger.setAttribute("aria-expanded", "true");

    const menu = container.querySelector(".ui-select-menu");
    if (!menu) return;

    const selected = menu.querySelector(".ui-select-option.is-selected") || menu.querySelector(".ui-select-option");
    if (selected) {
      selected.focus();
      selected.scrollIntoView({ block: "nearest" });
    }
  }

  function syncSelectValue(container) {
    const nativeSelect = container.querySelector(".ui-select-native");
    const menu = container.querySelector(".ui-select-menu");
    if (!nativeSelect || !menu) return;

    const value = nativeSelect.value || "";

    let activeOption = null;
    menu.querySelectorAll(".ui-select-option").forEach((btn) => {
      const isSelected = (btn.dataset.value || "") === value;
      btn.classList.toggle("is-selected", isSelected);
      btn.setAttribute("aria-selected", isSelected ? "true" : "false");
      if (isSelected) activeOption = btn;
    });

    const triggerTitle = container.querySelector(".ui-select-trigger-title");
    const triggerSubtitle = container.querySelector(".ui-select-trigger-subtitle");
    if (activeOption) {
      if (triggerTitle) triggerTitle.textContent = activeOption.dataset.title || activeOption.textContent.trim();
      if (triggerSubtitle) {
        triggerSubtitle.textContent = activeOption.dataset.subtitle || "";
        triggerSubtitle.classList.toggle("hidden", !triggerSubtitle.textContent.trim());
      }
    }
  }

  function setSelectValue(container, value) {
    const nativeSelect = container.querySelector(".ui-select-native");
    if (!nativeSelect) return;

    nativeSelect.value = value;
    nativeSelect.dispatchEvent(new Event("change", { bubbles: true }));
    syncSelectValue(container);
  }

  function initSelect(container) {
    if (!container || container.dataset.uiSelectInit === "true") return;

    const nativeSelect = container.querySelector(".ui-select-native");
    const trigger = container.querySelector(".ui-select-trigger");
    const menu = container.querySelector(".ui-select-menu");
    if (!nativeSelect || !trigger || !menu) return;

    container.dataset.uiSelectInit = "true";
    container.classList.add("is-enhanced");

    // Ensure selected state matches the native select.
    syncSelectValue(container);

    nativeSelect.addEventListener("change", () => {
      const schedule = typeof requestAnimationFrame === "function" ? requestAnimationFrame : setTimeout;
      schedule(() => syncSelectValue(container), 0);
    });

    trigger.addEventListener("click", (e) => {
      e.preventDefault();
      container.classList.contains("is-open") ? closeSelect(container) : openSelect(container);
    });

    trigger.addEventListener("keydown", (e) => {
      if (e.key === "ArrowDown" || e.key === "Enter" || e.key === " ") {
        e.preventDefault();
        openSelect(container);
      }
      if (e.key === "Escape") {
        e.preventDefault();
        closeSelect(container);
      }
    });

    menu.addEventListener("click", (e) => {
      const btn = e.target.closest(".ui-select-option");
      if (!btn) return;
      e.preventDefault();
      setSelectValue(container, btn.dataset.value || "");
      closeSelect(container);
      trigger.focus();
    });

    menu.addEventListener("keydown", (e) => {
      const items = Array.from(menu.querySelectorAll(".ui-select-option"));
      if (items.length === 0) return;

      const currentIndex = items.indexOf(document.activeElement);

      if (e.key === "Escape") {
        e.preventDefault();
        closeSelect(container);
        trigger.focus();
        return;
      }

      if (e.key === "ArrowDown") {
        e.preventDefault();
        const next = items[Math.min(items.length - 1, Math.max(0, currentIndex) + 1)];
        next.focus();
        next.scrollIntoView({ block: "nearest" });
        return;
      }

      if (e.key === "ArrowUp") {
        e.preventDefault();
        const prev = items[Math.max(0, Math.max(0, currentIndex) - 1)];
        prev.focus();
        prev.scrollIntoView({ block: "nearest" });
        return;
      }

      if (e.key === "Home") {
        e.preventDefault();
        items[0].focus();
        items[0].scrollIntoView({ block: "nearest" });
        return;
      }

      if (e.key === "End") {
        e.preventDefault();
        items[items.length - 1].focus();
        items[items.length - 1].scrollIntoView({ block: "nearest" });
        return;
      }

      if (e.key === "Enter" || e.key === " ") {
        const active = document.activeElement;
        if (active && active.classList && active.classList.contains("ui-select-option")) {
          e.preventDefault();
          setSelectValue(container, active.dataset.value || "");
          closeSelect(container);
          trigger.focus();
        }
      }
    });
  }

  function initAll(root) {
    const scope = root || document;
    if (!shouldEnhanceSelects()) return;
    scope.querySelectorAll(".ui-select").forEach(initSelect);
  }

  function initSettingsValidation() {
    const form = document.getElementById("settings-form");
    if (!form || form.dataset.uiValidated === "true") return;
    form.dataset.uiValidated = "true";

    const select = form.querySelector('select[name="selected_activity_location_id"]');
    if (select) {
      select.addEventListener("invalid", (e) => {
        e.preventDefault();
        showToast('Please select an activity location.', 'warning');

        const container = document.getElementById("activity-location-select");
        if (container && container.classList.contains("is-enhanced")) {
          openSelect(container);
          const trigger = container.querySelector(".ui-select-trigger");
          if (trigger) trigger.focus();
        } else {
          select.focus();
        }
      });
    }

    form.addEventListener("submit", (e) => {
      if (select && !select.value) {
        e.preventDefault();
        showToast('Please select an activity location.', 'warning');
      }
    });
  }

  document.addEventListener("click", (e) => {
    const clickedSelect = e.target.closest(".ui-select");
    document.querySelectorAll(".ui-select.is-open").forEach((container) => {
      if (container !== clickedSelect) closeSelect(container);
    });
  });

  function initAddressAutocomplete() {
    const wrappers = document.querySelectorAll(".address-autocomplete-wrapper");

    wrappers.forEach((wrapper) => {
      if (wrapper.dataset.uiAddressInit === "true") return;
      wrapper.dataset.uiAddressInit = "true";

      const input = wrapper.querySelector("input");
      const suggestionsContainer = wrapper.querySelector(".address-suggestions");

      if (!input || !suggestionsContainer) return;

      // Handle suggestion click
      suggestionsContainer.addEventListener("click", (e) => {
        const suggestion = e.target.closest(".address-suggestion");
        if (!suggestion) return;

        const address = suggestion.dataset.address;
        input.value = address;
        suggestionsContainer.innerHTML = "";

        // Trigger change event for form validation (but not input to avoid re-triggering search)
        input.dispatchEvent(new Event("change", { bubbles: true }));
      });

      // Handle Escape key
      input.addEventListener("keydown", (e) => {
        if (e.key === "Escape") {
          suggestionsContainer.innerHTML = "";
          input.blur();
        }
      });

      // Close on click outside wrapper
      document.addEventListener("click", (e) => {
        if (!wrapper.contains(e.target)) {
          suggestionsContainer.innerHTML = "";
        }
      });
    });
  }

  document.addEventListener("DOMContentLoaded", () => {
    ensureConfirmDialog();
    initAll(document);
    initSettingsValidation();
    initAddressAutocomplete();
    updateBulkSelectionCount('participants-tbody');
    updateBulkSelectionCount('drivers-tbody');
  });

  document.addEventListener("htmx:load", (e) => {
    initAll(e.target);
    initSettingsValidation();
    initAddressAutocomplete();
    updateBulkSelectionCount('participants-tbody');
    updateBulkSelectionCount('drivers-tbody');
  });

  document.addEventListener("htmx:confirm", (e) => {
    const question = e.detail && e.detail.question;
    const issueRequest = e.detail && e.detail.issueRequest;

    if (!question || typeof issueRequest !== "function") return;

    e.preventDefault();
    showConfirmDialog(question).then((confirmed) => {
      if (confirmed) issueRequest(true);
    });
  });
})();
