// Icon shapes on a 24×24 grid, stroked by the `.icon` styles. Add an icon by
// adding an entry here; icon(name) in dom.js draws it.
export const ICONS = {
  alert: [["path", { d: "M10.3 4.2 2.6 17.5a2 2 0 0 0 1.7 3h15.4a2 2 0 0 0 1.7-3L13.7 4.2a2 2 0 0 0-3.4 0zM12 9.5v4M12 17h.01" }]],
  copy: [
    ["rect", { x: 9, y: 9, width: 11, height: 11, rx: 2 }],
    ["path", { d: "M5 15a1 1 0 0 1-1-1V5a1 1 0 0 1 1-1h9a1 1 0 0 1 1 1" }],
  ],
  folder: [["path", { d: "M3 7.5A2.5 2.5 0 0 1 5.5 5H9l2 2.5h7.5A2.5 2.5 0 0 1 21 10v7.5a2.5 2.5 0 0 1-2.5 2.5h-13A2.5 2.5 0 0 1 3 17.5z" }]],
  lock: [
    ["rect", { x: 5, y: 11, width: 14, height: 10, rx: 2 }],
    ["path", { d: "M8 11V8a4 4 0 0 1 8 0v3" }],
  ],
  more: [["path", { d: "M6 12h.01M12 12h.01M18 12h.01" }]],
  pencil: [["path", { d: "M4 20h4L19 9a2.1 2.1 0 0 0-3-3L5 17z" }]],
  plus: [["path", { d: "M12 5v14M5 12h14" }]],
  search: [
    ["circle", { cx: 11, cy: 11, r: 7 }],
    ["path", { d: "m20 20-3.5-3.5" }],
  ],
  terminal: [["path", { d: "m5 7 5 5-5 5M12 17h7" }]],
  trash: [["path", { d: "M4 7h16M9 7V4.5h6V7M6.5 7l.8 12.2a1.5 1.5 0 0 0 1.5 1.3h6.4a1.5 1.5 0 0 0 1.5-1.3L17.5 7M10 11v6M14 11v6" }]],
  upload: [["path", { d: "M12 15V4M7 9l5-5 5 5M5 15v3a2 2 0 0 0 2 2h10a2 2 0 0 0 2-2v-3" }]],
  x: [["path", { d: "M6 6l12 12M18 6 6 18" }]],
};
