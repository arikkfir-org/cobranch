import { api } from "../../lib/api.js";
import { h } from "../../lib/dom.js";
import { repositoriesTable } from "./repos.js";

export const nav = { label: "Overview", order: 0, icon: "house" };
export const crumbs = [{ label: "Overview" }];
export const view = "organization";

// An organization's Overview: the repositories the viewer may read, and what the viewer may do here.
export default async function show({ org, main }) {
  const list = (await api("GET", "/orgs/" + org.slug + "/repos")).repositories;
  main.append(h("h1", {}, org.name), h("p", {}, h("code", {}, org.slug), org.acceptsJoinRequests ? " · accepts join requests" : ""),
    ...(list.length ? repositoriesTable(org, list) : []),
    h("section", { class: "panel" }, h("h3", {}, "What you may do here"), h("div", { class: "perms" }, org.viewerPermissions.map((p) => h("code", {}, p)))));
}
