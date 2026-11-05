import { annotationStatuses } from "./annotations.js";
import { h } from "./dom.js";
import { plural } from "./format.js";
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

// aiCheckStates are the states of the AI reviewer's check: a word for each, its icon and tone, and whether it is of
// the head; why one blocks a branch, said in full; and the three that keep it Processing.
export const aiCheckStates = {
  waiting: { word: "Waits for a member to start its review", icon: "circle-pause", tone: "neutral", head: true, pending: true },
  queued: { word: "Queued", icon: "clock", tone: "neutral", head: true, pending: true },
  running: { word: "Reviewing", icon: "loader-circle", tone: "info", head: true, pending: true },
  approved: { word: "Approved", icon: "circle-check", tone: "success", head: true },
  changes_requested: { word: "Changes requested", icon: "circle-alert", tone: "warning", head: true, why: "The AI reviewer requested changes on its head." },
  not_approved: { word: "Not approved", icon: "circle-x", tone: "danger", head: true, why: "The AI reviewer reviewed its head without approving it." },
  unreviewed: { word: "Not reviewed", icon: "circle-x", tone: "danger", head: true, why: "The AI reviewer hasn't reviewed its head: ask for a review." },
  failed: { word: "Failed", icon: "circle-x", tone: "danger", head: true, why: "The AI review failed: " },
  cancelled: { word: "Cancelled", icon: "circle-x", tone: "danger", head: true, why: "The AI review was cancelled: " },
  no_model: { word: "No model", icon: "circle-x", tone: "danger", why: "The AI reviewer has no model: an organization admin picks one on its agent's page." },
  suspended: { word: "Suspended", icon: "circle-x", tone: "danger", why: "The AI reviewer is suspended." },
  off: { word: "Off for this branch", icon: "circle-x", tone: "danger", why: "main's /cobranch-review.yaml leaves this branch out of the AI reviewer's reviews." },
  missing: { word: "No reviewer", icon: "circle-x", tone: "danger", why: "The organization has no AI reviewer." },
};

// checkShort names an unmet check in a few words, as a merge card's title and a branch row say it.
export function checkShort(b, c) {
  if (c.kind === "annotation") return c.name + (c.status ? " " + annotationStatuses[c.status].word.toLowerCase() : " missing");
  if (c.kind === "linear_history") return "merge commits in its history";
  if (c.kind === "approvals") return c.count + " of " + plural(c.required, "approval");
  if (c.kind === "changes_requested") return "changes requested";
  if (c.kind === "ai") return "AI review " + (aiCheckStates[c.ai] || aiCheckStates.unreviewed).word.toLowerCase();
  return plural(c.count, "commit") + " behind " + b.base;
}

// processingWhat names what keeps b Processing, in a few words.
export function processingWhat(b) {
  const running = b.checks.find((c) => c.kind === "annotation" && (c.status === "pending" || c.status === "in_progress"));
  const reviewing = b.checks.find((c) => c.kind === "ai" && aiCheckStates[c.ai] && aiCheckStates[c.ai].pending);
  if (running) return running.name + " " + annotationStatuses[running.status].word.toLowerCase();
  return reviewing ? "AI review " + aiCheckStates[reviewing.ai].word.toLowerCase() : "processing its last push";
}
