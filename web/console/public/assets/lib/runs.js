import { api, apiPage } from "./api.js";
import { h } from "./dom.js";
import { capitalized, shortSha, since, whenExactly } from "./format.js";
import { icon, machineAvatar, principalAvatar } from "./icons.js";
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

// runStatuses are the words a run's state reads as, the annotations' that the jobs design maps runs onto: Pending while
// it waits or is queued, In progress while it runs, then Successful or Failed, a cancellation reading Failed.
export const runStatuses = {
  pending: { word: "Pending", tone: "neutral", states: ["waiting", "queued"] },
  in_progress: { word: "In progress", tone: "info", states: ["running"] },
  successful: { word: "Successful", tone: "success", states: ["succeeded"] },
  failed: { word: "Failed", tone: "danger", states: ["failed", "cancelled"] },
};
// runStatus is a run's status as Badge/Quiet draws it in a table, its state and why in its tooltip.
export function runStatus(r) {
  const s = Object.values(runStatuses).find((v) => v.states.includes(r.state));
  return h("span", { class: "badge-quiet tone-" + s.tone, title: runStates[r.state].word + (r.reason ? ": " + capitalized(r.reason) : "") }, s.word);
}

// duration is how long a run ran, as the Jobs table says it: 38s, 4m 12s, 1h 05m; until now while it runs, and a dash
// before it starts.
export function duration(r) {
  if (!r.startedAt) return "—";
  const s = Math.max(0, Math.round(((r.endedAt ? new Date(r.endedAt) : new Date()) - new Date(r.startedAt)) / 1000));
  const two = (n) => String(n).padStart(2, "0");
  return s < 60 ? s + "s" : s < 3600 ? Math.floor(s / 60) + "m " + two(s % 60) + "s" : Math.floor(s / 3600) + "h " + two(Math.floor(s / 60) % 60) + "m";
}

// runsTable is a repository's runs as the Jobs screen's table draws them, newest first, a page at a time, Older runs
// adding the next under those shown. load shows the page at from, in place of those shown unless adding, and answers
// it, or null when it failed or a later load overtook it.
export function runsTable(path, at, fail) {
  const rows = h("tbody");
  const table = h("table", { class: "table runs-table" },
    h("thead", {}, h("tr", {}, ["Job", "Branch", "Commit", "Status", "Started", "Duration", "By"].map((t) => h("th", {}, t)))), rows);
  const more = h("button", { class: "btn btn-sm", type: "button", hidden: true }, "Older runs");
  let next = null;
  let asked = 0;
  async function load(from, adding) {
    const mine = ++asked;
    more.disabled = true;
    try {
      const page = await apiPage(from);
      if (mine !== asked) return null;
      if (!adding) rows.replaceChildren();
      rows.append(...page.data.runs.map((r) => runRow(at, r)));
      next = page.next;
      more.hidden = !next;
      return page;
    } catch (err) {
      if (mine === asked) fail(err);
      return null;
    } finally {
      if (mine === asked) more.disabled = false;
    }
  }
  more.addEventListener("click", () => load(next, true));
  return { table, more, load };
}

// runsAt is the runs at commit sha, as a branch's Jobs tab shows them at its head until it draws its pushes: the Jobs
// table, or an Empty state when none ran there.
export async function runsAt(path, at, fail, sha) {
  const list = runsTable(path, at, fail);
  const page = await list.load(path + "/runs?" + new URLSearchParams({ sha }), false);
  if (page && !page.data.runs.length) {
    return noRuns("play", "No runs at " + shortSha(sha), ["The jobs ", h("code", {}, "/cobranch.yaml"), " declares at this commit run when what they name under ", h("code", {}, "on"), " happens, such as a push."]);
  }
  return h("div", { class: "runs-list" }, list.table, h("div", {}, list.more));
}

// noRuns is the card a list of runs shows when it has none: an Empty state saying why, and action under it, if any.
export const noRuns = (glyph, title, text, action = null) => h("section", { class: "card empty-centred" }, h("div", { class: "empty" },
  h("div", { class: "empty-icon" }, icon(glyph)), h("h2", { class: "empty-title" }, title), h("p", { class: "empty-description" }, text), action));

// runRow is a run's row in the Jobs table: its job and number, linking to its page; its branch, or tag; its commit;
// its status; when it started, or was queued while it waits; how long it ran; and who caused it, or its job's avatar
// for a schedule's run.
const runRow = (at, r) => {
  const ref = r.ref.replace(/^refs\/(heads|tags)\//, "");
  return h("tr", {},
    h("td", { class: "cell-mono" }, h("a", { href: at.run(r.number), "data-nav": "" }, r.job.displayName + " #" + r.number)),
    h("td", { class: "cell-mono", title: r.ref.startsWith("refs/tags/") ? "Tag " + ref : ref }, ref),
    h("td", { class: "cell-mono" }, h("a", { href: at.commit(r.sha), "data-nav": "", title: r.sha }, shortSha(r.sha))),
    h("td", {}, runStatus(r)),
    h("td", { class: "cell-muted", title: whenExactly(r.startedAt || r.queuedAt) }, since(r.startedAt || r.queuedAt)),
    h("td", {}, duration(r)),
    h("td", {}, r.causedBy ? principalAvatar(r.causedBy) : machineAvatar(r.job.displayName + ", on its schedule")));
};

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
