import { api } from "../../lib/api.js";
import { branchSubject } from "../../lib/chat.js";
import { h } from "../../lib/dom.js";
import { shortSha } from "../../lib/format.js";
import { icon } from "../../lib/icons.js";
import { popover } from "../../lib/menu.js";
import { repositoryRun } from "../../lib/run.js";
import { go } from "../../lib/router.js";
import { noRuns, runStatuses, runsTable } from "../../lib/runs.js";
import { base } from "../../lib/state.js";

export const nav = { label: "Jobs", order: 20 };
export const accepts = (rest) => rest.length <= 1;
// crumbs name the Jobs list, or a run by its number until its page reads its job's name.
export const crumbs = ({ rest }) => [{ label: rest.length ? "#" + rest[0] : "Jobs" }];
// subject is a run's branch's conversation, on the run's page; a tag's run has none.
export const subject = ({ org, repo, path, at, rest, can }) => rest.length ? api("GET", path + "/runs/" + encodeURIComponent(rest[0]))
  .then((r) => (r.ref.startsWith("refs/heads/") ? branchSubject(org, repo, path, at, r.ref.slice("refs/heads/".length), can) : null)) : null;
export default async (ctx) => ctx.rest.length ? repositoryRun(ctx) : [await jobsList(ctx.path, ctx.at, ctx.fail)];

// filterNames are the Jobs list's filters, which the address names: a text, a job, a branch and a status.
const filterNames = ["q", "job", "branch", "status"];
// listPath is the list's path under the Console's root with the filters the address names now, each of changes set to
// its value, or dropped for none.
function listPath(changes) {
  const p = new URLSearchParams(location.search);
  for (const [name, value] of Object.entries(changes)) if (value) p.set(name, value); else p.delete(name);
  return "/" + location.pathname.slice(base.length) + (p.toString() ? "?" + p : "");
}
// apiQuery is the API's query for the runs the filters params name keep: a status asks for each state it reads.
function apiQuery(params) {
  const query = new URLSearchParams();
  for (const name of ["q", "job", "branch"]) if (params.get(name)) query.set(name, params.get(name));
  for (const state of runStatuses[params.get("status")]?.states || []) query.append("state", state);
  return query;
}

// jobsList is the repository's runs as the Jobs screen draws them, under its filters: Filter jobs, which keeps the runs
// whose job's name or branch holds its text, or whose commit starts with it, as it is typed; Any job, Any branch and
// Any status, whose choices reload the page; and how many runs they keep. A link to the runs at a commit names its
// short name as the text, and one of an earlier release's, sha=, is read as one.
async function jobsList(path, at, fail) {
  const legacy = new URLSearchParams(location.search).get("sha");
  if (legacy) history.replaceState(history.state, "", base + listPath({ sha: "", q: shortSha(legacy) }).slice(1));
  const params = new URLSearchParams(location.search);
  const list = runsTable(path, at, fail);
  const count = h("span", { class: "filters-count" });
  const none = h("div", { hidden: true });
  let typing = 0;
  const text = h("input", { type: "search", placeholder: "Filter jobs", "aria-label": "Filter jobs", value: params.get("q") || "",
    oninput: () => {
      clearTimeout(typing);
      typing = setTimeout(() => {
        history.replaceState(history.state, "", base + listPath({ q: text.value.trim() }).slice(1));
        show();
      }, 250);
    } });
  // show draws the runs the address's filters keep, and counts them, of every run once they keep fewer. A later show
  // overtakes it: what it reads after one started draws nothing.
  let shown = 0;
  async function show() {
    const mine = ++shown;
    const now = new URLSearchParams(location.search);
    const filtered = filterNames.some((n) => now.get(n));
    const [page, all] = await Promise.all([list.load(path + "/runs?" + apiQuery(now), false),
      filtered ? api("GET", path + "/runs?limit=1").then((d) => d.total, () => undefined) : undefined]);
    if (!page || mine !== shown) return;
    const n = page.data.total;
    const runs = (k) => k.toLocaleString() + (k === 1 ? " run" : " runs");
    count.textContent = n === undefined ? "" : all === undefined ? runs(n) : n.toLocaleString() + " of " + runs(all);
    list.table.hidden = !page.data.runs.length;
    none.hidden = !list.table.hidden;
    none.replaceChildren(...(none.hidden ? [] : [noneShown(now)]));
  }
  const filters = h("div", { class: "filters runs-filters" }, h("div", { class: "input" }, icon("search"), text), ...jobFilters(path), count);
  await show();
  return h("div", { class: "runs-page" }, filters, list.table, none, h("div", {}, list.more));
}

// jobFilters are the list's Any job, Any branch and Any status buttons, each opening a Menu of its choices, the one the
// address names checked, and reading as it once one is. Jobs are those that ever queued a run, read when the menu first
// opens, and branches the live ones, main first.
function jobFilters(path) {
  const params = new URLSearchParams(location.search);
  const pick = (name, any, glyph, choices, word = (value) => value) => {
    const current = params.get(name) || "";
    const button = h("button", { class: "btn", type: "button", "aria-haspopup": "menu" }, icon(glyph), current ? word(current) : any, icon("chevron-down"));
    let read = null;
    button.addEventListener("click", async () => {
      read ||= choices().catch((err) => { read = null; throw err; });
      // A list that fails to arrive leaves Any alone in the menu, and the next click asks again.
      const options = await read.catch(() => []);
      popover(button, menuAlign(button), (close) => {
        const item = ([value, label]) => h("button", { class: "menu-item", type: "button", role: "menuitemradio", "aria-checked": String(value === current),
          onclick: () => { close(); go(listPath({ [name]: value }), true); } }, h("span", {}, label));
        return [item(["", any]), options.length ? h("hr", { class: "menu-divider" }) : null, options.map(item)];
      }, "runs-menu");
    });
    return button;
  };
  const named = (list) => list.map((n) => [n, n]);
  return [
    pick("job", "Any job", "bot", () => api("GET", path + "/jobs").then((d) => named(d.jobs.map((j) => j.displayName).sort()))),
    pick("branch", "Any branch", "git-branch", () => api("GET", path + "/branches").then((d) => named(d.branches.map((b) => b.name)))),
    pick("status", "Any status", "circle-check", async () => Object.entries(runStatuses).map(([value, s]) => [value, s.word]), (value) => runStatuses[value]?.word || value),
  ];
}

// menuAlign aligns a Menu to its button's start, or to its end when the Menu would leave the window that way.
const menuAlign = (button) => (button.getBoundingClientRect().left + 260 > document.documentElement.clientWidth ? "end" : "start");
// How No run matches says each status, after the run it names.
const statusPhrases = { pending: "is pending", in_progress: "is in progress", successful: "has succeeded", failed: "has failed" };

// noneShown is the card the list shows when it has no run to show: none yet, or none the filters params name keep,
// which says what they ask for and clears them.
function noneShown(params) {
  if (!filterNames.some((n) => params.get(n))) {
    return noRuns("play", "No runs yet", ["The jobs ", h("code", {}, "/cobranch.yaml"), " declares run when what they name under ", h("code", {}, "on"), " happens, such as a push."]);
  }
  const [q, job, branch, status] = filterNames.map((n) => params.get(n));
  const run = "No run" + (job ? " of " + job : "") + (branch ? " on " + branch : "");
  const asked = statusPhrases[status] ? run + " " + statusPhrases[status] + (q ? ", matching “" + q + "”" : "") : q ? run + " matches “" + q + "”" : run + " yet";
  return noRuns("list-filter", "No run matches", asked + ". Clear the filters to see every run.",
    h("a", { class: "btn", href: base + listPath({ q: "", job: "", branch: "", status: "" }).slice(1), "data-nav": "" }, "Clear filters"));
}
