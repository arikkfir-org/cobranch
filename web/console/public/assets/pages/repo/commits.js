import { annotationIcons } from "../../lib/annotations.js";
import { api, apiPage } from "../../lib/api.js";
import { branchSubject } from "../../lib/chat.js";
import { refPicker } from "../../lib/code.js";
import { h } from "../../lib/dom.js";
import { shortSha, subject as messageSubject, whenExactly, who } from "../../lib/format.js";
import { go } from "../../lib/router.js";
import { base } from "../../lib/state.js";

export const under = "tree";
export const heading = false;
export const crumbs = ({ root, rest }) => [{ label: "Commits", href: root + "/commits/" + (rest.join("/") || "main") }, { label: rest.join("/") || "main" }];
// subject is the branch's conversation, on its history; a tag's or a commit's has none.
export const subject = ({ org, repo, path, at, rest, can }) => {
  const ref = rest.join("/") || "main";
  return api("GET", path + "/branches/" + encodeURIComponent(ref)).then(() => branchSubject(org, repo, path, at, ref, can), () => null);
};
export default async ({ path, at, rest, fail }) => [await repositoryHistory(path, at, rest.join("/") || "main", fail)];

// repositoryHistory is the history of a branch, a tag or a commit, nearest first, a page at a time, under a picker of
// the repository's branches and tags: through every parent beside a graph of its lines, or along first parents
// alone, which an installation without a Git server falls back to.
async function repositoryHistory(path, at, ref, fail) {
  const [branches, tags] = await Promise.all([api("GET", path + "/branches").then((d) => d.branches), api("GET", path + "/tags").then((d) => d.tags)]);
  if (!branches.length) return h("section", { class: "panel" }, h("h3", {}, "History"), h("p", {}, "Nothing has been pushed yet: push ", h("code", {}, "main"), " to start its history."));
  let first = new URLSearchParams(location.search).get("parents") === "first";
  const panel = h("section", { class: "panel" });
  const pressed = (on, label, firstOnly) => h("button", { class: "button small", type: "button", "aria-pressed": String(on),
    onclick: () => go(at.history(ref, firstOnly).slice(base.length - 1)) }, label);
  const toggle = h("div", { class: "tabs", role: "group", "aria-label": "Parents" }, pressed(!first, "Every parent", false), pressed(first, "First parent", true));
  panel.append(h("div", { class: "row" }, h("h3", {}, "History"), h("span", { class: "spacer" }), toggle,
    refPicker(branches, tags, ref, (r) => go(at.history(r, first).slice(base.length - 1)))));
  let draw = first ? null : historyGraph();
  const row = (c) => {
    const lines = draw ? draw(c) : null;
    return h("tr", {}, lines ? h("td", { class: "graph", style: "width:" + lines.width + "px", "aria-hidden": "true" }, lines.svg) : null,
      h("td", {}, h("a", { class: "subject", href: at.commit(c.sha), "data-nav": "" }, messageSubject(c.message)),
        h("p", {}, who(c.author), " · ", whenExactly(c.author.at))), h("td", { class: "sha" }, annotationIcons(c.annotations), h("a", { href: at.commit(c.sha), "data-nav": "" }, h("code", {}, shortSha(c.sha)))));
  };
  const rows = h("tbody");
  let next = null;
  const older = h("button", { class: "button small", type: "button", hidden: true, onclick: () => load(next).catch(fail) }, "Older commits");
  async function load(from) {
    older.disabled = true;
    try {
      const page = await apiPage(from);
      rows.append(...page.data.commits.map(row));
      next = page.next;
      older.hidden = !next;
      return page.data.commits.length;
    } finally {
      older.disabled = false;
    }
  }
  const start = (firstOnly) => path + "/commits?" + new URLSearchParams({ ref, parents: firstOnly ? "first" : "all" });
  let shown;
  const needsGit = () => panel.append(h("p", {}, "A tag's history needs the Git server too: pick a branch or a commit."));
  try {
    shown = await load(start(first));
  } catch (err) {
    if (err.code === "git.unavailable" && !first) {
      first = true;
      draw = null;
      toggle.hidden = true;
      panel.append(h("p", {}, "This installation runs no Git server, so history follows first parents alone."));
      try {
        shown = await load(start(true));
      } catch (again) {
        if (again.code !== "git.unavailable") throw again;
        needsGit();
        return panel;
      }
    } else if (err.code === "git.unavailable") {
      needsGit();
      return panel;
    } else if (err.code === "ref.not_found" || err.code === "ref.invalid" || err.code === "branch.not_found") {
      panel.append(h("p", {}, "This repository has no branch, tag or commit ", h("code", {}, ref), "."));
      return panel;
    } else throw err;
  }
  if (!shown && !next) panel.append(h("p", {}, "Nothing to show yet: ", h("code", {}, /^[0-9a-f]{40,64}$/.test(ref) ? shortSha(ref) : ref), " is at no commit a push recorded."));
  panel.append(h("div", { class: "scroll" }, h("table", { class: "commits" }, rows)), h("div", {}, older));
  return panel;
}

// historyGraph draws the lines of history beside each commit of its pages, in order, a lane per line, keeping its
// lanes from page to page: each lane waits for a commit, whose dot sits in the first lane that waited for it while
// the others join it; its first parent continues that lane, and its other parents join the lanes that wait for them
// or open new ones. A commit no page shows leaves its lane running on.
function historyGraph() {
  const step = 14, x = (i) => i * step + step / 2, ns = "http://www.w3.org/2000/svg";
  let lanes = [];
  const svg = (tag, attrs) => { const el = document.createElementNS(ns, tag); for (const [k, v] of Object.entries(attrs)) el.setAttribute(k, v); return el; };
  const line = (i, x1, y1, x2, y2) => svg("line", { class: "lane-" + (i % 5), x1, y1, x2, y2 });
  return (c) => {
    const before = lanes;
    const waited = before.flatMap((s, i) => (s === c.sha ? [i] : []));
    let col = waited.length ? waited[0] : before.indexOf(null);
    if (col < 0) col = before.length;
    const after = before.map((s) => (s === c.sha ? null : s));
    after[col] = c.parents[0] || null;
    const opened = c.parents.slice(1).map((p) => {
      let j = after.indexOf(p);
      if (j < 0) {
        j = after.indexOf(null);
        if (j < 0) j = after.length;
        after[j] = p;
      }
      return j;
    });
    while (after.length && after[after.length - 1] === null) after.pop();
    lanes = after;
    const parts = [];
    before.forEach((s, i) => { if (s !== null && s !== c.sha) parts.push(line(i, x(i), 0, x(i), "100%")); });
    waited.forEach((i) => parts.push(line(i, x(i), 0, x(col), "50%")));
    if (after[col]) parts.push(line(col, x(col), "50%", x(col), "100%"));
    opened.forEach((j) => parts.push(line(j, x(col), "50%", x(j), "100%")));
    parts.push(svg("circle", { class: "lane-" + (col % 5), cx: x(col), cy: "50%", r: 4 }));
    const width = Math.max(before.length, after.length, col + 1) * step;
    const el = svg("svg", { width, height: "100%" });
    el.append(...parts);
    return { svg: el, width };
  };
}
