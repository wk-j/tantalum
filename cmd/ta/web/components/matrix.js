import { h, icon, setChildren } from "../lib/dom.js";
import { ago } from "../lib/format.js";

// SecretMatrix is a table of secret names (rows) against environments
// (columns). Every header and cell is a button: onEnv(env), onName(name),
// onSet(env, name) for a stored secret and onGap(env, name) for a missing one.
// Gaps stay visible: without fallback (ADR 0004) a gap is a command that fails
// in that environment.
export class SecretMatrix {
  constructor({ onEnv, onName, onSet, onGap }) {
    this.handlers = { onEnv, onName, onSet, onGap };
    this.headRow = h("tr");
    this.body = h("tbody");
    this.el = h(
      "div",
      { class: "matrix-wrap", hidden: true },
      h("table", { class: "matrix" }, h("thead", {}, this.headRow), this.body),
    );
  }

  // render shows the given names (already filtered) in every environment of store.
  render(store, names) {
    const { onEnv, onName } = this.handlers;
    setChildren(
      this.headRow,
      h("th", { class: "corner", scope: "col", textContent: "Secret" }),
      store.envs.map((env) =>
        h(
          "th",
          { scope: "col" },
          h(
            "button",
            { type: "button", class: "env-head", title: `Copy, rename or delete ${env}`, onclick: () => onEnv(env) },
            h("span", { class: "env-name", textContent: env }),
            h("span", { class: "count", textContent: String(store.count(env)) }),
            icon("more"),
          ),
        ),
      ),
    );
    setChildren(
      this.body,
      ...names.map((name) =>
        h(
          "tr",
          {},
          h(
            "th",
            { scope: "row" },
            h(
              "button",
              { type: "button", class: "name-head", title: `Rename or delete ${name}`, onclick: () => onName(name) },
              h("code", { textContent: name }),
              icon("pencil"),
            ),
          ),
          store.envs.map((env) => h("td", {}, this.#cell(store.get(env, name), env, name))),
        ),
      ),
    );
  }

  #cell(secret, env, name) {
    const { onSet, onGap } = this.handlers;
    if (!secret) {
      return h(
        "button",
        {
          type: "button",
          class: "cell cell-gap",
          title: `Not set in ${env}: click to add`,
          "aria-label": `Add ${name} to ${env}`,
          onclick: () => onGap(env, name),
        },
        h(
          "span",
          { class: "gap" },
          icon("plus"),
          h("span", { class: "gap-label", textContent: "Not set" }),
          h("span", { class: "add-label", textContent: "Add" }),
        ),
      );
    }
    return h(
      "button",
      {
        type: "button",
        class: "cell cell-set",
        title: `Updated ${new Date(secret.updated).toLocaleString()}: click to replace or delete`,
        "aria-label": `${env}/${name}, updated ${ago(secret.updated)}: replace or delete`,
        onclick: () => onSet(env, name),
      },
      h("span", { class: "dot", "aria-hidden": "true" }),
      h("span", { class: "age", textContent: ago(secret.updated) }),
    );
  }
}

// matrixLegend explains the two kinds of cell, for PageHeader.
export const matrixLegend = () => [
  [h("span", { class: "dot", "aria-hidden": "true" }), "Set"],
  [h("span", { class: "gap-swatch", "aria-hidden": "true" }), "Not set"],
];
