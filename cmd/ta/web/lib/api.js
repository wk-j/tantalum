// takeToken keeps the #token=… from the URL `ta serve` prints for this tab
// only, and removes it from the address bar and history.
export function takeToken() {
  const match = location.hash.match(/token=([A-Za-z0-9_-]+)/);
  if (match) {
    sessionStorage.setItem("ta-token", match[1]);
    history.replaceState(null, "", location.pathname);
  }
  return sessionStorage.getItem("ta-token");
}

const enc = encodeURIComponent;

// createClient wraps the JSON API of `ta serve`. Values only travel to the
// server: no call returns one. revealSecret has the server copy a value to
// this Mac's clipboard once the person at the Mac approves.
export function createClient(token) {
  async function call(method, path, body) {
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

  return {
    vault: () => call("GET", "/api/vault"),
    secrets: () => call("GET", "/api/secrets"),
    putSecret: (env, name, value) => call("PUT", `/api/secrets/${enc(env)}/${enc(name)}`, { value }),
    deleteSecret: (env, name) => call("DELETE", `/api/secrets/${enc(env)}/${enc(name)}`),
    revealSecret: (env, name) => call("POST", `/api/secrets/${enc(env)}/${enc(name)}/reveal`),
    copyEnv: (from, to, overwrite) => call("POST", `/api/envs/${enc(from)}/copy`, { to, overwrite }),
    renameEnv: (from, to) => call("POST", `/api/envs/${enc(from)}/rename`, { to }),
    deleteEnv: (env) => call("DELETE", `/api/envs/${enc(env)}`),
    renameName: (from, to) => call("POST", `/api/names/${enc(from)}/rename`, { to }),
    deleteName: (name) => call("DELETE", `/api/names/${enc(name)}`),
  };
}
