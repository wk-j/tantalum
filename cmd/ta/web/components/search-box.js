import { h, icon } from "../lib/dom.js";

// SearchBox is a filter field. Pressing shortcut focuses it, unless the user
// is typing in another control or a dialog is open.
export function SearchBox({ placeholder, label, shortcut = "/", oninput }) {
  const input = h("input", {
    type: "search",
    class: "input search-input",
    placeholder,
    "aria-label": label,
    spellcheck: false,
    autocapitalize: "off",
    autocomplete: "off",
    oninput,
  });
  document.addEventListener("keydown", (event) => {
    const typing = event.target.closest("input, textarea, select");
    if (event.key === shortcut && !typing && !document.querySelector("dialog[open]")) {
      event.preventDefault();
      input.focus();
    }
  });
  const el = h("label", { class: "search" }, icon("search"), input, h("kbd", { "aria-hidden": "true", textContent: shortcut }));
  return { el, input };
}
