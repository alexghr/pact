const form = document.querySelector("#new-session-form");

if (form) {
  const model = form.elements.model;
  const effort = form.elements.effort;

  const updateDefaultLabel = () => {
    const defaultEffort = model.selectedOptions[0].dataset.defaultEffort;
    for (const option of effort.options) {
      option.textContent = option.value + (option.value === defaultEffort ? " (default)" : "");
    }
    return defaultEffort;
  };

  updateDefaultLabel();
  model.addEventListener("change", () => {
    effort.value = updateDefaultLabel();
  });
}
