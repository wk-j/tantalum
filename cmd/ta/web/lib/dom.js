import { ICONS } from "./icons.js";

const SVG_NS = "http://www.w3.org/2000/svg";

// Keys that must be set as attributes although the element has a property of
// that name (the property is read-only).
const ATTRIBUTES = new Set(["list"]);

// h creates an element. In props, "class" sets the class list, "on<event>"
// adds a listener, and any other key sets the DOM property when the element
// has one, else the attribute (true sets it empty, false leaves it out).
// Undefined and null props are skipped. Children may be nodes, strings,
// arrays of them, or null/undefined/false, which are skipped.
export function h(tag, props = {}, ...children) {
  const el = document.createElement(tag);
  for (const [key, value] of Object.entries(props)) {
    if (value == null) continue;
    if (key === "class") el.className = value;
    else if (key.startsWith("on")) el.addEventListener(key.slice(2), value);
    else if (key in el && !ATTRIBUTES.has(key)) el[key] = value;
    else if (value !== false) el.setAttribute(key, value === true ? "" : value);
  }
  el.append(...flatten(children));
  return el;
}

// setChildren replaces the children of el, taking children as h does.
export function setChildren(el, ...children) {
  el.replaceChildren(...flatten(children));
  return el;
}

const flatten = (children) => children.flat(Infinity).filter((child) => child != null && child !== false);

// icon draws an icon from ICONS as a decorative <svg>.
export function icon(name, className = "icon") {
  const shapes = ICONS[name];
  if (!shapes) throw new Error(`unknown icon ${name}`);
  const svg = document.createElementNS(SVG_NS, "svg");
  svg.setAttribute("class", className);
  svg.setAttribute("viewBox", "0 0 24 24");
  svg.setAttribute("aria-hidden", "true");
  for (const [tag, attrs] of shapes) {
    const shape = document.createElementNS(SVG_NS, tag);
    for (const [key, value] of Object.entries(attrs)) shape.setAttribute(key, value);
    svg.append(shape);
  }
  return svg;
}
