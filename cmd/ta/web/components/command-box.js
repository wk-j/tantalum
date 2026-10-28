import { h, icon } from "../lib/dom.js";
import { Button } from "./button.js";

// CommandBox shows a shell command on one line with a copy button; oncopy
// runs when the button is pressed and should call flash() once copied.
export class CommandBox {
  #timer;

  constructor({ label, oncopy }) {
    this.code = h("code");
    this.copyButton = Button({ label: `Copy ${label.toLowerCase()}`, icon: "copy", variant: "icon", onclick: oncopy });
    this.el = h(
      "div",
      { class: "field", role: "group", "aria-label": label },
      h("span", { class: "field-label", textContent: label }),
      h("div", { class: "command" }, icon("terminal"), this.code, this.copyButton),
    );
  }

  set(command) {
    this.code.textContent = command;
  }

  // flash marks the copy button as done for a moment.
  flash() {
    clearTimeout(this.#timer);
    this.copyButton.classList.add("copied");
    this.#timer = setTimeout(() => this.copyButton.classList.remove("copied"), 1500);
  }
}
