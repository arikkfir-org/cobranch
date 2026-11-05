import { annotationStatuses } from "./annotations.js";
import { api, apiPage } from "./api.js";
import { h } from "./dom.js";
import { capitalized, plural, shortSha, since, whenExactly } from "./format.js";
import { actorAvatar, icon, machineAvatar } from "./icons.js";
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

// branchJobs is b's Jobs tab: its pushes, newest first by when their first run was queued, each a card of the runs
// of its jobs at that commit as Annotation rows, the latest attempt of each, with what its annotation found. The head's
// card leads even before any job ran there, with the jobs it starts by hand and, once each of its runs ended, Re-run
// all; the two newest pushes show, and N earlier pushes opens the rest of those its latest hundred runs name.
export async function branchJobs(path, at, b, can, fail) {
  const page = await api("GET", path + "/runs?" + new URLSearchParams({ branch: b.name, limit: "100" }));
  const latest = new Map([[b.head, []]]);
  const pushed = new Map();
  for (const r of page.runs) {
    const runs = latest.get(r.sha) || latest.set(r.sha, []).get(r.sha);
    if (!runs.some((o) => o.job.id === r.job.id)) runs.push(r);
    if (!pushed.has(r.sha) || r.queuedAt < pushed.get(r.sha)) pushed.set(r.sha, r.queuedAt);
  }
  const pushes = new Map([...latest].sort(([x], [y]) => (x === b.head ? -1 : y === b.head ? 1 : pushed.get(y).localeCompare(pushed.get(x)))));
  if (pushes.size === 1 && !pushes.get(b.head).length && !(b.manualJobs || []).length) {
    return [noRuns("play", "No runs on this branch", ["Jobs run when a push to this branch matches what they name under ", h("code", {}, "on"), ". Manual jobs appear here with a Run button."])];
  }
  const cards = await Promise.all([...pushes].map(([sha, runs], i) => i < 2 ? pushCard(path, at, b, sha, runs, pushed.get(sha), can, fail) : null));
  const earlier = [...pushes].slice(2);
  if (!earlier.length) return cards.filter(Boolean);
  const open = h("a", { class: "earlier-pushes", href: "#", role: "button" }, earlier.length + " earlier push" + (earlier.length === 1 ? "" : "es"));
  const line = h("p", {}, open);
  open.addEventListener("click", async (e) => {
    e.preventDefault();
    line.replaceWith(...await Promise.all(earlier.map(([sha, runs]) => pushCard(path, at, b, sha, runs, pushed.get(sha), can, fail))));
  });
  return [...cards.filter(Boolean), line];
}

// pushCard is one push's card on a branch's Jobs tab: when it was pushed (b's last move for its head, else when its
// first run was queued), the runs at sha, by their jobs' names, each with its job's annotation there, and, at b's head,
// the jobs it starts by hand that haven't run there, and Re-run all.
async function pushCard(path, at, b, sha, runs, queuedAt, can, fail) {
  const head = sha === b.head;
  // A commit that can't be read leaves its rows without what their annotations found.
  const found = await api("GET", path + "/commits/" + sha).then((c) => new Map(c.annotations.map((a) => [a.name, a.summary])), () => new Map());
  runs.sort((x, y) => x.job.displayName.localeCompare(y.job.displayName));
  const manual = head ? (b.manualJobs || []).filter((job) => !runs.some((r) => r.job.displayName === job)) : [];
  const ended = runs.length && runs.every((r) => !["waiting", "queued", "running"].includes(r.state));
  const rerun = head && ended && can("jobs.run") ? h("button", { class: "btn btn-sm", type: "button", title: "Run each job again at " + shortSha(sha) }, "Re-run all") : null;
  rerun?.addEventListener("click", async () => {
    rerun.disabled = true;
    try {
      for (const r of runs) await api("POST", path + "/runs/" + r.number + "/rerun");
      render();
    } catch (err) {
      rerun.disabled = false;
      fail(err);
    }
  });
  const rows = [...runs.map((r) => jobRow(at, r, found.get(r.job.displayName))), ...manual.map((job) => manualRow(path, at, b, job, can, fail))];
  const when = head ? b.updatedAt : queuedAt;
  const counted = [runs.length ? plural(runs.length, "job") : "no jobs", manual.length ? manual.length + " manual" : null].filter(Boolean).join(" · ");
  return h("section", { class: "card push-card" },
    h("div", { class: "card-header" }, h("h2", { class: "card-title" }, "On " + shortSha(sha)),
      h("span", { class: "card-meta", title: whenExactly(when) }, since(when) + " · " + counted), rerun),
    rows.length ? h("ul", { class: "list" }, rows)
      : h("p", { class: "card-body card-note" }, h("span", {}, "No jobs ran on this push: nothing in ", h("code", {}, "/cobranch.yaml"), " names it.")));
}

// jobRow is a run in a push's card (Annotation row): its status, as its job's annotation says it; its job; what the
// annotation found, or that it waits for a member; its job's avatar; how long it ran; and its page.
function jobRow(at, r, found) {
  const status = Object.entries(runStatuses).find(([, v]) => v.states.includes(r.state))[0];
  const s = annotationStatuses[status];
  const why = r.state === "waiting" ? "Waiting for a member to start it" : found || (r.reason ? capitalized(r.reason) : "");
  return h("li", { class: "annotation-row" },
    h("span", { class: "tone-" + s.tone }, icon(s.icon, runStates[r.state].word + (r.reason ? ": " + capitalized(r.reason) : ""))),
    h("span", { class: "row-title" }, r.job.displayName), h("span", { class: "row-description", title: why || null }, why),
    machineAvatar(r.job.displayName), h("span", { class: "row-aside", title: r.startedAt ? "Started " + whenExactly(r.startedAt) : null }, duration(r)),
    h("a", { href: at.run(r.number), "data-nav": "", title: r.job.displayName + " #" + r.number }, "Details"));
}

// manualRow is a job b's head starts by hand, in its card: its name, and, for a viewer holding jobs.run, Run, which
// starts it at the head and opens its run.
function manualRow(path, at, b, job, can, fail) {
  const run = can("jobs.run") ? h("button", { class: "btn btn-sm", type: "button", title: "Run " + job + " at " + shortSha(b.head) }, icon("play"), "Run") : null;
  run?.addEventListener("click", () => {
    run.disabled = true;
    api("POST", path + "/branches/" + encodeURIComponent(b.name) + "/jobs/" + encodeURIComponent(job) + "/runs")
      .then((r) => go(at.run(r.number))).catch((err) => { run.disabled = false; fail(err); });
  });
  return h("li", { class: "annotation-row manual-job" }, h("span", { class: "manual-job-icon" }, icon("play", "Manual")),
    h("span", { class: "row-title" }, job), h("span", { class: "row-description" }, "Manual · runs when a member starts it"), run);
}

// noRuns is the card a list of runs shows when it has none: an Empty state saying why, and action under it, if any.
export const noRuns = (glyph, title, text, action = null) => h("section", { class: "card empty-centred" }, h("div", { class: "empty" },
  h("div", { class: "empty-icon" }, icon(glyph)), h("h2", { class: "empty-title" }, title), h("p", { class: "empty-description" }, text), action));

// runRow is a run's row in the Jobs table: its job and number, linking to its page; its branch, or tag; its commit;
// its status; when it started, or was queued while it waits; how long it ran; and who caused it.
const runRow = (at, r) => {
  const ref = r.ref.replace(/^refs\/(heads|tags)\//, "");
  return h("tr", {},
    h("td", { class: "cell-mono" }, h("a", { href: at.run(r.number), "data-nav": "" }, r.job.displayName + " #" + r.number)),
    h("td", { class: "cell-mono", title: r.ref.startsWith("refs/tags/") ? "Tag " + ref : ref }, ref),
    h("td", { class: "cell-mono" }, h("a", { href: at.commit(r.sha), "data-nav": "", title: r.sha }, shortSha(r.sha))),
    h("td", {}, runStatus(r)),
    h("td", { class: "cell-muted", title: whenExactly(r.startedAt || r.queuedAt) }, since(r.startedAt || r.queuedAt)),
    h("td", {}, duration(r)),
    h("td", {}, causer(r)));
};

// causer is the avatar of who caused a run, by its kind: a person, an agent, a job or an API key; its job's for a
// schedule's run; or a dash once whoever caused it is gone.
const causer = (r) => r.causedBy ? actorAvatar(r.causedBy)
  : r.trigger.kind === "schedule" ? machineAvatar(r.job.displayName + ", on its schedule")
  : h("span", { class: "cell-unassigned", title: "Someone since gone" }, "–");

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

// refName is a ref by the name its kind gives it: a branch's or a tag's.
export const refName = (ref) => ref.startsWith("refs/heads/") ? ["branch ", h("code", {}, ref.slice(11))] : ref.startsWith("refs/tags/") ? ["tag ", h("code", {}, ref.slice(10))] : h("code", {}, ref);
// triggered says what queued a run.
export const triggered = (r) => (r.rerunOf ? "a re-run of #" + r.rerunOf + ", " : "") + (({ push: "a push", tag: "a tag's push", annotation: "annotation " + r.trigger.name,
  review: "a review", event: "event " + r.trigger.name, schedule: "its schedule", manual: "a member" })[r.trigger.kind] || r.trigger.kind);
