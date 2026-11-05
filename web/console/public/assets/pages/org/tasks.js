import { emptyState } from "../../lib/agent.js";
import { api } from "../../lib/api.js";
import { go } from "../../lib/router.js";

// Tasks: the addresses tasks had before each moved under its agent (lib/task.js), kept for the links that name them,
// a branch's AI review's among them. /<organization>/tasks/<number> opens the task under its agent, tasks?agent=<ID> the
// agent's page, and tasks the Agents page.
export const crumbs = ({ root, sub }) => [{ label: "Agents", href: root + "/agents" }, ...(sub ? [{ label: "#" + sub }] : [])];

export default async function show({ org, main, sub }) {
  if (!sub) {
    const agent = new URLSearchParams(location.search).get("agent");
    return go("/" + org.slug + "/agents" + (agent ? "/" + encodeURIComponent(agent) : ""), true);
  }
  let t;
  try {
    t = await api("GET", "/orgs/" + org.slug + "/tasks/" + encodeURIComponent(sub));
  } catch (err) {
    if (err.status !== 404 && err.status !== 400) throw err;
    return main.replaceChildren(emptyState("sparkles", "No such task", "There is no task #" + sub + " in " + org.name + ", or you may not see it."));
  }
  return go("/" + org.slug + "/agents/" + t.agent.id + "/tasks/" + t.number, true);
}
