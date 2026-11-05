import { api } from "../../lib/api.js";
import { agentPage, newAgent, showAgent } from "../../lib/agent.js";
import { tabPages } from "../../lib/assets.js";
import { agentSubject } from "../../lib/chat.js";
import { h } from "../../lib/dom.js";
import { icon } from "../../lib/icons.js";
import { pageHeading } from "../../lib/layout.js";
import { go } from "../../lib/router.js";
import { setAgents } from "../../lib/state.js";
import { tabBar, tabsOf } from "../../lib/tabs.js";
import { showTask } from "../../lib/task.js";

// Agents: one page with tabs, each a module of pages/org/agents/ (lib/tabs.js): the agents (its index.js) at
// /<organization>/agents, what they did at agents/activity and what they spent at agents/quotas; one agent's page
// (lib/agent.js) at agents/<its ID>, and one of its tasks (lib/task.js) at agents/<its ID>/tasks/<number>.
export const nav = { label: "Agents", order: 50, icon: "bot", items: agentItems };

// agentID is whether a part of the route after agents/ names an agent, by its ID, rather than a tab.
const agentID = (part) => /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i.test(part || "");
// tabOf is the tab module at a part of the route after agents/, whether or not the viewer may open it.
const tabOf = (part) => (tabPages.get("org/agents") || new Map()).get(part || "");
// names are the agents' names by ID, as the sidebar last read them, for an agent page's breadcrumb before it loads.
const names = new Map();

// taskOf is the number of the task the parts of the route after an agent's ID name (tasks/<number>), or null.
const taskOf = (rest) => rest && rest[0] === "tasks" && /^\d+$/.test(rest[1] || "") ? rest[1] : null;

export const crumbs = ({ root, sub, rest }) => {
  if (!sub) return [{ label: "Agents" }];
  const agents = { label: "Agents", href: root + "/agents" };
  if (!agentID(sub)) return [agents, { label: tabOf(sub)?.tab?.label || sub }];
  const agent = { label: names.get(sub.toLowerCase()) || "Agent", href: root + "/agents/" + sub.toLowerCase() };
  return taskOf(rest) ? [agents, agent, { label: "#" + taskOf(rest) }] : [agents, { label: agent.label }];
};
export const view = (after) => agentID(after[0]) ? (taskOf(after.slice(1)) ? "organization.agent-task" : "organization.agent") : tabOf(after[0])?.view || "organization.agents";
// subject is the viewer's conversation with an agent, on the agent's page; the tabs keep the organization's.
export const subject = ({ org, can, sub }) => agentID(sub) ? api("GET", "/orgs/" + org.slug + "/agents/" + encodeURIComponent(sub.toLowerCase())).then((a) => agentSubject(org, a, can), () => null) : null;

// heading is the Agents page's title, what it holds, New agent for a viewer holding agents.create, and the tabs; an
// agent's page draws its own.
export const heading = ({ org, can, sub, root }) => {
  if (agentID(sub)) return null;
  const head = pageHeading({ title: "Agents",
    description: "The agents " + org.name + " runs: what each one is for, the model it runs on, and what it is doing now. Give one a task and it works on it.",
    actions: can("agents.create") ? [h("button", { class: "btn btn-primary", type: "button", onclick: () => newAgent(org) }, icon("plus"), "New agent")] : [] });
  head.append(tabBar(tabsOf("org/agents", can), sub || "", (p) => root + "/agents" + (p ? "/" + p : ""), { org, can }, "Agents"));
  return head;
};

// The tab the route names draws under the heading, or the agent its ID names, or one of its tasks; anything else opens
// the agents.
export default function show({ org, main, can, sub, rest, head }) {
  if (agentID(sub) && taskOf(rest)) return showTask(org, main, can, sub.toLowerCase(), taskOf(rest));
  if (agentID(sub)) return rest.length ? go("/" + org.slug + "/agents/" + sub.toLowerCase(), true) : showAgent(org, main, can, sub.toLowerCase());
  const shown = tabsOf("org/agents", can).find(([p]) => p === (sub || ""));
  if (!shown) return go("/" + org.slug + "/agents", true);
  return shown[1].default({ org, main, can, rest, actions: head.querySelector(".page-header-actions") });
}

// agentItems are the organization's agents as the sidebar lists them, each marked while one of its tasks runs; it
// keeps their IDs too, for the conversation pane to tell an agent's message from a person's.
async function agentItems({ org }) {
  const [agents, running] = await Promise.all([api("GET", "/orgs/" + org.slug + "/agents").then((d) => d.agents),
    api("GET", "/orgs/" + org.slug + "/tasks?state=running&limit=100").then((d) => new Set(d.tasks.map((t) => t.agent.id)), () => new Set())]);
  setAgents(new Set(agents.map((a) => a.id)));
  agents.forEach((a) => names.set(a.id, a.name));
  return { whole: true, count: agents.length, items: agents.map((a) => ({ ...agentItem(org, a),
    dot: running.has(a.id) ? "ai" : null, dotLabel: running.has(a.id) ? "Working" : null })) };
}

// agentItem is agent a as a sub item of the sidebar's Agents: it opens the agent's page.
export const agentItem = (org, a) => ({ key: a.id, label: a.name, href: agentPage(org, a.id), avatar: "agent" });
