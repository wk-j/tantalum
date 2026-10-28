import { h } from "../lib/dom.js";

// ChipGroup is a row of checkbox chips, one per option.
export class ChipGroup {
  constructor() {
    this.el = h("div", { class: "chips" });
  }

  // set replaces the options; only the option equal to checked starts checked.
  set(options, checked) {
    this.el.replaceChildren(
      ...options.map((value) =>
        h("label", { class: "chip" }, h("input", { type: "checkbox", value, checked: value === checked }), value),
      ),
    );
  }

  values() {
    return [...this.el.querySelectorAll("input:checked")].map((input) => input.value);
  }
}
