import { h, icon } from "../lib/dom.js";

// Button renders a .btn with an optional leading icon. variant is "primary",
// "danger", "danger-solid" or "icon" (icon only: label becomes its accessible
// name and tooltip). close: true closes the dialog the button sits in. Other
// props (id, hidden, disabled, title, …) go to the element.
export function Button({ label, icon: iconName, variant, type = "button", close = false, ...props }) {
  const iconOnly = variant === "icon";
  return h(
    "button",
    {
      type,
      class: variant ? `btn btn-${variant}` : "btn",
      "aria-label": iconOnly ? label : undefined,
      title: iconOnly ? label : undefined,
      "data-close": close,
      ...props,
    },
    iconName && icon(iconName),
    !iconOnly && h("span", { class: "btn-label", textContent: label }),
  );
}

// setLabel changes the text of a Button made with a label.
export function setLabel(button, text) {
  button.querySelector(".btn-label").textContent = text;
}

// FileButton looks like a Button and opens the file chooser; onchange gets the
// change event of its file input.
export function FileButton({ label, icon: iconName = "upload", accept, onchange }) {
  return h(
    "label",
    { class: "btn btn-file" },
    icon(iconName),
    h("span", { class: "btn-label", textContent: label }),
    h("input", { type: "file", accept, onchange }),
  );
}
