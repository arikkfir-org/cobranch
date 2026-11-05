import { api } from "../../lib/api.js";
import { h } from "../../lib/dom.js";
import { when } from "../../lib/format.js";
import { visibilities } from "../../lib/organization.js";
import { base } from "../../lib/state.js";

// nav puts Repositories in the sidebar, with the repositories the viewer may read under it.
export const nav = { label: "Repositories", order: 20, icon: "folder-git-2",
  items: ({ org }) => api("GET", "/orgs/" + org.slug + "/repos").then((d) => ({ whole: true, count: d.repositories.length,
    items: d.repositories.map((r) => ({ key: r.slug, label: r.slug, href: base + org.slug + "/" + r.slug, code: true, icon: "folder-git-2" })) })) };
export const view = "organization";
export const crumbs = [{ label: "Repositories" }];

// The organization's repositories that the viewer may read.
export default async function show({ org, main, can }) {
  const list = (await api("GET", "/orgs/" + org.slug + "/repos")).repositories;
  main.replaceChildren(h("h1", {}, "Repositories"), repositoriesPanel(org, can, list));
}

// repositoriesPanel lists repositories the viewer may read, with the button that makes one for a viewer holding
// repos.create.
export function repositoriesPanel(org, can, list) {
  const repoRow = (r) => h("tr", {}, h("td", {}, h("a", { href: base + org.slug + "/" + r.slug, "data-nav": "" }, r.name), r.description ? h("p", {}, r.description) : null),
    h("td", {}, h("code", {}, r.slug)), h("td", {}, h("span", { class: "chip", title: visibilities[r.visibility].title }, visibilities[r.visibility].name)), h("td", {}, when(r.createdAt)));
  return h("section", { class: "panel" }, h("div", { class: "row" }, h("h3", {}, "Repositories"),
    can("repos.create") ? h("a", { class: "button small primary", href: base + org.slug + "/new", "data-nav": "" }, "New repository") : null),
  list.length ? h("table", {}, h("thead", {}, h("tr", {}, h("th", {}, "Repository"), h("th", {}, "Slug"), h("th", {}, "Visibility"), h("th", {}, "Created"))), h("tbody", {}, list.map(repoRow)))
    : h("p", {}, can("repos.create") ? "No repositories you may read yet. Make the first." : "No repositories you may read yet."));
}
