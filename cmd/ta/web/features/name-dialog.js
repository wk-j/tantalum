import { h } from "../lib/dom.js";
import { Button } from "../components/button.js";
import { confirmAction } from "../components/confirm.js";
import { CancelButton, Dialog, Spacer } from "../components/dialog.js";
import { Field, Hint, NameInput } from "../components/fields.js";

// createNameDialog renames a secret, or deletes it, in every environment.
export function createNameDialog({ store, client, status, attempt }) {
  let current = null;

  const summary = h("p", { class: "muted" });
  const target = NameInput({ required: true });
  const dialog = new Dialog({
    title: "Secret",
    body: [
      summary,
      Field("New name", target),
      Hint("Renames the secret in every environment. Commands and agents that use the old name stop finding it."),
    ],
    actions: [
      Button({ label: "Delete everywhere", icon: "trash", variant: "danger", onclick: removeName }),
      Spacer(),
      CancelButton(),
      Button({ label: "Rename", type: "submit", variant: "primary" }),
    ],
    onSubmit: rename,
  });

  function open(name) {
    current = name;
    const where = store.envsWith(name);
    const missing = store.envs.filter((env) => !where.includes(env));
    dialog.setTitle(name);
    summary.textContent = `Set in ${where.join(", ")}.` + (missing.length ? ` Not set in ${missing.join(", ")}.` : "");
    dialog.open(target);
    target.value = name;
    target.select();
  }

  function rename() {
    attempt(async () => {
      const from = current;
      const to = target.value.trim();
      if (to === from) return dialog.close();
      await client.renameName(from, to);
      dialog.close();
      status.show(`Renamed ${from} to ${to} in ${store.envsWith(from).join(", ")}.`);
      return store.load();
    });
  }

  function removeName() {
    attempt(async () => {
      const name = current;
      const where = store.envsWith(name);
      if (!(await confirmAction(`Delete ${name} everywhere?`, `Removes ${name} from ${where.join(", ")}.`))) return;
      await client.deleteName(name);
      dialog.close();
      status.show(`Deleted ${name} from ${where.join(", ")}.`);
      return store.load();
    });
  }

  return { open };
}
