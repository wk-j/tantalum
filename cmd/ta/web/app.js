// The web UI of `ta serve`, built from native ES modules (no build step):
//
//   lib/         DOM helper and icons, API client, vault listing store,
//                formatting, .env parser
//   components/  reusable pieces (button, dialog, fields, matrix, …), each
//                with its stylesheet, imported in style.css
//   features/    one dialog per task, composed from components; each takes
//                the `app` context below and returns its open functions
//
// This file builds the page and wires the features to it. To extend the UI,
// add a component (.js plus .css, imported in style.css) or a feature.
import { createClient, takeToken } from "./lib/api.js";
import { h } from "./lib/dom.js";
import { plural } from "./lib/format.js";
import { Store } from "./lib/store.js";
import { Button } from "./components/button.js";
import { Callout } from "./components/callout.js";
import { EmptyState } from "./components/empty-state.js";
import { SecretMatrix, matrixLegend } from "./components/matrix.js";
import { PageHeader } from "./components/page-header.js";
import { SearchBox } from "./components/search-box.js";
import { StatusBanner } from "./components/status.js";
import { Topbar } from "./components/topbar.js";
import { createEnvDialog } from "./features/env-dialog.js";
import { createImportDialog } from "./features/import-dialog.js";
import { createNameDialog } from "./features/name-dialog.js";
import { createSecretDialog } from "./features/secret-dialog.js";

const token = takeToken();
const client = createClient(token);
const store = new Store(client);
const status = new StatusBanner();

// attempt runs a user action and reports its error. It reloads the vault on
// error, since a multi-step action may have stopped half way.
async function attempt(action) {
  try {
    await action();
  } catch (err) {
    status.show(err.message, true);
    store.load().catch(() => {});
  }
}

// ---- page -------------------------------------------------------------------

const ImportButton = () => Button({ label: "Import .env", icon: "upload", onclick: () => importDialog.open() });
const AddButton = () =>
  Button({ label: "Add secret", icon: "plus", variant: "primary", onclick: () => secretDialog.openAdd() });

const search = SearchBox({ placeholder: "Filter secrets", label: "Filter secrets by name", oninput: render });
const topbar = new Topbar({ tools: [search.el, ImportButton(), AddButton()] });
const header = new PageHeader({ title: "Secrets", legend: matrixLegend() });
const empty = EmptyState({
  icon: "lock",
  title: "No secrets yet",
  text: ["Add a secret, or import an existing ", h("code", { textContent: ".env" }), " file so it no longer has to live in the repository."],
  actions: [ImportButton(), AddButton()],
});
const matrix = new SecretMatrix({
  onEnv: (env) => envDialog.open(env),
  onName: (name) => nameDialog.open(name),
  onSet: (env, name) => secretDialog.openEdit(env, name),
  onGap: (env, name) => secretDialog.openAdd({ name, env }),
});
const noMatch = h("p", { class: "no-match", textContent: "No secret matches the filter.", hidden: true });
const envList = h("datalist", { id: "env-list" }); // suggestions for EnvInput
empty.hidden = true;

document.body.append(
  topbar.el,
  h(
    "main",
    { class: "page" },
    header.el,
    status.el,
    empty,
    matrix.el,
    noMatch,
    Callout(
      { icon: "lock" },
      "Values are write-only: this page adds, replaces, copies, renames and deletes secrets, but never shows a value. " +
        "Click a cell to replace or delete that value, a name to rename it, an environment to copy, rename or delete it. " +
        "Agents use a secret with ",
      h("code", { textContent: "ta run -e ENV -s NAME -- …" }),
      ".",
    ),
  ),
  envList,
);

// ---- features ---------------------------------------------------------------

const app = { store, client, status, attempt };
const secretDialog = createSecretDialog(app);
const nameDialog = createNameDialog(app);
const envDialog = createEnvDialog(app);
const importDialog = createImportDialog(app);

function render() {
  const filter = search.input.value.trim().toLowerCase();
  const shown = store.names.filter((name) => name.toLowerCase().includes(filter));
  const any = store.secrets.length > 0;
  empty.hidden = any;
  matrix.el.hidden = shown.length === 0;
  noMatch.hidden = !any || shown.length > 0;
  header.showLegend(shown.length > 0);
  header.setSummary(
    any && `${plural(store.names.length, "secret")} in ${plural(store.envs.length, "environment")}`,
    store.gaps > 0 && [
      h("span", { class: "sep", "aria-hidden": "true", textContent: "·" }),
      h("span", { class: "gaps", textContent: `${store.gaps} not set` }),
    ],
  );
  envList.replaceChildren(...store.envs.map((env) => new Option(env)));
  matrix.render(store, shown);
}
store.subscribe(render);

async function showVault() {
  const vault = await client.vault();
  topbar.setVault(vault);
  if (vault.root) document.title = `${vault.root.split("/").pop()} · tantalum`;
}

if (token) {
  Promise.all([showVault(), store.load()]).catch((err) => status.show(err.message, true));
  // The CLI may change the vault while the page is open.
  window.addEventListener("focus", () => store.load().catch((err) => status.show(err.message, true)));
} else {
  for (const control of document.querySelectorAll("button, input")) control.disabled = true;
  status.show("No access token: open the URL printed by `ta serve`.", true);
}
