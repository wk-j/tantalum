import { ENV_ERROR, ENV_RE, NAME_ERROR, NAME_RE, runCommand } from "../lib/format.js";
import { Button, setLabel } from "../components/button.js";
import { ChipGroup } from "../components/chips.js";
import { CommandBox } from "../components/command-box.js";
import { confirmAction } from "../components/confirm.js";
import { CancelButton, Dialog, Spacer } from "../components/dialog.js";
import { EnvInput, Field, Fieldset, Hint, NameInput, TextArea } from "../components/fields.js";

// createSecretDialog adds a secret to one or more environments, or replaces
// or deletes one stored value.
export function createSecretDialog({ store, client, status, attempt }) {
  let editing = null; // {env, name} while replacing one value; null while adding

  const name = NameInput({ required: true, placeholder: "GITHUB_TOKEN" });
  const command = new CommandBox({ label: "Run command", oncopy: copyCommand });
  const chips = new ChipGroup();
  const newEnv = EnvInput({ list: undefined, placeholder: "new environment, e.g. staging", "aria-label": "New environment" });
  const envs = Fieldset("Environments", chips.el, newEnv);
  const value = TextArea({ required: true, rows: 4, placeholder: "Paste the secret value" });
  const remove = Button({ label: "Delete", icon: "trash", variant: "danger", onclick: removeSecret });
  const submit = Button({ label: "Save", type: "submit", variant: "primary" });

  const dialog = new Dialog({
    title: "Add secret",
    body: [
      Field("Name", name),
      command.el,
      envs,
      Field("Value", value),
      Hint("One trailing newline from a paste is dropped. The value is never shown again."),
    ],
    actions: [remove, Spacer(), CancelButton(), submit],
    onSubmit: save,
  });

  // openAdd starts a new secret; name and env pre-fill it (a gap in the matrix).
  function openAdd({ name: presetName = "", env } = {}) {
    editing = null;
    dialog.setTitle("Add secret");
    name.readOnly = false;
    envs.hidden = false;
    remove.hidden = command.el.hidden = true;
    setLabel(submit, "Save");
    chips.set(store.envs, env ?? (store.envs.length === 1 ? store.envs[0] : undefined));
    dialog.open(presetName ? value : name);
    name.value = presetName;
    newEnv.value = store.envs.length ? "" : "default";
  }

  function openEdit(env, secretName) {
    editing = { env, name: secretName };
    dialog.setTitle(`Replace ${env} / ${secretName}`);
    name.readOnly = true;
    envs.hidden = true;
    remove.hidden = command.el.hidden = false;
    command.set(`${runCommand(env, secretName)}…`);
    setLabel(remove, `Delete from ${env}`);
    setLabel(submit, "Replace");
    dialog.open(value);
    name.value = secretName;
  }

  function save() {
    attempt(async () => {
      // Drop one trailing newline left by pasting, as `ta set` does for stdin.
      const secret = value.value.replace(/\n$/, "");
      if (!secret) throw new Error("Enter a value.");
      if (editing) {
        const { env, name: secretName } = editing;
        await client.putSecret(env, secretName, secret);
        dialog.close();
        status.show(`Replaced ${env}/${secretName}.`);
        return store.load();
      }
      const secretName = name.value.trim();
      if (!NAME_RE.test(secretName)) throw new Error(NAME_ERROR);
      const targets = chips.values();
      const extra = newEnv.value.trim();
      if (extra) {
        if (!ENV_RE.test(extra)) throw new Error(ENV_ERROR);
        if (!targets.includes(extra)) targets.push(extra);
      }
      if (!targets.length) throw new Error("Pick at least one environment.");
      const existing = targets.filter((env) => store.has(env, secretName));
      if (
        existing.length &&
        !(await confirmAction(
          `Replace ${secretName}?`,
          `${secretName} already exists in ${existing.join(", ")}; its value there will be replaced.`,
          "Replace",
        ))
      ) {
        return;
      }
      for (const env of targets) await client.putSecret(env, secretName, secret);
      dialog.close();
      status.show(`Saved ${secretName} in ${targets.join(", ")}.`);
      return store.load();
    });
  }

  function removeSecret() {
    attempt(async () => {
      const { env, name: secretName } = editing;
      const last = store.count(env) === 1;
      const message =
        `Commands that use ${secretName} with -e ${env} will fail.` +
        (last ? ` It is the last secret in ${env}, so the environment disappears too.` : "");
      if (!(await confirmAction(`Delete ${env}/${secretName}?`, message))) return;
      await client.deleteSecret(env, secretName);
      dialog.close();
      status.show(`Deleted ${env}/${secretName}.`);
      return store.load();
    });
  }

  function copyCommand() {
    attempt(async () => {
      const text = runCommand(editing.env, editing.name);
      await navigator.clipboard.writeText(text);
      command.flash();
      status.show(`Copied: ${text}`);
    });
  }

  return { openAdd, openEdit };
}
