const byId = (id) => document.getElementById(id);
export const app = byId("app");
export const h = (tag, attrs = {}, ...children) => {
  const el = document.createElement(tag);
  for (const [k, v] of Object.entries(attrs)) {
    if (k === "class") el.className = v;
    else if (k.startsWith("on")) el.addEventListener(k.slice(2), v);
    else if (k === "hidden" || k === "disabled" || k === "checked" || k === "selected") el[k] = v;
    else if (v !== undefined && v !== null) el.setAttribute(k, v);
  }
  el.append(...children.flat(Infinity).filter((c) => c !== null && c !== undefined && c !== false));
  return el;
};
