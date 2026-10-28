import { h, icon } from "../lib/dom.js";

// Logo is tantalum's element symbol, Ta, on a metal tile; it names the app
// for screen readers and on hover.
const Logo = () => h("span", { class: "logo", role: "img", "aria-label": "tantalum", title: "tantalum", textContent: "Ta" });

// Topbar shows the brand, which vault the page manages, and the page tools
// (search, buttons) on the right.
export class Topbar {
  constructor({ tools = [] }) {
    this.vault = h("p", { class: "vault" });
    this.el = h(
      "header",
      { class: "topbar" },
      h(
        "div",
        { class: "topbar-inner" },
        h(
          "div",
          { class: "brand" },
          Logo(),
          h("span", { class: "divider", "aria-hidden": "true" }),
          this.vault,
        ),
        h("div", { class: "toolbar" }, tools),
      ),
    );
  }

  // setVault takes GET /api/vault: root is the project directory, "" for the
  // default vault; dir is the vault directory.
  setVault({ root, dir }) {
    const where = root || dir;
    this.vault.title = root ? `Project vault of ${root}` : `Default vault in ${dir}`;
    this.vault.replaceChildren(
      icon(root ? "folder" : "lock"),
      h("strong", { textContent: root ? root.split("/").pop() : "Default vault" }),
      h("span", { class: "vault-path", title: where, textContent: where }),
    );
  }
}
