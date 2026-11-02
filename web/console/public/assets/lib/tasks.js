import { h } from "./dom.js";
import { icon } from "./icons.js";
import { runStates } from "./runs.js";

// The task states, drawn as the run states are: a waiting task waits for an answer in its thread.
export const taskStates = { ...runStates, waiting: { word: "Waiting for an answer", icon: "circle-pause", tone: "neutral" } };
export function taskBadge(state) {
  const s = taskStates[state];
  return h("span", { class: "badge tone-" + s.tone }, icon(s.icon), s.word);
}
