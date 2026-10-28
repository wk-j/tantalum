const key = (env, name) => `${env}\0${name}`;

// Store holds the vault listing the API returns: environment, name and update
// time of every secret, never a value. Subscribers run after each load.
export class Store {
  secrets = []; // [{env, name, updated}]
  envs = []; // environment names, sorted
  names = []; // secret names, sorted
  #byKey = new Map();
  #listeners = new Set();

  constructor(client) {
    this.client = client;
  }

  async load() {
    this.secrets = await this.client.secrets();
    this.#byKey = new Map(this.secrets.map((s) => [key(s.env, s.name), s]));
    this.envs = [...new Set(this.secrets.map((s) => s.env))].sort();
    this.names = [...new Set(this.secrets.map((s) => s.name))].sort();
    for (const listener of this.#listeners) listener(this);
  }

  subscribe(listener) {
    this.#listeners.add(listener);
    return () => this.#listeners.delete(listener);
  }

  get(env, name) {
    return this.#byKey.get(key(env, name));
  }

  has(env, name) {
    return this.#byKey.has(key(env, name));
  }

  // envsWith lists the environments that hold name.
  envsWith(name) {
    return this.envs.filter((env) => this.has(env, name));
  }

  count(env) {
    return this.secrets.filter((s) => s.env === env).length;
  }

  // gaps counts name/environment pairs without a secret.
  get gaps() {
    return this.names.length * this.envs.length - this.secrets.length;
  }
}
