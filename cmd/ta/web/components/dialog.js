import { h, icon } from "../lib/dom.js";
import { Button } from "./button.js";

let nextId = 0;

// Dialog is a modal form: a header (an optional tone icon, the title and,
// unless closable is false, a close button), the body, and a footer of
// actions. Submitting calls onSubmit. Closing resets the form, so typed
// values never linger. Any [data-close] element inside closes it.
export class Dialog {
  constructor({ title = "", icon: iconName, tone, wide = false, closable = true, body = [], actions = [], onSubmit }) {
    const titleId = `dialog-title-${++nextId}`;
    this.titleEl = h("h2", { id: titleId, textContent: title });
    this.form = h(
      "form",
      {
        class: "dialog-form",
        autocomplete: "off",
        onsubmit: (event) => {
          event.preventDefault();
          onSubmit?.();
        },
      },
      h(
        "header",
        { class: "dialog-head" },
        iconName && h("span", { class: tone ? `dialog-icon tone-${tone}` : "dialog-icon" }, icon(iconName)),
        this.titleEl,
        closable && Button({ label: "Close", icon: "x", variant: "icon", close: true }),
      ),
      body,
      h("div", { class: "dialog-actions" }, actions),
    );
    this.el = h("dialog", { class: wide ? "dialog dialog-wide" : "dialog", "aria-labelledby": titleId }, this.form);
    this.el.addEventListener("close", () => this.form.reset());
    this.el.addEventListener("click", (event) => {
      if (event.target.closest("[data-close]")) this.close();
    });
    document.body.append(this.el);
  }

  setTitle(text) {
    this.titleEl.textContent = text;
  }

  // open shows the dialog and moves focus to the given control.
  open(focus) {
    this.el.showModal();
    focus?.focus();
  }

  close(returnValue) {
    this.el.close(returnValue);
  }
}

export const Spacer = () => h("span", { class: "spacer" });

export const CancelButton = () => Button({ label: "Cancel", close: true });
