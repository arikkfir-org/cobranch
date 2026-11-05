import { api } from "./api.js";
import { h } from "./dom.js";
import { whenExactly } from "./format.js";
import { icon, principalAvatar } from "./icons.js";
import { render } from "./router.js";
import { me } from "./state.js";

// The annotation statuses as the design system draws them: a word, a Lucide icon (ISC licence) and a tone.
export const annotationStatuses = {
  pending: { word: "Pending", icon: "clock", tone: "neutral" },
  in_progress: { word: "In progress", icon: "loader-circle", tone: "info" },
  failed: { word: "Failed", icon: "circle-x", tone: "danger" },
  successful: { word: "Successful", icon: "circle-check", tone: "success" },
};
// annotationIcons are a commit's annotations as icons alone, each named in its tooltip, as a history row shows them.
export const annotationIcons = (annotations) => annotations.length ? h("span", { class: "icons row-icons" }, annotations.map((a) => {
  const s = annotationStatuses[a.status];
  return h("span", { class: "tone-" + s.tone }, icon(s.icon, a.name + ": " + s.word));
})) : null;
// took says how long an annotation's run took, as 1m 12s, or since when it runs.
function took(a) {
  if (!a.completedAt) return "since " + whenExactly(a.startedAt);
  const seconds = Math.max(0, Math.round((new Date(a.completedAt) - new Date(a.startedAt)) / 1000));
  const parts = [[Math.floor(seconds / 3600), "h"], [Math.floor(seconds / 60) % 60, "m"], [seconds % 60, "s"]].filter(([n], i, all) => n || i === all.length - 1);
  return parts.map(([n, unit], i) => (i && unit !== "h" ? String(n).padStart(2, "0") : n) + unit).join(" ");
}

// commitAnnotations is the card of a commit's annotations (Annotation row): each its status, name and summary, the
// principal that set it, how long its run took and a link to its details, and Delete for a viewer who may.
export function commitAnnotations(path, c, can, fail) {
  const deletable = (a) => can("annotations.delete_any") || (can("annotations.write") && a.principal && a.principal.id === me.id);
  // It deletes the version it shows: one changed meanwhile is refused, saying so.
  const remove = (a) => api("DELETE", path + "/commits/" + c.sha + "/annotations/" + encodeURIComponent(a.name), undefined, { "If-Match": `"${a.version}"` })
    .then(render).catch(fail);
  const row = (a) => {
    const s = annotationStatuses[a.status];
    return h("li", { class: "annotation-row" }, h("span", { class: "tone-" + s.tone }, icon(s.icon, s.word)),
      h("code", { class: "row-title" }, a.name), h("span", { class: "row-description", title: a.summary || null }, a.summary || ""),
      a.principal ? principalAvatar(a.principal) : null,
      h("span", { class: "row-aside", title: (a.principal ? a.principal.displayName : "A principal since gone") + " · started " + whenExactly(a.startedAt) }, took(a)),
      a.link ? h("a", { href: a.link, target: "_blank", rel: "noopener noreferrer" }, "Details") : null,
      deletable(a) ? h("button", { class: "icon-btn icon-btn-ghost icon-btn-sm", type: "button", "aria-label": "Delete " + a.name, title: "Delete " + a.name,
        onclick: () => confirm("Delete annotation " + a.name + " of " + c.sha.slice(0, 7) + "?") && remove(a) }, icon("trash-2")) : null);
  };
  return h("section", { class: "card", id: "annotations" },
    h("div", { class: "card-header" }, h("h2", { class: "card-title" }, "Annotations"), h("code", { class: "card-meta" }, "on " + c.sha.slice(0, 7))),
    c.annotations.length ? h("ul", { class: "list" }, c.annotations.map(row))
      : h("div", { class: "card-body" }, h("p", { class: "muted" }, "None yet. Principals holding annotations.write set them through the API, such as a CI system's verdict on this commit.")));
}
