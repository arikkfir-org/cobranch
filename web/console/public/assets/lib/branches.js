import { h } from "./dom.js";
import { icon } from "./icons.js";

// The branch statuses as the design system draws them, the same way.
export const branchStatuses = {
  processing: { word: "Processing", icon: "loader-circle", tone: "info" },
  blocked: { word: "Blocked", icon: "octagon-alert", tone: "danger" },
  empty: { word: "Empty", icon: "circle-dashed", tone: "neutral" },
  merged: { word: "Merged", icon: "git-merge", tone: "inverse" },
  ready: { word: "Ready", icon: "circle-check", tone: "success" },
};
// branchBadge is a branch's status as a badge, as annotationBadge draws an annotation's.
export function branchBadge(status) {
  const s = branchStatuses[status];
  return h("span", { class: "badge tone-" + s.tone }, icon(s.icon), s.word);
}
