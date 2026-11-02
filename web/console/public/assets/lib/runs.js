import { api, apiPage } from "./api.js";
import { h } from "./dom.js";
import { capitalized, shortSha, whenExactly } from "./format.js";
import { icon } from "./icons.js";
import { go, render } from "./router.js";

// The run states, drawn as the annotation statuses are.
export const runStates = {
  waiting: { word: "Waiting", icon: "circle-pause", tone: "neutral" },
  queued: { word: "Queued", icon: "clock", tone: "neutral" },
  running: { word: "Running", icon: "loader-circle", tone: "info" },
  succeeded: { word: "Succeeded", icon: "circle-check", tone: "success" },
  failed: { word: "Failed", icon: "circle-x", tone: "danger" },
  cancelled: { word: "Cancelled", icon: "ban", tone: "neutral" },
};
// runBadge is a run's state as a badge, as annotationBadge draws an annotation's status.
export function runBadge(state) {
  const s = runStates[state];
  return h("span", { class: "badge tone-" + s.tone }, icon(s.icon), s.word);
}

// runsPanel lists the repository's runs, newest first, at commit sha unless it is empty, a page at a time: each
// with its state and why, its job, its ref and commit, what queued it and when, and Start or Cancel for a viewer
// holding jobs.run.
export async function runsPanel(path, at, can, fail, sha) {
  const rows = h("tbody");
  let next = null;
  const older = h("button", { class: "button small", type: "button", hidden: true, onclick: () => load(next) }, "Older runs");
  const row = runRow(path, at, can, fail);
  async function load(from) {
    older.disabled = true;
    try {
      const page = await apiPage(from);
      rows.append(...page.data.runs.map(row));
      next = page.next;
      older.hidden = !next;
    } catch (err) {
      fail(err);
    } finally {
      older.disabled = false;
    }
  }
  await load(path + "/runs" + (sha ? "?" + new URLSearchParams({ sha }) : ""));
  const none = h("p", {}, sha ? "No runs at this commit. " : "No runs yet. ", "The jobs that ", h("code", {}, "/cobranch.yaml"),
    " declares run when what they name under ", h("code", {}, "on"), " happens, such as a push.");
  return h("section", { class: "panel" }, h("h3", {}, "Runs"), rows.children.length ? h("table", { class: "runs" }, rows) : none, h("div", {}, older));
}

// runRow makes a row of a run: its state and why, its job, ref and commit, what queued it and when, and its actions.
const runRow = (path, at, can, fail) => (r) => h("tr", {},
  h("td", { class: "state" }, runBadge(r.state)),
  h("td", {}, h("a", { href: at.run(r.number), "data-nav": "" }, h("code", {}, r.job.displayName), " #" + r.number), r.reason ? h("p", {}, capitalized(r.reason)) : null,
    h("p", { class: "muted" }, refName(r.ref), " · ", h("a", { href: at.commit(r.sha), "data-nav": "" }, h("code", {}, shortSha(r.sha))), " · ", triggered(r), " · ", whenExactly(r.queuedAt))),
  h("td", { class: "actions" }, runActions(path, at, r, can, fail)));

// runActions are Start for a waiting run, Cancel for one that hasn't ended, and Re-run for one that has, which opens
// the new run, for a viewer holding jobs.run.
export function runActions(path, at, r, can, fail) {
  if (!can("jobs.run")) return null;
  const act = (verb, button, then) => {
    button.disabled = true;
    api("POST", path + "/runs/" + r.number + "/" + verb).then(then).catch((err) => { button.disabled = false; fail(err); });
  };
  const name = r.job.displayName + " #" + r.number;
  const active = ["waiting", "queued", "running"].includes(r.state);
  const start = r.state === "waiting" ? h("button", { class: "button small primary", type: "button", title: "Start " + name,
    onclick: (e) => act("start", e.currentTarget, render) }, "Start") : null;
  const cancel = active ? h("button", { class: "button small danger", type: "button", title: "Cancel " + name,
    onclick: (e) => confirm("Cancel " + name + "?") && act("cancel", e.currentTarget, render) }, "Cancel") : null;
  const rerun = active ? null : h("button", { class: "button small", type: "button", title: "Run " + name + "'s definition again at its commit",
    onclick: (e) => act("rerun", e.currentTarget, (n) => go(at.run(n.number))) }, "Re-run");
  return [start, start && cancel ? " " : null, cancel, rerun];
}

// manualJobs lists the jobs b's head declares with a manual trigger, each with Run, which starts it at the head and
// opens its run, for a viewer holding jobs.run.
export function manualJobs(path, at, b, can, fail) {
  if (!b.manualJobs || !b.manualJobs.length || !can("jobs.run")) return null;
  const run = (job, button) => {
    button.disabled = true;
    api("POST", path + "/branches/" + encodeURIComponent(b.name) + "/jobs/" + encodeURIComponent(job) + "/runs")
      .then((r) => go(at.run(r.number))).catch((err) => { button.disabled = false; fail(err); });
  };
  return h("section", { class: "panel" }, h("h3", {}, "Jobs to run by hand"),
    h("p", {}, "Its ", h("code", {}, "/cobranch.yaml"), " lets members start these at ", h("code", {}, shortSha(b.head)), "."),
    h("table", { class: "runs" }, h("tbody", {}, b.manualJobs.map((job) => h("tr", {}, h("td", {}, h("code", {}, job)),
      h("td", { class: "actions" }, h("button", { class: "button small primary", type: "button", title: "Run " + job + " at " + shortSha(b.head),
        onclick: (e) => run(job, e.currentTarget) }, "Run")))))));
}
// refName is a ref by the name its kind gives it: a branch's or a tag's.
export const refName = (ref) => ref.startsWith("refs/heads/") ? ["branch ", h("code", {}, ref.slice(11))] : ref.startsWith("refs/tags/") ? ["tag ", h("code", {}, ref.slice(10))] : h("code", {}, ref);
// triggered says what queued a run.
export const triggered = (r) => (r.rerunOf ? "a re-run of #" + r.rerunOf + ", " : "") + (({ push: "a push", tag: "a tag's push", annotation: "annotation " + r.trigger.name,
  review: "a review", event: "event " + r.trigger.name, schedule: "its schedule", manual: "a member" })[r.trigger.kind] || r.trigger.kind);
