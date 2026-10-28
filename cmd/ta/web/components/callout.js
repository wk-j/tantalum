import { h, icon } from "../lib/dom.js";

// Callout is a boxed note with a leading icon; children may be strings or nodes.
export const Callout = ({ icon: iconName }, ...children) =>
  h("aside", { class: "callout" }, icon(iconName), h("p", {}, children));
