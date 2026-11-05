import { api } from "../../lib/api.js";
import { h } from "../../lib/dom.js";
import { plural, since } from "../../lib/format.js";
import { icon } from "../../lib/icons.js";
import { pageHeading } from "../../lib/layout.js";
import { visibilities } from "../../lib/organization.js";
import { base } from "../../lib/state.js";

// mains are how a repository's main stands by the annotations on its head, as its Status and its sidebar dot say it:
// the word and the tone; one without pipelines has no dot.
export const mains = {
  failing: { word: "Failing", tone: "danger" },
  running: { word: "Running", tone: "info" },
  passing: { word: "Passing", tone: "success" },
  none: { word: "No pipelines", tone: "neutral" },
};

// The Tag each visibility shows: its icon and tone.
const visibilityTags = { private: { icon: "lock", tone: "warning" }, internal: { icon: "building-2", tone: "neutral" } };

// nav puts Repositories in the sidebar, with the repositories the viewer may read under it, each with main's status as
// its dot.
export const nav = { label: "Repositories", order: 20, icon: "folder-git-2",
  items: ({ org }) => api("GET", "/orgs/" + org.slug + "/repos").then((d) => ({ whole: true, count: d.repositories.length,
    items: d.repositories.map((r) => {
      const main = mains[r.summary?.main] || mains.none;
      return { key: r.slug, label: r.slug, href: base + org.slug + "/" + r.slug, code: true, icon: "folder-git-2",
        dot: main === mains.none ? null : main.tone, dotLabel: "main " + main.word.toLowerCase() };
    }) })) };
export const view = "organization";
export const crumbs = [{ label: "Repositories" }];

// heading is the page's title, what it holds and New repository, for a viewer holding repos.create.
export const heading = ({ org, can }) => pageHeading({ title: "Repositories",
  description: "Every repository " + org.name + " hosts. Push to one and Cobranch runs its pipelines, reviews the push and keeps the branch status current.",
  actions: can("repos.create") ? [h("a", { class: "btn btn-primary", href: base + org.slug + "/new", "data-nav": "" }, icon("plus"), "New repository")] : [] });

// The organization's repositories that the viewer may read, as a table under its filters.
export default async function show({ org, main, can }) {
  const list = (await api("GET", "/orgs/" + org.slug + "/repos")).repositories;
  main.replaceChildren(...(list.length ? repositoriesTable(org, list) : [noRepositories(org, can)]));
}

// noRepositories is the card a viewer who reads no repository sees, with New repository for one who may make one.
const noRepositories = (org, can) => h("div", { class: "card" }, h("div", { class: "empty" }, h("div", { class: "empty-icon" }, icon("folder-git-2")),
  h("h2", { class: "empty-title" }, "No repositories yet"),
  h("p", { class: "empty-description" }, can("repos.create") ? "Make " + org.name + "'s first repository and push to it." : "None of " + org.name + "'s repositories is shared with you yet."),
  can("repos.create") ? h("a", { class: "btn btn-primary", href: base + org.slug + "/new", "data-nav": "" }, icon("plus"), "New repository") : null));

// lastPush is when a repository's last push was recorded, as the table sorts it: never pushed last.
const lastPush = (r) => (r.summary?.lastPushAt ? Date.parse(r.summary.lastPushAt) : 0);

// The orders the sort button steps through, each with its label and how it compares two repositories.
const orders = [
  { label: "Last push", compare: (a, b) => lastPush(b) - lastPush(a) || a.slug.localeCompare(b.slug) },
  { label: "Name", compare: (a, b) => a.slug.localeCompare(b.slug) },
];

// repositoriesTable is the repositories in list as Filters over a Table: by its last push first, or by name, filtered
// by slug, name and description.
export function repositoriesTable(org, list) {
  const rows = new Map(list.map((r) => [r, repositoryRow(org, r)]));
  const tbody = h("tbody", {});
  const none = h("p", { class: "cell-muted", hidden: true }, "No repository matches.");
  const table = h("table", { class: "table repositories-table" }, h("thead", {}, h("tr", {}, h("th", {}, "Repository"), h("th", {}, "Visibility"), h("th", {}, "Default branch"),
    h("th", {}, "Open branches"), h("th", {}, "Status"), h("th", {}, "Last push"))), tbody);
  let order = 0;
  const sort = h("button", { class: "btn btn-sm", type: "button", title: "Change the order", onclick: () => { order = (order + 1) % orders.length; draw(); } });
  const filter = h("input", { type: "search", placeholder: "Filter repositories", "aria-label": "Filter repositories", oninput: () => draw() });
  const draw = () => {
    const q = filter.value.trim().toLowerCase();
    const shown = [...rows.keys()].filter((r) => !q || [r.slug, r.name, r.description].some((t) => t.toLowerCase().includes(q))).sort(orders[order].compare);
    tbody.replaceChildren(...shown.map((r) => rows.get(r)));
    sort.replaceChildren(icon("arrow-up-down"), orders[order].label);
    table.hidden = !shown.length;
    none.hidden = !!shown.length;
  };
  draw();
  return [h("div", { class: "filters" }, h("div", { class: "input" }, icon("search"), filter), sort, h("span", { class: "filters-count" }, plural(list.length, "repository").replace(/ys$/, "ies"))),
    table, none];
}

// repositoryRow is one repository's row: its slug over its description, its visibility, its default branch, its open
// branches and how many are ready, main's status and when its last push was recorded.
function repositoryRow(org, r) {
  const s = r.summary || { openBranches: 0, readyBranches: 0, main: "none" };
  const main = mains[s.main] || mains.none;
  const tag = visibilityTags[r.visibility];
  return h("tr", {},
    h("td", { class: "cell-two-lines" }, icon("folder-git-2"), h("code", {}, h("a", { href: base + org.slug + "/" + r.slug, "data-nav": "" }, r.slug)),
      r.description ? h("span", {}, r.description) : null),
    h("td", {}, h("span", { class: "tag tone-" + tag.tone, title: visibilities[r.visibility].title }, icon(tag.icon), visibilities[r.visibility].name)),
    h("td", { class: "cell-mono" }, "main"),
    h("td", {}, s.openBranches + (s.readyBranches ? " · " + s.readyBranches + " ready" : "")),
    h("td", {}, h("span", { class: "badge-quiet tone-" + main.tone }, main.word)),
    h("td", { class: "cell-muted" }, s.lastPushAt ? since(s.lastPushAt) : "never"));
}
