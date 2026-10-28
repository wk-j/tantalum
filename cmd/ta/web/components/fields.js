import { h } from "../lib/dom.js";
import { ENV_PATTERN, ENV_RULE, NAME_PATTERN, NAME_RULE } from "../lib/format.js";

// Secret names, environments and values are typed exactly: no spellcheck,
// no autocapitalisation.
const exact = { spellcheck: false, autocapitalize: "off" };

// Field labels one control: <label>Label <control></label>.
export const Field = (label, control, { grow = false } = {}) =>
  h("label", { class: grow ? "field field-grow" : "field" }, label, control);

// CheckField puts a checkbox before its label.
export const CheckField = (label, checkbox) => h("label", { class: "check" }, checkbox, label);

export const Checkbox = (props = {}) => h("input", { type: "checkbox", ...props });

export const TextInput = (props = {}) => h("input", { type: "text", class: "input", ...exact, ...props });

// NameInput takes a secret name; EnvInput an environment name and suggests
// the existing ones (the #env-list datalist) unless list is undefined.
export const NameInput = (props = {}) =>
  TextInput({ class: "input mono", pattern: NAME_PATTERN, title: NAME_RULE, ...props });

export const EnvInput = (props = {}) =>
  TextInput({ class: "input mono", pattern: ENV_PATTERN, title: ENV_RULE, list: "env-list", ...props });

export const TextArea = (props = {}) => h("textarea", { class: "input mono", ...exact, ...props });

export const Hint = (text) => h("p", { class: "hint", textContent: text });

// Fieldset groups related controls under a legend.
export const Fieldset = (legend, ...children) =>
  h("fieldset", { class: "fieldset" }, h("legend", { textContent: legend }), children);
