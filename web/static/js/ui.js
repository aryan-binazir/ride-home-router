// Minimal UI helpers (custom selects, etc.) for ride-home-router.

(function () {
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

  function setSelectValue(container, value) {
    const nativeSelect = container.querySelector(".ui-select-native");
    const menu = container.querySelector(".ui-select-menu");
    if (!nativeSelect || !menu) return;

    nativeSelect.value = value;
    nativeSelect.dispatchEvent(new Event("change", { bubbles: true }));

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

  function initSelect(container) {
    if (!container || container.dataset.uiSelectInit === "true") return;

    const nativeSelect = container.querySelector(".ui-select-native");
    const trigger = container.querySelector(".ui-select-trigger");
    const menu = container.querySelector(".ui-select-menu");
    if (!nativeSelect || !trigger || !menu) return;

    container.dataset.uiSelectInit = "true";
    container.classList.add("is-enhanced");

    // Ensure selected state matches the native select.
    setSelectValue(container, nativeSelect.value || "");

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
    scope.querySelectorAll(".ui-select").forEach(initSelect);
  }

  function initSettingsValidation() {
    const form = document.getElementById("settings-form");
    if (!form || form.dataset.uiValidated === "true") return;
    form.dataset.uiValidated = "true";

    form.addEventListener("submit", (e) => {
      const select = form.querySelector('select[name="selected_activity_location_id"]');
      if (select && !select.value) {
        e.preventDefault();
        const result = document.getElementById("settings-result");
        if (result) {
          result.innerHTML = '<div class="alert alert-warning">Please select an activity location.</div>';
          result.scrollIntoView({ block: "nearest", behavior: "smooth" });
        }
      }
    });
  }

  document.addEventListener("click", (e) => {
    const clickedSelect = e.target.closest(".ui-select");
    document.querySelectorAll(".ui-select.is-open").forEach((container) => {
      if (container !== clickedSelect) closeSelect(container);
    });
  });

  document.addEventListener("DOMContentLoaded", () => {
    initAll(document);
    initSettingsValidation();
  });

  document.addEventListener("htmx:load", (e) => {
    initAll(e.target);
    initSettingsValidation();
  });
})();

