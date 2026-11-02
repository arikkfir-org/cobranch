import { repositoryFiles } from "../../lib/code.js";
import { h } from "../../lib/dom.js";

export const nav = { label: "Code", order: 0 };
export const view = "repository.code";

// The repository's code: its files at main, how to clone it, and what the viewer may do here.
export default async ({ org, repo, path, at, rest, can }) => [...await repositoryFiles(org, repo, path, at, false, rest, can),
  h("section", { class: "panel" }, h("h3", {}, "What you may do here"), h("div", { class: "perms" }, repo.viewerPermissions.map((p) => h("code", {}, p))))];
