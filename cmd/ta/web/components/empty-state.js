import { h, icon } from "../lib/dom.js";

// EmptyState fills a view that has nothing to show yet: an icon, a title, a
// short text (string or nodes) and the actions that get the user started.
export const EmptyState = ({ icon: iconName, title, text, actions = [] }) =>
  h(
    "section",
    { class: "empty" },
    h("span", { class: "empty-icon" }, icon(iconName)),
    h("h2", { textContent: title }),
    h("p", {}, text),
    h("div", { class: "empty-actions" }, actions),
  );
