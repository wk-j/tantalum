import { h, setChildren } from "../lib/dom.js";

// PageHeader is the page title over a summary line, with an optional legend
// ([swatch node, text] pairs) on the right.
export class PageHeader {
  constructor({ title, legend = [] }) {
    this.summary = h("p", { class: "summary" });
    this.legend = h("ul", { class: "legend", hidden: true }, legend.map(([swatch, text]) => h("li", {}, swatch, text)));
    this.el = h("div", { class: "page-head" }, h("div", {}, h("h1", { textContent: title }), this.summary), this.legend);
  }

  setSummary(...parts) {
    setChildren(this.summary, ...parts);
  }

  showLegend(show) {
    this.legend.hidden = !show;
  }
}
