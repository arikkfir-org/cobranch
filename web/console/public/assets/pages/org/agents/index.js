import { api } from "../../../lib/api.js";
import { agentPage, emptyState, spendReport, statusBadge, taskPage, taskTitle } from "../../../lib/agent.js";
import { h } from "../../../lib/dom.js";
import { plural } from "../../../lib/format.js";
import { avatar, icon } from "../../../lib/icons.js";
import { recount } from "../../../lib/tabs.js";

export const tab = { label: "Agents", order: 0 };
export const count = ({ org }) => api("GET", "/orgs/" + org.slug + "/agents").then((d) => d.agents.length);
export const view = "organization.agents";
export default ({ org, main }) => showAgents(org, main);

// showAgents lists the organization's agents (Agents): what each is for, the model it runs on, whether it works and on
// what, and how many tasks it was given this month, filtered by name or what it is for.
async function showAgents(org, main) {
  const [agents, working, report] = await Promise.all([
    api("GET", "/orgs/" + org.slug + "/agents").then((d) => d.agents),
    api("GET", "/orgs/" + org.slug + "/tasks?" + new URLSearchParams([["state", "running"], ["state", "waiting"], ["limit", "100"]])).then((d) => d.tasks, () => []),
    spendReport(org).catch(() => null),
  ]);
  recount("", agents.length);
  // on is the task each agent works on now, the newest when it works on more than one.
  const on = new Map();
  working.forEach((t) => { if (!on.has(t.agent.id)) on.set(t.agent.id, t); });
  const monthly = new Map((report ? report.agents : []).map((s) => [s.agent.id, s.tasks]));
  const row = (a) => {
    const t = on.get(a.id);
    return h("tr", { "data-filter": (a.name + " " + a.description).toLowerCase() },
      h("td", {}, h("a", { class: "agents-agent", href: agentPage(org, a.id), "data-nav": "" }, avatar(a.name, true, 32),
        h("span", { class: "cell-two-lines" }, h("span", {}, a.name), a.description ? h("span", {}, a.description) : ""))),
      h("td", { class: a.model ? "cell-mono" : "cell-muted" }, a.model || "Not set"),
      h("td", {}, statusBadge(!!t)),
      h("td", {}, t ? h("a", { href: taskPage(org, t), "data-nav": "" }, "#" + t.number + " " + taskTitle(t)) : h("span", { class: "cell-muted" }, "—")),
      h("td", { title: "Tasks it was given this month" }, report ? String(monthly.get(a.id) || 0) : "–"));
  };
  if (!agents.length) {
    return main.replaceChildren(h("div", { class: "card" }, emptyState("bot", "No agents yet", "An agent takes the tasks it is given. Make one with New agent.")));
  }
  const rows = agents.map(row);
  const table = h("table", { class: "table agents-table" }, h("thead", {}, h("tr", {}, h("th", {}, "Agent"), h("th", {}, "Model"), h("th", {}, "Status"),
    h("th", {}, "Working on"), h("th", {}, "Tasks"))), h("tbody", {}, rows));
  const none = h("p", { class: "filters-count", hidden: true }, "No agent matches.");
  const filter = h("input", { type: "search", "aria-label": "Filter agents", placeholder: "Filter agents", oninput: () => {
    const q = filter.value.trim().toLowerCase();
    const shown = rows.filter((r) => { r.hidden = !!q && !r.dataset.filter.includes(q); return !r.hidden; }).length;
    table.hidden = !shown;
    none.hidden = !!shown;
  } });
  main.replaceChildren(h("div", { class: "filters" }, h("div", { class: "input" }, icon("search"), filter),
    h("span", { class: "filters-count" }, plural(agents.length, "agent") + " · " + on.size + " working")),
  table, none);
}
