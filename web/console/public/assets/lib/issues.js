import { h } from "./dom.js";
import { icon } from "./icons.js";

// The issue states as the design system draws them, in the order the side column offers them: Done and Canceled
// close an issue. A list groups the open ones nearest to done first.
export const issueStates = {
  backlog: { word: "Backlog", icon: "circle-dashed", tone: "neutral" },
  todo: { word: "Todo", icon: "circle", tone: "neutral" },
  in_progress: { word: "In progress", icon: "loader-circle", tone: "info" },
  in_review: { word: "In review", icon: "eye", tone: "warning" },
  done: { word: "Done", icon: "circle-check", tone: "success" },
  canceled: { word: "Canceled", icon: "circle-slash", tone: "neutral" },
};
// issueBadge is an issue's state as a badge, as branchBadge draws a branch's.
export function issueBadge(state) {
  const s = issueStates[state];
  return h("span", { class: "badge tone-" + s.tone }, icon(s.icon), s.word);
}
