import { h } from "../lib/dom.js";
import { plural } from "../lib/format.js";
import { Button } from "../components/button.js";
import { confirmAction } from "../components/confirm.js";
import { CancelButton, Dialog, Spacer } from "../components/dialog.js";
import { CheckField, Checkbox, EnvInput, Field } from "../components/fields.js";

// createEnvDialog copies an environment's secrets into another environment,
// renames the environment, or deletes it.
export function createEnvDialog({ store, client, status, attempt }) {
  let current = null;

  const summary = h("p", { class: "muted" });
  const target = EnvInput({ required: true, placeholder: "e.g. staging" });
  const overwrite = Checkbox();
  const dialog = new Dialog({
    title: "Environment",
    body: [
      summary,
      Field("Target environment", target),
      CheckField("When copying, replace secrets that already exist in the target", overwrite),
    ],
    actions: [
      Button({ label: "Delete environment", icon: "trash", variant: "danger", onclick: removeEnv }),
      Spacer(),
      CancelButton(),
      Button({ label: "Rename", onclick: rename }),
      Button({ label: "Copy secrets", type: "submit", variant: "primary" }),
    ],
    onSubmit: copy,
  });

  function open(env) {
    current = env;
    dialog.setTitle(`Environment ${env}`);
    summary.textContent =
      `${plural(store.count(env), "secret")}. Copy them into another environment (new or existing), ` +
      `rename ${env}, or delete it.`;
    dialog.open(target);
  }

  // targetEnv returns the typed environment, or null after telling the user
  // why it cannot be used.
  function targetEnv() {
    const to = target.value.trim();
    if (!target.reportValidity()) return null;
    if (to === current) {
      status.show(`Pick an environment other than ${current}.`, true);
      return null;
    }
    return to;
  }

  function copy() {
    attempt(async () => {
      const from = current;
      const to = targetEnv();
      if (!to) return;
      const { copied, skipped } = await client.copyEnv(from, to, overwrite.checked);
      dialog.close();
      status.show(
        `Copied ${plural(copied.length, "secret")} from ${from} to ${to}.` +
          (skipped.length ? ` Kept ${skipped.length} that already existed: ${skipped.join(", ")}.` : ""),
      );
      return store.load();
    });
  }

  function rename() {
    attempt(async () => {
      const from = current;
      const to = targetEnv();
      if (!to) return;
      await client.renameEnv(from, to);
      dialog.close();
      status.show(`Renamed environment ${from} to ${to}. Commands that use -e ${from} must use -e ${to}.`);
      return store.load();
    });
  }

  function removeEnv() {
    attempt(async () => {
      const env = current;
      const message = `Deletes its ${plural(store.count(env), "secret")}. Commands run with -e ${env} will fail.`;
      if (!(await confirmAction(`Delete environment ${env}?`, message))) return;
      await client.deleteEnv(env);
      dialog.close();
      status.show(`Deleted environment ${env}.`);
      return store.load();
    });
  }

  return { open };
}
