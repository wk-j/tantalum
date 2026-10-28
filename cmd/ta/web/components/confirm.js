import { h } from "../lib/dom.js";
import { Button, setLabel } from "./button.js";
import { CancelButton, Dialog, Spacer } from "./dialog.js";

let dialog;
let message;
let cancel;
let ok;

// confirmAction asks before a destructive or overwriting step and resolves
// true when the user agrees. Focus starts on Cancel.
export function confirmAction(title, text, okLabel = "Delete") {
  if (!dialog) {
    message = h("p");
    cancel = CancelButton();
    ok = Button({ label: okLabel, type: "submit", variant: "danger-solid" });
    dialog = new Dialog({
      icon: "alert",
      tone: "danger",
      closable: false,
      body: [message],
      actions: [Spacer(), cancel, ok],
      onSubmit: () => dialog.close("ok"),
    });
  }
  dialog.setTitle(title);
  message.textContent = text;
  setLabel(ok, okLabel);
  return new Promise((resolve) => {
    dialog.el.addEventListener("close", () => resolve(dialog.el.returnValue === "ok"), { once: true });
    dialog.el.returnValue = "";
    dialog.open(cancel);
  });
}
