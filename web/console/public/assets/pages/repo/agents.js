import { agentPage, declaredIn, emptyState, since, spendReport } from "../../lib/agent.js";
import { api } from "../../lib/api.js";
import { h } from "../../lib/dom.js";
import { avatar, icon } from "../../lib/icons.js";
import { drawAgain, followLists, readTagged } from "../../lib/live.js";
import { popover } from "../../lib/menu.js";
import { go } from "../../lib/router.js";
import { base } from "../../lib/state.js";

// nav makes Agents one of a repository's tabs, after Issues, and count counts the agents main's /cobranch.yaml
// declares.
export const nav = { label: "Agents", order: 50 };
export const accepts = (rest) => !rest.length;
export const crumbs = ({ root }) => [{ label: "Agents", href: root + "/agents" }];
export const count = ({ org, repo }) => api("GET", "/orgs/" + org.slug + "/agents").then((d) => d.agents.filter((a) => a.repositoryId === repo.id && !a.retiredAt).length);

// The repository's agents (Figma "Repository · Agents"), which main's /cobranch.yaml declares.
export default async ({ org, repo }) => {
  const box = h("div", { class: "repo-agents-page" });
  await showAgents(org, repo, box);
  return [box];
};

// fileOf is the path of main's /cobranch.yaml among the repository's files, under the Console's root.
const fileOf = (org, repo) => org.slug + "/" + repo.slug + "/blob/main/cobranch.yaml";

// showAgents draws the repository's agents into box: the model each runs on, its image, the secrets its tasks receive,
// the permissions it declares and its tasks this month, each with a menu, then those main's file no longer declares,
// retired. Nothing here changes one: a push to main does, and Open /cobranch.yaml opens the file. It follows the
// organization's agents while they show, and draws itself again once they change.
async function showAgents(org, repo, box) {
  const path = "/orgs/" + org.slug + "/agents";
  const [list, report] = await Promise.all([readTagged(path), spendReport(org).catch(() => null)]);
  const mine = list.data.agents.filter((a) => a.repositoryId === repo.id)
    .sort((x, y) => (x.retiredAt ? 1 : 0) - (y.retiredAt ? 1 : 0) || x.name.localeCompare(y.name));
  const open = h("a", { class: "btn btn-sm", href: base + fileOf(org, repo), "data-nav": "" }, icon("file"), "Open /cobranch.yaml");
  box.replaceChildren(...(mine.length ? [h("div", { class: "filters" }, h("p", { class: "repo-agents-note" },
    "Declared in main's /cobranch.yaml, and changed only by a push to main. Each works in this repository alone, within its jobs' ceiling."), open),
  table(org, mine, report)]
    : [h("div", { class: "card" }, emptyState("bot", "No agents yet", "Declare one under agents: in main's /cobranch.yaml, and it shows here once main has it.", open))]));
  const still = () => box.isConnected;
  followLists([[path, list.tag]], still, () => drawAgain(() => showAgents(org, repo, box), still));
}

// table lists agents: what each is for, or when it was retired, the model it runs on, its image, the secrets its tasks
// receive, the permissions it declares, its tasks this month, and its menu.
function table(org, agents, report) {
  const monthly = new Map((report ? report.agents : []).map((s) => [s.agent.id, s.tasks]));
  const muted = (text) => h("td", { class: "cell-muted" }, text);
  const mono = (text, none) => text ? h("td", { class: "cell-mono" }, text) : muted(none);
  const row = (a) => h("tr", {},
    h("td", {}, h("a", { class: "agents-agent agents-agent-small", href: agentPage(org, a.id), "data-nav": "" }, avatar(a.name, true, 24),
      h("span", { class: "cell-two-lines" }, h("span", {}, a.name),
        a.retiredAt ? h("span", {}, "Retired " + since(a.retiredAt)) : a.description ? h("span", {}, a.description) : ""))),
    ...(a.retiredAt ? [muted("—"), muted("—"), muted("—"), muted("—")]
      : [mono(a.model, "Not set"), mono(a.image, "The installation's"), mono(Object.keys(a.secrets).join(", "), "—"), mono(a.permissions.join(", "), "None")]),
    h("td", {}, report ? String(monthly.get(a.id) || 0) : "–"),
    h("td", { class: "cell-actions" }, menu(org, a)));
  return h("table", { class: "table repo-agents" }, h("thead", {}, h("tr", {}, ["Agent", "Model", "Image", "Secrets", "Permissions", "Tasks"].map((t) => h("th", {}, t)),
    h("th", { "aria-label": "Actions" }))), h("tbody", {}, agents.map(row)));
}

// menu is an agent's menu: open its page, and its lines of main's /cobranch.yaml while main declares it.
function menu(org, a) {
  const items = [["Open its page", "bot", () => go("/" + org.slug + "/agents/" + a.id)],
    a.retiredAt ? null : ["Open its lines", "file", () => go(declaredIn(org, a).slice(base.length - 1))]].filter(Boolean);
  const button = h("button", { class: "icon-btn icon-btn-ghost icon-btn-sm", type: "button", "aria-haspopup": "menu", "aria-label": "More for " + a.name, title: "More for " + a.name }, icon("ellipsis"));
  button.addEventListener("click", () => popover(button, "end", (close) => items.map(([word, glyph, run]) =>
    h("button", { class: "menu-item", type: "button", role: "menuitem", onclick: () => { close(); run(); } }, icon(glyph), h("span", {}, word)))));
  return button;
}
