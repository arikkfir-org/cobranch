import { api } from "./api.js";
import { h } from "./dom.js";
import { whenExactly } from "./format.js";
import { icon } from "./icons.js";
import { render } from "./router.js";
import { me } from "./state.js";

// The annotation statuses as the design system draws them: a word, a Lucide icon (ISC licence) and a tone.
export const annotationStatuses = {
  pending: { word: "Pending", icon: "clock", tone: "neutral" },
  in_progress: { word: "In progress", icon: "loader-circle", tone: "info" },
  failed: { word: "Failed", icon: "circle-x", tone: "danger" },
  successful: { word: "Successful", icon: "circle-check", tone: "success" },
};
// annotationBadge is an annotation's status as a badge: its icon and word, in its tone.
function annotationBadge(status) {
  const s = annotationStatuses[status];
  return h("span", { class: "badge tone-" + s.tone }, icon(s.icon), s.word);
}
// annotationIcons are a commit's annotations as icons alone, each named in its tooltip, as a history row shows them.
export const annotationIcons = (annotations) => annotations.length ? h("span", { class: "icons" }, annotations.map((a) => {
  const s = annotationStatuses[a.status];
  return h("span", { class: "tone-" + s.tone }, icon(s.icon, a.name + ": " + s.word));
})) : null;
// took says how long an annotation's run took, or since when it runs.
function took(a) {
  if (!a.completedAt) return "Since " + whenExactly(a.startedAt);
  const seconds = Math.max(0, Math.round((new Date(a.completedAt) - new Date(a.startedAt)) / 1000));
  const parts = [[Math.floor(seconds / 3600), "h"], [Math.floor(seconds / 60) % 60, "m"], [seconds % 60, "s"]].filter(([n], i, all) => n || i === all.length - 1);
  return "Took " + parts.map(([n, unit]) => n + unit).join(" ");
}

// commitAnnotations lists a commit's annotations, by name, with their status, summary, link, principal and run.
export function commitAnnotations(path, c, can, fail) {
  const deletable = (a) => can("annotations.delete_any") || (can("annotations.write") && a.principal && a.principal.id === me.id);
  // It deletes the version it shows: one changed meanwhile is refused, saying so.
  const remove = (a) => api("DELETE", path + "/commits/" + c.sha + "/annotations/" + encodeURIComponent(a.name), undefined, { "If-Match": `"${a.version}"` })
    .then(render).catch(fail);
  const row = (a) => h("tr", {},
    h("td", { class: "state" }, annotationBadge(a.status)),
    h("td", {}, h("code", {}, a.name), a.summary ? h("p", {}, a.summary) : null,
      h("p", { class: "muted" }, (a.principal ? a.principal.displayName : "A principal since gone") + " · " + took(a))),
    h("td", { class: "actions" }, a.link ? h("a", { class: "button small", href: a.link, target: "_blank", rel: "noopener noreferrer" }, "Details") : null,
      deletable(a) ? [" ", h("button", { class: "button small danger", type: "button", title: "Delete " + a.name, onclick: () => remove(a) }, "Delete")] : null));
  return h("section", { class: "panel" }, h("h3", {}, "Annotations"),
    c.annotations.length ? h("table", { class: "annotations" }, h("tbody", {}, c.annotations.map(row)))
      : h("p", {}, "No annotations yet. Principals holding ", h("code", {}, "annotations.write"), " set them through the API, such as a CI system's verdict on this commit."));
}
