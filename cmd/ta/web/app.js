"use strict";

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
const statusEl = document.getElementById("status");
const form = document.getElementById("secret-form");
const envInput = document.getElementById("env");
const envList = document.getElementById("env-list");
const nameInput = document.getElementById("name");
const valueInput = document.getElementById("value");
const envTabs = document.getElementById("envs");
const table = document.getElementById("secrets");
const empty = document.getElementById("empty");
const vaultEl = document.getElementById("vault");

let secrets = []; // [{env, name, updated}] from the API
let currentEnv = envInput.value;

function showStatus(message, isError = false) {
  statusEl.textContent = message;
  statusEl.className = isError ? "error" : "ok";
  statusEl.hidden = false;
}

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

function secretPath(env, name) {
  return `/api/secrets/${encodeURIComponent(env)}/${encodeURIComponent(name)}`;
}

function button(label, onClick, className = "") {
  const b = document.createElement("button");
  b.type = "button";
  b.textContent = label;
  b.className = className;
  b.addEventListener("click", onClick);
  return b;
}

function selectEnv(env) {
  currentEnv = env;
  envInput.value = env;
  render();
}

function row({ env, name, updated }) {
  const nameCell = document.createElement("td");
  const code = document.createElement("code");
  code.textContent = name;
  nameCell.append(code);

  const updatedCell = document.createElement("td");
  updatedCell.textContent = new Date(updated).toLocaleString();

  const actions = document.createElement("td");
  actions.className = "actions";
  actions.append(
    button("Replace", () => {
      envInput.value = env;
      nameInput.value = name;
      valueInput.value = "";
      valueInput.focus();
    }),
    button("Delete", () => remove(env, name), "danger"),
  );

  const tr = document.createElement("tr");
  tr.append(nameCell, updatedCell, actions);
  return tr;
}

function render() {
  const envs = [...new Set(secrets.map((s) => s.env))];
  envTabs.replaceChildren(
    ...envs.map((env) => {
      const tab = button(env, () => selectEnv(env), env === currentEnv ? "tab active" : "tab");
      tab.setAttribute("aria-pressed", String(env === currentEnv));
      return tab;
    }),
  );
  envTabs.hidden = envs.length === 0;
  envList.replaceChildren(...envs.map((env) => new Option(env)));

  const rows = secrets.filter((s) => s.env === currentEnv);
  table.tBodies[0].replaceChildren(...rows.map(row));
  table.hidden = rows.length === 0;
  empty.hidden = rows.length !== 0;
}

async function refresh() {
  secrets = await api("GET", "/api/secrets");
  const envs = new Set(secrets.map((s) => s.env));
  // The shown environment disappears when its last secret is deleted.
  if (envs.size > 0 && !envs.has(currentEnv)) selectEnv(envs.values().next().value);
  render();
}

async function remove(env, name) {
  if (!confirm(`Delete ${env}/${name}? Commands that use it will fail.`)) return;
  try {
    await api("DELETE", secretPath(env, name));
    showStatus(`Deleted ${env}/${name}.`);
    await refresh();
  } catch (err) {
    showStatus(err.message, true);
  }
}

form.addEventListener("submit", async (event) => {
  event.preventDefault();
  const env = envInput.value.trim();
  const name = nameInput.value.trim();
  // Drop one trailing newline left by pasting, as `ta set` does for stdin.
  const value = valueInput.value.replace(/\n$/, "");
  try {
    await api("PUT", secretPath(env, name), { value });
    form.reset();
    selectEnv(env);
    showStatus(`Saved ${env}/${name}.`);
    await refresh();
  } catch (err) {
    showStatus(err.message, true);
  }
});

// Several `ta serve` tabs can be open, one per project; name the vault.
async function showVault() {
  const { root, dir } = await api("GET", "/api/vault");
  const name = root.split("/").pop();
  const strong = document.createElement("strong");
  strong.textContent = root ? `Project ${name}` : "Default vault";
  vaultEl.replaceChildren(strong, ` · ${root || dir}`);
  if (root) document.title = `${name} · tantalum`;
}

if (token) {
  Promise.all([showVault(), refresh()]).catch((err) => showStatus(err.message, true));
} else {
  showStatus("No access token: open the URL printed by `ta serve`.", true);
}
