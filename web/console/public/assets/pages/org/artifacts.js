import { api } from "../../lib/api.js";
import { artifactSubject, knowTitle, showArtifact, titleOf } from "../../lib/artifact.js";
import { artifactHref, audienceBadge, emptyCard, filterMenu, formats, kindOptions, kinds, listedOnly, nameCell, producer, search, updated } from "../../lib/artifacts.js";
import { h } from "../../lib/dom.js";
import { icon } from "../../lib/icons.js";
import { pageHeader } from "../../lib/layout.js";
import { go } from "../../lib/router.js";

// nav puts Artifacts in the sidebar after Agents, with the artifacts the viewer may open under it.
export const nav = { label: "Artifacts", order: 60, icon: "file", items: artifactItems };
export const crumbs = ({ root, sub }) => sub ? [{ label: "Artifacts", href: root + "/artifacts" }, { label: titleOf(sub) }] : [{ label: "Artifacts" }];
// subject is, on an artifact's page, the viewer's conversation about its repository, where they ask an agent to revise it.
export const subject = ({ org, can, sub }) => (sub ? artifactSubject(org, can, sub) : null);

// The organization's artifacts the viewer may list or read, or, under /artifacts/<id>, one of them.
export default ({ org, main, can, sub }) => (sub ? showArtifact(org, main, can, sub) : showArtifacts(org, main));

// artifactItems are the organization's artifacts the viewer may open, newest change first, as the sidebar lists them.
async function artifactItems({ org }) {
  const list = (await api("GET", "/orgs/" + org.slug + "/artifacts")).artifacts;
  list.forEach(knowTitle);
  return { whole: true, count: list.length, items: list.filter((a) => !listedOnly(a)).map((a) => ({ key: a.id, label: a.title, href: artifactHref(org, a), code: true, icon: "file" })) };
}

// listPath is the page's path with the filters query names.
const listPath = (org, query) => "/" + org.slug + "/artifacts" + (query.size ? "?" + query : "");

// showArtifacts draws the organization's artifacts (Figma "Artifacts"): those the viewer may list or read, newest
// change first, filtered by the page's query (repository, kind, mine) through the API and by title as typed.
async function showArtifacts(org, main) {
  const query = new URLSearchParams(location.search);
  const params = new URLSearchParams();
  if (query.get("repository")) params.set("repositoryId", query.get("repository"));
  if (query.get("kind")) params.set("kind", query.get("kind"));
  if (query.get("mine")) params.set("mine", "true");
  const [list, repos] = await Promise.all([api("GET", "/orgs/" + org.slug + "/artifacts" + (params.size ? "?" + params : "")).then((d) => d.artifacts),
    api("GET", "/orgs/" + org.slug + "/repos").then((d) => d.repositories)]);
  list.forEach(knowTitle);
  const repoCount = new Set(list.filter((a) => a.repository).map((a) => a.repository.id)).size;
  const description = list.length + (list.length === 1 ? " artifact" : " artifacts") + " across " + repoCount + (repoCount === 1 ? " repository" : " repositories") + " · what agents produced for you and your organization";
  const header = pageHeader({ title: "Artifacts", description });
  if (!list.length && !query.size) {
    return main.replaceChildren(header, emptyCard("No artifacts yet", "Ask an agent in a conversation for a document, a diagram, a report or a page, and it shows here."));
  }
  const refilter = (q) => go(listPath(org, q), true);
  const tbody = h("tbody", {});
  const none = h("p", { class: "cell-muted", hidden: true }, "No artifact matches.");
  const filter = search("Filter artifacts", () => draw());
  const table = h("table", { class: "table" }, h("thead", {}, h("tr", {}, ["Name", "Repository", "Produced by", "Access", "Updated"].map((t) => h("th", {}, t)))), tbody);
  const row = (a) => h("tr", {}, nameCell(org, a, "file", [formats[a.mediaType], kinds[a.kind].word].filter(Boolean).join(" · ")),
    h("td", {}, a.repository ? h("code", { class: "cell-mono" }, a.repository.slug) : h("span", { class: "cell-muted" }, listedOnly(a) ? "" : "None")),
    h("td", {}, producer(a)), h("td", {}, audienceBadge(a)), h("td", { class: "cell-muted" }, a.updatedAt ? updated(a.updatedAt) : ""));
  const rows = new Map(list.map((a) => [a, row(a)]));
  const count = h("span", { class: "filters-count" });
  const draw = () => {
    const shown = list.filter(filter.matches);
    tbody.replaceChildren(...shown.map((a) => rows.get(a)));
    table.hidden = !shown.length;
    none.hidden = !!shown.length;
    count.textContent = shown.length === 1 ? "1 artifact" : shown.length + " artifacts";
  };
  const mine = (on) => {
    const q = new URLSearchParams(location.search);
    if (on) q.set("mine", "1");
    else q.delete("mine");
    refilter(q);
  };
  const scope = h("div", { class: "segmented artifact-scope", role: "group", "aria-label": "Whose" },
    h("button", { type: "button", "aria-pressed": String(!query.get("mine")), onclick: () => mine(false) }, "All"),
    h("button", { type: "button", "aria-pressed": String(!!query.get("mine")), onclick: () => mine(true) }, "Mine"));
  const repoOptions = [{ value: "", word: "Any repository" }, ...repos.map((r) => ({ value: r.id, word: r.slug, lead: icon("folder-git-2") }))];
  draw();
  main.replaceChildren(header, h("div", { class: "filters" }, filter.el, h("span", { class: "artifact-spacer" }),
    filterMenu("repository", "folder-git-2", "Any repository", repoOptions, refilter), scope,
    filterMenu("kind", "file", "Any kind", kindOptions("Any kind"), refilter), count), table, none);
}
