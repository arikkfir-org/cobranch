import { api, apiPage } from "../../lib/api.js";
import { branchSubject } from "../../lib/chat.js";
import { empty, refPicker } from "../../lib/code.js";
import { h } from "../../lib/dom.js";
import { banner } from "../../lib/forms.js";
import { plural, shortSha } from "../../lib/format.js";
import { commitListRow } from "../../lib/history.js";
import { icon } from "../../lib/icons.js";
import { go } from "../../lib/router.js";
import { base } from "../../lib/state.js";

export const under = "";
export const crumbs = () => [{ label: "Commits" }];
// subject is the branch's conversation, on its history; a tag's or a commit's has none.
export const subject = ({ org, repo, path, at, rest, can }) => {
  const ref = rest.join("/") || "main";
  return api("GET", path + "/branches/" + encodeURIComponent(ref)).then(() => branchSubject(org, repo, path, at, ref, can), () => null);
};
export default ({ path, at, rest, fail }) => repositoryHistory(path, at, rest.join("/") || "main", fail);

// repositoryHistory is the history of a branch, a tag or a commit, as the Commits screen draws it: under the ref
// picker, Every parent or First parent, a filter of the commits' messages and how many there are, the commits
// nearest first, a page at a time, in a card per day they were committed, beside a graph of their lines; along first
// parents, the graph is one line, and an installation without a Git server follows first parents alone. Filtered
// (?q=), the commits that remain draw no graph: their lines would join commits that aren't each other's parents. With
// a path (?path=), it is the commits that changed what is there, through every parent, which takes the Git server,
// without the graph too.
async function repositoryHistory(path, at, ref, fail) {
  const [branches, tags] = await Promise.all([api("GET", path + "/branches").then((d) => d.branches), api("GET", path + "/tags").then((d) => d.tags)]);
  if (!branches.length) return [h("section", { class: "card empty-centred" }, empty("git-commit-horizontal", "No commits yet", ["Push ", h("code", {}, "main"), " to start its history."]))];
  const params = new URLSearchParams(location.search);
  const file = params.get("path") || "";
  let first = !file && params.get("parents") === "first";
  let query = params.get("q") || "";
  const named = /^[0-9a-f]{40,64}$/.test(ref) ? shortSha(ref) : ref;
  const link = (r, firstOnly) => {
    const kept = new URLSearchParams({ ...(firstOnly ? { parents: "first" } : {}), ...(file ? { path: file } : {}), ...(query ? { q: query } : {}) });
    return at.history(r) + (kept.size ? "?" + kept : "");
  };
  const parents = h("div", { class: "segmented commits-parents", role: "group", "aria-label": "Parents" },
    [[false, "Every parent"], [true, "First parent"]].map(([firstOnly, label]) => h("button", { type: "button", "aria-pressed": String(first === firstOnly),
      onclick: () => go(link(ref, firstOnly).slice(base.length - 1)) }, label)));
  const input = h("input", { type: "search", value: query, placeholder: "Filter commits", maxlength: "200", "aria-label": "Filter commits by their messages" });
  const count = h("span", { class: "commits-count" });
  const toolbar = h("div", { class: "commits-toolbar" }, refPicker(branches, tags, ref, (r) => link(r, first)), parents, h("span", { class: "spacer" }),
    h("label", { class: "input commits-filter" }, icon("search"), input), count);
  const heading = file ? h("div", { class: "commits-path" }, h("h2", { class: "commits-path-title" }, icon("folder"), "History of", h("code", {}, file)),
    h("p", { class: "commits-path-description" }, "Commits on " + named + " that changed it, newest first. Every parent is followed.")) : null;
  const note = h("div", { hidden: true });
  const days = h("div", { class: "commits-days" });
  const older = h("button", { class: "btn", type: "button", hidden: true }, "Older");
  const more = h("div", { class: "commits-more" }, older);

  let draw, last, next, shown, generation = 0, gitless = false;
  const reset = () => {
    draw = file || query ? null : historyGraph(days);
    last = null;
    next = null;
    shown = 0;
    days.replaceChildren();
    days.classList.toggle("commits-graphed", !!draw);
  };
  const add = (commits) => {
    for (const c of commits) {
      const day = dayOf(c.committer.at);
      if (!last || last.day !== day) {
        const header = h("div", { class: "card-header" }, draw ? draw.through() : null, h("h2", { class: "card-title" }, dayName(c.committer.at)));
        last = { day, list: h("ul", { class: "list" }) };
        days.append(h("section", { class: "card commits-day" }, header, last.list));
      }
      const row = commitListRow(at)(c);
      if (draw) row.prepend(draw(first ? { ...c, parents: c.parents.slice(0, 1) } : c));
      last.list.append(row);
    }
    shown += commits.length;
  };
  const start = () => path + "/commits?" + new URLSearchParams({ ref, parents: first ? "first" : "all", ...(file ? { path: file } : {}), ...(query ? { q: query } : {}) });
  // load adds the page at from, unless the history was drawn afresh meanwhile.
  const load = async (from) => {
    const drawing = generation;
    older.disabled = true;
    try {
      const page = await apiPage(from);
      if (drawing !== generation) return;
      add(page.data.commits);
      next = page.next;
      older.hidden = !next;
      if (page.data.total !== undefined) count.textContent = plural(page.data.total, "commit") + " on " + named + (query ? (page.data.total === 1 ? " matches" : " match") : "");
    } finally {
      older.disabled = false;
    }
  };
  older.onclick = () => load(next).catch(fail);

  // show draws the history from its first page, saying why when there is none, unless it was drawn afresh meanwhile.
  const show = async () => {
    const drawing = ++generation;
    reset();
    count.textContent = "";
    // The switch shows over a history it changes: not a path's or a filtered one, nor one without a Git server.
    parents.hidden = !!file || !!query || gitless;
    try {
      await load(start());
    } catch (err) {
      if (drawing !== generation) return;
      if (err.code === "git.unavailable" && !first && !file) {
        first = true;
        gitless = true;
        note.replaceChildren(banner("info", null, "This installation runs no Git server, so history follows first parents alone."));
        note.hidden = false;
        return show();
      }
      older.hidden = true;
      parents.hidden = true;
      if (err.code === "git.unavailable") days.append(none("server", "No Git server", file ? "A path's history needs the Git server, which this installation doesn't run."
        : "A tag's history needs the Git server, which this installation doesn't run: pick a branch or a commit."));
      else if (err.code === "ref.not_found" || err.code === "ref.invalid" || err.code === "branch.not_found") days.append(none("git-branch", "No such branch, tag or commit",
        "This repository has nothing named " + ref + ". Pick a ref from the list, or push the branch."));
      else if (err.code === "q.invalid") days.append(none("search", "Can't filter by that", "A filter is up to 200 characters on one line."));
      else throw err;
      return;
    }
    if (drawing !== generation || shown || next) return;
    if (query) days.append(none("search", "No commit matches “" + query + "”", "Nothing on " + named + " says that in its message. Try fewer words, or clear the filter.",
      h("button", { class: "btn", type: "button", onclick: () => { input.value = ""; input.dispatchEvent(new Event("input")); } }, "Clear filter")));
    else if (file) days.append(none("folder", "No commit changed " + file, "Nothing on " + named + " touched this path. Pick another ref, or a directory above it."));
    else days.append(none("git-commit-horizontal", "No commits yet", [h("code", {}, named), " is at no commit a push recorded."]));
  };

  // Typing filters the history once the viewer pauses, keeping the filter in the page's address.
  let typing;
  input.addEventListener("input", () => {
    clearTimeout(typing);
    typing = setTimeout(() => {
      if (input.value.trim() === query) return;
      query = input.value.trim();
      const kept = new URLSearchParams(location.search);
      if (query) kept.set("q", query);
      else kept.delete("q");
      history.replaceState(null, "", location.pathname + (kept.size ? "?" + kept : ""));
      show().catch(fail);
    }, 300);
  });
  await show();
  return [toolbar, heading, note, days, more].filter(Boolean);
}

// none is a card holding the Empty state of a history that shows no commits, with its action, if any.
const none = (glyph, title, description, action) => h("section", { class: "card empty-centred" }, empty(glyph, title, description, action));

// dayOf is the day, in the viewer's time zone, of a moment; dayName names it as a day's card does: Today, Yesterday,
// or its date, with its year when that isn't this one.
const dayOf = (at) => new Date(at).toDateString();
function dayName(at) {
  const d = new Date(at), now = new Date();
  if (d.toDateString() === now.toDateString()) return "Today";
  if (d.toDateString() === new Date(now.getFullYear(), now.getMonth(), now.getDate() - 1).toDateString()) return "Yesterday";
  return d.toLocaleDateString(undefined, d.getFullYear() === now.getFullYear() ? { month: "short", day: "numeric" } : { dateStyle: "medium" });
}

// historyGraph draws the lines of history beside each commit of its pages, in order, a lane per line, keeping its
// lanes from page to page: each lane waits for a commit, whose dot sits in the first lane that waited for it while
// the others bend into it; its first parent continues that lane, and its other parents bend out to the lanes that
// wait for them or open new ones. A commit no page shows leaves its lane running on, through the next day's header.
// Lane 0 is history's main line, the others take the status colours in turn; days, whose cards it widens to make
// room for every lane, holds the graph's width.
function historyGraph(days) {
  const x = (i) => 24 + 16 * i;
  const tone = (i) => "lane-" + (i ? 1 + ((i - 1) % 4) : 0);
  const line = (i, top, bottom) => h("span", { class: "graph-line " + tone(i), style: `left:${x(i)}px;top:${top};bottom:${bottom}` });
  // bend is a diagonal in lane i's colour from lane a down to lane b, 16px lower: from the row's middle when below,
  // or to it.
  const bend = (i, a, b, below) => {
    const left = Math.min(x(a), x(b)), width = Math.abs(x(a) - x(b));
    const el = document.createElementNS("http://www.w3.org/2000/svg", "svg");
    el.setAttribute("class", "graph-bend " + tone(i));
    el.setAttribute("width", String(width));
    el.setAttribute("height", "16");
    el.setAttribute("style", `left:${left}px;top:${below ? "50%" : "calc(50% - 16px)"}`);
    const l = document.createElementNS("http://www.w3.org/2000/svg", "line");
    for (const [k, v] of Object.entries({ x1: x(a) - left, y1: 0, x2: x(b) - left, y2: 16 })) l.setAttribute(k, String(v));
    el.append(l);
    return el;
  };
  const gutter = h("span", { class: "commits-graph", "aria-hidden": "true" });
  let lanes = [], width = 0, newest = true;
  const widen = (n) => {
    if (32 + 16 * n <= width) return;
    width = 32 + 16 * n;
    days.style.setProperty("--commits-gutter", Math.max(64, width) + "px");
  };
  widen(1);
  const draw = (c) => {
    const before = lanes;
    const waited = before.flatMap((s, i) => (s === c.sha ? [i] : []));
    let col = waited.length ? waited[0] : before.indexOf(null);
    if (col < 0) col = before.length;
    const after = before.map((s) => (s === c.sha ? null : s));
    after[col] = c.parents[0] || null;
    const opened = c.parents.slice(1).map((p) => {
      let j = after.indexOf(p);
      const fresh = j < 0;
      if (fresh) {
        j = after.indexOf(null);
        if (j < 0) j = after.length;
        after[j] = p;
      }
      return [j, fresh];
    });
    while (after.length && after[after.length - 1] === null) after.pop();
    lanes = after;
    widen(Math.max(before.length, after.length, col + 1));
    const parts = [];
    before.forEach((s, i) => { if (s !== null && s !== c.sha) parts.push(line(i, "0", "0")); });
    waited.forEach((i) => parts.push(...(i === col ? [line(i, "0", "50%")] : [line(i, "0", "calc(50% + 16px)"), bend(i, i, col, false)])));
    if (after[col]) parts.push(line(col, "50%", "0"));
    opened.forEach(([j, fresh]) => parts.push(bend(j, col, j, true), ...(fresh ? [line(j, "calc(50% + 16px)", "0")] : [])));
    parts.push(h("span", { class: "graph-dot " + (newest ? "graph-dot-newest" : col ? tone(col) : "graph-dot-main"), style: `left:${x(col)}px` }));
    newest = false;
    const el = gutter.cloneNode();
    el.append(...parts);
    return el;
  };
  // through is the graph across a day's header: the lanes that wait run through it.
  draw.through = () => {
    const el = gutter.cloneNode();
    el.append(...lanes.flatMap((s, i) => (s !== null ? [line(i, "0", "0")] : [])));
    return el;
  };
  return draw;
}
