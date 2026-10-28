"use strict";

const NAME_RE = /^[A-Za-z_][A-Za-z0-9_]*$/;
const ENV_RE = /^[A-Za-z0-9][A-Za-z0-9_-]*$/;

// `ta serve` prints a URL with #token=…; keep the token for this tab only and
// remove it from the address bar and history.
function takeToken() {
  const match = location.hash.match(/token=([A-Za-z0-9_-]+)/);
  if (match) {
    sessionStorage.setItem("ta-token", match[1]);
    history.replaceState(null, "", location.pathname);
  }
  return sessionStorage.getItem("ta-token");
}

const token = takeToken();
const $ = (id) => document.getElementById(id);
const enc = encodeURIComponent;

let secrets = []; // [{env, name, updated}] from the API
let envs = []; // environment names, sorted
let names = []; // secret names, sorted
const byKey = new Map(); // "env\0name" → entry

const key = (env, name) => `${env}\0${name}`;
const secretPath = (env, name) => `/api/secrets/${enc(env)}/${enc(name)}`;
const plural = (n, word) => `${n} ${word}${n === 1 ? "" : "s"}`;

// ---- API and status ---------------------------------------------------------

async function api(method, path, body) {
  const headers = { Authorization: `Bearer ${token}` };
  if (body !== undefined) headers["Content-Type"] = "application/json";
  const res = await fetch(path, {
    method,
    headers,
    body: body === undefined ? undefined : JSON.stringify(body),
    cache: "no-store",
  });
  if (!res.ok) {
    let message = `${res.status} ${res.statusText}`;
    try {
      message = (await res.json()).error || message;
    } catch {}
    throw new Error(message);
  }
  return res.status === 204 ? null : res.json();
}

let statusTimer;
function showStatus(message, isError = false) {
  const status = $("status");
  clearTimeout(statusTimer);
  status.textContent = message;
  status.className = isError ? "error" : "ok";
  status.hidden = false;
  if (!isError) statusTimer = setTimeout(() => (status.hidden = true), 6000);
}

// attempt runs a user action, reports its error and reloads the vault, since
// a multi-step action may have stopped half way.
async function attempt(action) {
  try {
    await action();
  } catch (err) {
    showStatus(err.message, true);
    refresh().catch(() => {});
  }
}

// ---- DOM helpers ------------------------------------------------------------

function node(tag, props = {}, ...children) {
  const n = document.createElement(tag);
  for (const [k, v] of Object.entries(props)) {
    if (k === "class") n.className = v;
    else if (k.startsWith("on")) n.addEventListener(k.slice(2), v);
    else if (k in n) n[k] = v;
    else n.setAttribute(k, v);
  }
  n.append(...children);
  return n;
}

function ago(iso) {
  const s = (Date.now() - new Date(iso).getTime()) / 1000;
  if (s < 60) return "just now";
  if (s < 3600) return `${Math.floor(s / 60)}m ago`;
  if (s < 86400) return `${Math.floor(s / 3600)}h ago`;
  if (s < 30 * 86400) return `${Math.floor(s / 86400)}d ago`;
  return new Date(iso).toLocaleDateString();
}

function runCommand(env, name) {
  return env === "default" ? `ta run -s ${name} -- ` : `ta run -e ${env} -s ${name} -- `;
}

function openDialog(dialog, focus) {
  dialog.showModal();
  focus?.focus();
}

// Every dialog clears its form when it closes, so typed values never linger.
for (const dialog of document.querySelectorAll("dialog")) {
  dialog.addEventListener("close", () => dialog.querySelector("form").reset());
}
for (const button of document.querySelectorAll("[data-close]")) {
  button.addEventListener("click", () => button.closest("dialog").close());
}

function confirmAction(title, message, ok = "Delete") {
  const dialog = $("confirm-dialog");
  const form = $("confirm-form");
  $("confirm-title").textContent = title;
  $("confirm-message").textContent = message;
  $("confirm-ok").textContent = ok;
  return new Promise((resolve) => {
    const onSubmit = (event) => {
      event.preventDefault();
      dialog.close("ok");
    };
    form.addEventListener("submit", onSubmit);
    dialog.addEventListener(
      "close",
      () => {
        form.removeEventListener("submit", onSubmit);
        resolve(dialog.returnValue === "ok");
      },
      { once: true },
    );
    dialog.returnValue = "";
    openDialog(dialog, dialog.querySelector("[data-close]"));
  });
}

// ---- matrix -----------------------------------------------------------------

async function refresh() {
  secrets = await api("GET", "/api/secrets");
  byKey.clear();
  for (const s of secrets) byKey.set(key(s.env, s.name), s);
  envs = [...new Set(secrets.map((s) => s.env))].sort();
  names = [...new Set(secrets.map((s) => s.name))].sort();
  render();
}

function render() {
  const filter = $("search").value.trim().toLowerCase();
  const shown = names.filter((n) => n.toLowerCase().includes(filter));
  $("empty").hidden = secrets.length > 0;
  $("matrix-wrap").hidden = shown.length === 0;
  $("no-match").hidden = secrets.length === 0 || shown.length > 0;
  $("env-list").replaceChildren(...envs.map((env) => new Option(env)));

  const counts = new Map(envs.map((env) => [env, 0]));
  for (const s of secrets) counts.set(s.env, counts.get(s.env) + 1);

  const matrix = $("matrix");
  matrix.tHead.rows[0].replaceChildren(
    node("th", { class: "corner", scope: "col" }, plural(names.length, "secret")),
    ...envs.map((env) =>
      node(
        "th",
        { scope: "col" },
        node(
          "button",
          { type: "button", class: "env-head", title: `Copy, rename or delete ${env}`, onclick: () => openEnv(env) },
          node("span", { class: "env-name", textContent: env }),
          node("span", { class: "count", textContent: String(counts.get(env)) }),
        ),
      ),
    ),
  );
  matrix.tBodies[0].replaceChildren(
    ...shown.map((name) =>
      node(
        "tr",
        {},
        node(
          "th",
          { scope: "row" },
          node(
            "button",
            { type: "button", class: "name-head", title: `Rename or delete ${name}`, onclick: () => openName(name) },
            node("code", { textContent: name }),
          ),
        ),
        ...envs.map((env) => node("td", {}, cell(env, name))),
      ),
    ),
  );
}

function cell(env, name) {
  const s = byKey.get(key(env, name));
  if (!s) {
    return node(
      "button",
      {
        type: "button",
        class: "cell missing",
        title: `Not set in ${env}: click to add`,
        "aria-label": `Add ${name} to ${env}`,
        onclick: () => openAdd({ name, env }),
      },
      "+",
    );
  }
  return node(
    "button",
    {
      type: "button",
      class: "cell set",
      title: `Updated ${new Date(s.updated).toLocaleString()}: click to replace or delete`,
      "aria-label": `${env}/${name}, updated ${ago(s.updated)}: replace or delete`,
      onclick: () => openEdit(env, name),
    },
    node("span", { class: "dot", "aria-hidden": "true" }),
    node("span", { class: "age", textContent: ago(s.updated) }),
  );
}

// ---- add and replace --------------------------------------------------------

const secretDialog = $("secret-dialog");
let editing = null; // {env, name} while replacing one value; null while adding

function openAdd({ name = "", env } = {}) {
  editing = null;
  $("secret-title").textContent = "Add secret";
  $("secret-name").readOnly = false;
  $("secret-envs").hidden = false;
  $("secret-delete").hidden = $("secret-copy-command").hidden = true;
  $("secret-submit").textContent = "Save";
  const preset = env ?? (envs.length === 1 ? envs[0] : undefined);
  $("secret-env-options").replaceChildren(
    ...envs.map((e) =>
      node("label", { class: "chip" }, node("input", { type: "checkbox", value: e, checked: e === preset }), e),
    ),
  );
  openDialog(secretDialog, name ? $("secret-value") : $("secret-name"));
  $("secret-name").value = name;
  $("secret-new-env").value = envs.length ? "" : "default";
}

function openEdit(env, name) {
  editing = { env, name };
  $("secret-title").textContent = `Replace ${env} / ${name}`;
  $("secret-name").readOnly = true;
  $("secret-envs").hidden = true;
  $("secret-delete").hidden = $("secret-copy-command").hidden = false;
  $("secret-delete").textContent = `Delete from ${env}`;
  $("secret-submit").textContent = "Replace";
  openDialog(secretDialog, $("secret-value"));
  $("secret-name").value = name;
}

$("secret-form").addEventListener("submit", (event) => {
  event.preventDefault();
  attempt(async () => {
    // Drop one trailing newline left by pasting, as `ta set` does for stdin.
    const value = $("secret-value").value.replace(/\n$/, "");
    if (!value) throw new Error("Enter a value.");
    if (editing) {
      const { env, name } = editing;
      await api("PUT", secretPath(env, name), { value });
      secretDialog.close();
      showStatus(`Replaced ${env}/${name}.`);
      return refresh();
    }
    const name = $("secret-name").value.trim();
    if (!NAME_RE.test(name)) throw new Error("Names use letters, digits and _, not starting with a digit.");
    const targets = [...$("secret-env-options").querySelectorAll("input:checked")].map((i) => i.value);
    const extra = $("secret-new-env").value.trim();
    if (extra) {
      if (!ENV_RE.test(extra)) throw new Error("Environments use letters, digits, _ and -, starting with a letter or digit.");
      if (!targets.includes(extra)) targets.push(extra);
    }
    if (!targets.length) throw new Error("Pick at least one environment.");
    const existing = targets.filter((env) => byKey.has(key(env, name)));
    if (
      existing.length &&
      !(await confirmAction(
        `Replace ${name}?`,
        `${name} already exists in ${existing.join(", ")}; its value there will be replaced.`,
        "Replace",
      ))
    ) {
      return;
    }
    for (const env of targets) await api("PUT", secretPath(env, name), { value });
    secretDialog.close();
    showStatus(`Saved ${name} in ${targets.join(", ")}.`);
    return refresh();
  });
});

$("secret-delete").addEventListener("click", () =>
  attempt(async () => {
    const { env, name } = editing;
    const last = secrets.filter((s) => s.env === env).length === 1;
    const message = `Commands that use ${name} with -e ${env} will fail.` +
      (last ? ` It is the last secret in ${env}, so the environment disappears too.` : "");
    if (!(await confirmAction(`Delete ${env}/${name}?`, message))) return;
    await api("DELETE", secretPath(env, name));
    secretDialog.close();
    showStatus(`Deleted ${env}/${name}.`);
    return refresh();
  }),
);

$("secret-copy-command").addEventListener("click", () =>
  attempt(async () => {
    const command = runCommand(editing.env, editing.name);
    await navigator.clipboard.writeText(command);
    showStatus(`Copied: ${command}`);
  }),
);

// ---- rename and delete a name -----------------------------------------------

const nameDialog = $("name-dialog");
let currentName = null;

const envsWith = (name) => envs.filter((env) => byKey.has(key(env, name)));

function openName(name) {
  currentName = name;
  const where = envsWith(name);
  const missing = envs.filter((env) => !where.includes(env));
  $("name-title").textContent = name;
  $("name-summary").textContent =
    `Set in ${where.join(", ")}.` + (missing.length ? ` Not set in ${missing.join(", ")}.` : "");
  openDialog(nameDialog, $("name-target"));
  $("name-target").value = name;
  $("name-target").select();
}

$("name-form").addEventListener("submit", (event) => {
  event.preventDefault();
  attempt(async () => {
    const from = currentName;
    const to = $("name-target").value.trim();
    if (to === from) return nameDialog.close();
    await api("POST", `/api/names/${enc(from)}/rename`, { to });
    nameDialog.close();
    showStatus(`Renamed ${from} to ${to} in ${envsWith(from).join(", ")}.`);
    return refresh();
  });
});

$("name-delete").addEventListener("click", () =>
  attempt(async () => {
    const name = currentName;
    const where = envsWith(name);
    if (!(await confirmAction(`Delete ${name} everywhere?`, `Removes ${name} from ${where.join(", ")}.`))) return;
    await api("DELETE", `/api/names/${enc(name)}`);
    nameDialog.close();
    showStatus(`Deleted ${name} from ${where.join(", ")}.`);
    return refresh();
  }),
);

// ---- environments -----------------------------------------------------------

const envDialog = $("env-dialog");
let currentEnv = null;

function openEnv(env) {
  currentEnv = env;
  const count = secrets.filter((s) => s.env === env).length;
  $("env-title").textContent = `Environment ${env}`;
  $("env-summary").textContent =
    `${plural(count, "secret")}. Copy them into another environment (new or existing), rename ${env}, or delete it.`;
  openDialog(envDialog, $("env-target"));
}

function envTarget() {
  const input = $("env-target");
  const to = input.value.trim();
  if (!input.reportValidity()) return null;
  if (to === currentEnv) {
    showStatus(`Pick an environment other than ${currentEnv}.`, true);
    return null;
  }
  return to;
}

$("env-form").addEventListener("submit", (event) => {
  event.preventDefault();
  attempt(async () => {
    const from = currentEnv;
    const to = envTarget();
    if (!to) return;
    const { copied, skipped } = await api("POST", `/api/envs/${enc(from)}/copy`, {
      to,
      overwrite: $("env-overwrite").checked,
    });
    envDialog.close();
    showStatus(
      `Copied ${plural(copied.length, "secret")} from ${from} to ${to}.` +
        (skipped.length ? ` Kept ${skipped.length} that already existed: ${skipped.join(", ")}.` : ""),
    );
    return refresh();
  });
});

$("env-rename").addEventListener("click", () =>
  attempt(async () => {
    const from = currentEnv;
    const to = envTarget();
    if (!to) return;
    await api("POST", `/api/envs/${enc(from)}/rename`, { to });
    envDialog.close();
    showStatus(`Renamed environment ${from} to ${to}. Commands that use -e ${from} must use -e ${to}.`);
    return refresh();
  }),
);

$("env-delete").addEventListener("click", () =>
  attempt(async () => {
    const env = currentEnv;
    const count = secrets.filter((s) => s.env === env).length;
    const message = `Deletes its ${plural(count, "secret")}. Commands run with -e ${env} will fail.`;
    if (!(await confirmAction(`Delete environment ${env}?`, message))) return;
    await api("DELETE", `/api/envs/${enc(env)}`);
    envDialog.close();
    showStatus(`Deleted environment ${env}.`);
    return refresh();
  }),
);

// ---- import .env ------------------------------------------------------------

const importDialog = $("import-dialog");
let parsed = { entries: new Map(), errors: [] };

// parseDotenv reads KEY=VALUE lines: blank lines and # comments are skipped,
// `export ` is allowed, "double" quotes understand \n \r \t \" \\ and may span
// lines, 'single' quotes are literal, and an unquoted value ends at " #".
// A later line wins over an earlier one with the same name.
function parseDotenv(text) {
  const entries = new Map();
  const errors = [];
  const lines = text.replace(/\r\n?/g, "\n").split("\n");
  for (let i = 0; i < lines.length; i++) {
    const lineNo = i + 1;
    let line = lines[i].trim();
    if (!line || line.startsWith("#")) continue;
    if (line.startsWith("export ")) line = line.slice(7).trimStart();
    const eq = line.indexOf("=");
    if (eq < 1) {
      errors.push(`Line ${lineNo}: expected NAME=VALUE.`);
      continue;
    }
    const name = line.slice(0, eq).trim();
    if (!NAME_RE.test(name)) {
      errors.push(`Line ${lineNo}: ${name} is not a valid name.`);
      continue;
    }
    const rest = line.slice(eq + 1).trimStart();
    const quote = rest[0];
    let value;
    if (quote === '"' || quote === "'") {
      let body = rest.slice(1);
      let end = closingQuote(body, quote);
      while (end < 0 && i + 1 < lines.length) {
        body += "\n" + lines[++i];
        end = closingQuote(body, quote);
      }
      if (end < 0) {
        errors.push(`Line ${lineNo}: ${name} has no closing ${quote}.`);
        continue;
      }
      value = body.slice(0, end);
      if (quote === '"') {
        value = value.replace(/\\([nrt"\\])/g, (_, c) => ({ n: "\n", r: "\r", t: "\t" })[c] ?? c);
      }
    } else {
      const comment = rest.search(/\s#/);
      value = (comment >= 0 ? rest.slice(0, comment) : rest).trimEnd();
    }
    if (!value) {
      errors.push(`Line ${lineNo}: ${name} is empty and is skipped.`);
      continue;
    }
    entries.set(name, value);
  }
  return { entries, errors };
}

function closingQuote(s, quote) {
  for (let i = 0; i < s.length; i++) {
    if (quote === '"' && s[i] === "\\") i++;
    else if (s[i] === quote) return i;
  }
  return -1;
}

function importPlan() {
  const env = $("import-env").value.trim();
  const overwrite = $("import-overwrite").checked;
  return [...parsed.entries.keys()].map((name) => ({
    name,
    action: !byKey.has(key(env, name)) ? "new" : overwrite ? "replace" : "keep",
  }));
}

function renderImport() {
  const plan = importPlan();
  const todo = plan.filter((p) => p.action !== "keep");
  const labels = { new: "new", replace: "replace", keep: "keep existing" };
  $("import-preview").replaceChildren(
    ...(plan.length
      ? [
          node(
            "ul",
            { class: "plan" },
            ...plan.map((p) =>
              node("li", {}, node("code", { textContent: p.name }), node("span", { class: `badge ${p.action}`, textContent: labels[p.action] })),
            ),
          ),
        ]
      : []),
    ...parsed.errors.map((e) => node("p", { class: "problem", textContent: e })),
  );
  const submit = $("import-submit");
  submit.disabled = todo.length === 0;
  submit.textContent = todo.length ? `Import ${plural(todo.length, "secret")}` : "Import";
}

function openImport() {
  parsed = { entries: new Map(), errors: [] };
  openDialog(importDialog, $("import-text"));
  $("import-env").value = envs.length && !envs.includes("default") ? envs[0] : "default";
  renderImport();
}

$("import-text").addEventListener("input", () => {
  parsed = parseDotenv($("import-text").value);
  renderImport();
});
$("import-env").addEventListener("input", renderImport);
$("import-overwrite").addEventListener("change", renderImport);
$("import-file").addEventListener("change", () =>
  attempt(async () => {
    const input = $("import-file");
    const file = input.files[0];
    if (!file) return;
    $("import-text").value = await file.text();
    input.value = "";
    parsed = parseDotenv($("import-text").value);
    renderImport();
  }),
);

$("import-form").addEventListener("submit", (event) => {
  event.preventDefault();
  attempt(async () => {
    const env = $("import-env").value.trim();
    if (!ENV_RE.test(env)) throw new Error("Environments use letters, digits, _ and -, starting with a letter or digit.");
    const plan = importPlan();
    const todo = plan.filter((p) => p.action !== "keep");
    let done = 0;
    try {
      for (const p of todo) {
        await api("PUT", secretPath(env, p.name), { value: parsed.entries.get(p.name) });
        done++;
      }
    } catch (err) {
      throw new Error(`Imported ${done} of ${todo.length} into ${env}, then: ${err.message}`);
    }
    importDialog.close();
    parsed = { entries: new Map(), errors: [] };
    const kept = plan.length - todo.length;
    showStatus(`Imported ${plural(done, "secret")} into ${env}.` + (kept ? ` Kept ${kept} that already existed.` : ""));
    return refresh();
  });
});

// ---- page wiring ------------------------------------------------------------

async function showVault() {
  const { root, dir } = await api("GET", "/api/vault");
  const name = root.split("/").pop();
  $("vault").replaceChildren(node("strong", { textContent: root ? `Project ${name}` : "Default vault" }), ` · ${root || dir}`);
  if (root) document.title = `${name} · tantalum`;
}

$("search").addEventListener("input", render);
$("add-button").addEventListener("click", () => openAdd());
$("import-button").addEventListener("click", openImport);
for (const button of document.querySelectorAll("[data-open]")) {
  button.addEventListener("click", () => (button.dataset.open === "import" ? openImport() : openAdd()));
}
document.addEventListener("keydown", (event) => {
  const typing = event.target.closest("input, textarea, select");
  if (event.key === "/" && !typing && !document.querySelector("dialog[open]")) {
    event.preventDefault();
    $("search").focus();
  }
});

if (token) {
  Promise.all([showVault(), refresh()]).catch((err) => showStatus(err.message, true));
  // The CLI may change the vault while the page is open.
  window.addEventListener("focus", () => refresh().catch((err) => showStatus(err.message, true)));
} else {
  for (const control of document.querySelectorAll("button, input")) control.disabled = true;
  showStatus("No access token: open the URL printed by `ta serve`.", true);
}
