import { h, setChildren } from "../lib/dom.js";
import { parseDotenv } from "../lib/dotenv.js";
import { DEFAULT_ENV, ENV_ERROR, ENV_RE, plural } from "../lib/format.js";
import { Button, FileButton, setLabel } from "../components/button.js";
import { CancelButton, Dialog, Spacer } from "../components/dialog.js";
import { CheckField, Checkbox, EnvInput, Field, TextArea } from "../components/fields.js";

const LABELS = { new: "new", replace: "replace", keep: "keep existing" };

// createImportDialog imports a .env file, pasted or chosen from disk, into
// one environment, previewing which names are new, replaced or kept.
export function createImportDialog({ store, client, status, attempt }) {
  let parsed = { entries: new Map(), errors: [] };

  const env = EnvInput({ required: true });
  const text = TextArea({
    rows: 9,
    placeholder: 'DB_URL=postgres://…\nexport API_KEY="…"   # comments are fine',
    oninput: reparse,
  });
  const overwrite = Checkbox({ onchange: renderPreview });
  const preview = h("div", { class: "preview", "aria-live": "polite" });
  const submit = Button({ label: "Import", type: "submit", variant: "primary", disabled: true });
  env.addEventListener("input", renderPreview);

  const dialog = new Dialog({
    title: "Import .env",
    wide: true,
    body: [
      h(
        "div",
        { class: "row" },
        Field("Environment", env, { grow: true }),
        FileButton({ label: "Choose file…", accept: ".env,.txt,text/plain", onchange: readFile }),
      ),
      Field("Contents", text),
      CheckField("Replace secrets that already exist in this environment", overwrite),
      preview,
    ],
    actions: [Spacer(), CancelButton(), submit],
    onSubmit: importAll,
  });

  function open() {
    parsed = { entries: new Map(), errors: [] };
    dialog.open(text);
    env.value = store.envs.length && !store.envs.includes(DEFAULT_ENV) ? store.envs[0] : DEFAULT_ENV;
    renderPreview();
  }

  function plan() {
    const target = env.value.trim();
    return [...parsed.entries.keys()].map((name) => ({
      name,
      action: !store.has(target, name) ? "new" : overwrite.checked ? "replace" : "keep",
    }));
  }

  function reparse() {
    parsed = parseDotenv(text.value);
    renderPreview();
  }

  function renderPreview() {
    const steps = plan();
    const todo = steps.filter((step) => step.action !== "keep");
    setChildren(
      preview,
      steps.length > 0 &&
        h(
          "ul",
          { class: "plan" },
          steps.map((step) =>
            h(
              "li",
              {},
              h("code", { textContent: step.name }),
              h("span", { class: `badge badge-${step.action}`, textContent: LABELS[step.action] }),
            ),
          ),
        ),
      parsed.errors.map((error) => h("p", { class: "problem", textContent: error })),
    );
    submit.disabled = todo.length === 0;
    setLabel(submit, todo.length ? `Import ${plural(todo.length, "secret")}` : "Import");
  }

  function readFile(event) {
    attempt(async () => {
      const input = event.target;
      const file = input.files[0];
      if (!file) return;
      text.value = await file.text();
      input.value = "";
      reparse();
    });
  }

  function importAll() {
    attempt(async () => {
      const target = env.value.trim();
      if (!ENV_RE.test(target)) throw new Error(ENV_ERROR);
      const steps = plan();
      const todo = steps.filter((step) => step.action !== "keep");
      let done = 0;
      try {
        for (const step of todo) {
          await client.putSecret(target, step.name, parsed.entries.get(step.name));
          done++;
        }
      } catch (err) {
        throw new Error(`Imported ${done} of ${todo.length} into ${target}, then: ${err.message}`);
      }
      dialog.close();
      parsed = { entries: new Map(), errors: [] };
      const kept = steps.length - todo.length;
      status.show(`Imported ${plural(done, "secret")} into ${target}.` + (kept ? ` Kept ${kept} that already existed.` : ""));
      return store.load();
    });
  }

  return { open };
}
