import { api } from "../../lib/api.js";
import { h } from "../../lib/dom.js";
import { when } from "../../lib/format.js";
import { visibilities } from "../../lib/organization.js";
import { base } from "../../lib/state.js";

export const nav = { label: "Home", order: 0 };
export const view = "organization";

// An organization's home: the repositories the viewer may read, and what the viewer may do here.
export default async function show({ org, main, can }) {
  const list = (await api("GET", "/orgs/" + org.slug + "/repos")).repositories;
  const repoRow = (r) => h("tr", {}, h("td", {}, h("a", { href: base + org.slug + "/" + r.slug, "data-nav": "" }, r.name), r.description ? h("p", {}, r.description) : null),
    h("td", {}, h("code", {}, r.slug)), h("td", {}, h("span", { class: "chip", title: visibilities[r.visibility].title }, visibilities[r.visibility].name)), h("td", {}, when(r.createdAt)));
  main.append(h("h1", {}, org.name), h("p", {}, h("code", {}, org.slug), org.acceptsJoinRequests ? " · accepts join requests" : ""),
    h("section", { class: "panel" }, h("div", { class: "row" }, h("h3", {}, "Repositories"),
      can("repos.create") ? h("a", { class: "button small primary", href: base + org.slug + "/new", "data-nav": "" }, "New repository") : null),
      list.length ? h("table", {}, h("thead", {}, h("tr", {}, h("th", {}, "Repository"), h("th", {}, "Slug"), h("th", {}, "Visibility"), h("th", {}, "Created"))), h("tbody", {}, list.map(repoRow)))
        : h("p", {}, can("repos.create") ? "No repositories you may read yet. Make the first." : "No repositories you may read yet.")),
    h("section", { class: "panel" }, h("h3", {}, "What you may do here"), h("div", { class: "perms" }, org.viewerPermissions.map((p) => h("code", {}, p)))));
}
