import { h } from "../lib/dom.js";

// StatusBanner reports the outcome of the last action. A success hides after
// six seconds; an error stays until the next message.
export class StatusBanner {
  #timer;

  constructor() {
    this.el = h("p", { class: "status", role: "status", hidden: true });
  }

  show(message, isError = false) {
    clearTimeout(this.#timer);
    this.el.textContent = message;
    this.el.className = isError ? "status status-error" : "status status-ok";
    this.el.hidden = false;
    if (!isError) this.#timer = setTimeout(() => (this.el.hidden = true), 6000);
  }
}
