import { apiPage } from "../../../lib/api.js";
import { agentPage, dollars, emptyState, eventIcon, eventText, events, micros, since, taskLink, taskTitle, usd, whoAvatar } from "../../../lib/agent.js";
import { h } from "../../../lib/dom.js";
import { plural } from "../../../lib/format.js";
import { icon } from "../../../lib/icons.js";
import { drawAgain, followLists } from "../../../lib/live.js";

export const tab = { label: "Activity", order: 10 };
export const view = "organization.agents-activity";
export default ({ org, main }) => showActivity(org, main);

const day = 24 * 60 * 60 * 1000;

// newest is the API's path of the page of the organization's newest tasks, which Agents · Activity follows.
const newest = (org) => "/orgs/" + org.slug + "/tasks?limit=100";

// recentTasks are the organization's tasks queued in the last two days, newest first, read a page at a time, and the
// ETag of their first page: what happened to its tasks in the last day is in them, but for a task that ran longer than
// a day.
async function recentTasks(org) {
  const tasks = [];
  let next = newest(org), tag = null;
  for (let pages = 0; next && pages < 5; pages++) {
    const { data, next: after, tag: read } = await apiPage(next);
    tag = tag || read;
    tasks.push(...data.tasks);
    const oldest = data.tasks[data.tasks.length - 1];
    next = oldest && Date.now() - new Date(oldest.createdAt).getTime() < 2 * day ? after : null;
  }
  return { tasks, tag };
}

// showActivity lists what happened to the organization's tasks in the last 24 hours (Agents · Activity), newest first:
// each was queued, started and ended, by agent, with what a task that ended spent, filtered by agent, event or task;
// it follows the newest tasks while it shows.
async function showActivity(org, main) {
  const { tasks, tag } = await recentTasks(org);
  const all = events(tasks).filter((e) => Date.now() - new Date(e.at).getTime() < day);
  const follow = () => {
    const drawn = main.firstElementChild;
    const still = () => drawn.isConnected;
    followLists([[newest(org), tag]], still, () => drawAgain(() => showActivity(org, main), still));
  };
  const ended = (e) => !["queued", "started"].includes(e.kind);
  const total = all.filter(ended).reduce((sum, e) => sum + micros(e.task.spent), 0);
  if (!all.length) {
    main.replaceChildren(h("div", { class: "card" }, emptyState("activity", "Nothing in the last 24 hours",
      "When an agent's task is queued, starts or ends, it shows here.")));
    return follow();
  }
  const row = (e) => h("tr", { "data-filter": [e.task.agent.displayName, eventText(e), "#" + e.task.number, taskTitle(e.task)].join(" ").toLowerCase() },
    h("td", { class: "cell-muted" }, since(e.at)),
    h("td", {}, h("a", { class: "activity-agent", href: agentPage(org, e.task.agent.id), "data-nav": "" }, whoAvatar(e.task.agent), e.task.agent.displayName)),
    h("td", {}, h("span", { class: "activity-event", title: eventText(e) }, eventIcon(e), h("span", {}, eventText(e)))),
    h("td", {}, taskLink(org, e.task, "activity-task", "#" + e.task.number + " " + taskTitle(e.task))),
    h("td", { class: "cell-number cell-mono" }, ended(e) ? dollars(e.task.spent) : "–"));
  const rows = all.map(row);
  const table = h("table", { class: "table activity-table" }, h("thead", {}, h("tr", {}, h("th", {}, "When"), h("th", {}, "Agent"), h("th", {}, "Event"),
    h("th", {}, "Task"), h("th", { class: "cell-number" }, "Spend"))), h("tbody", {}, rows));
  const none = h("p", { class: "filters-count", hidden: true }, "No event matches.");
  const filter = h("input", { type: "search", "aria-label": "Filter events", placeholder: "Filter events", oninput: () => {
    const q = filter.value.trim().toLowerCase();
    const shown = rows.filter((r) => { r.hidden = !!q && !r.dataset.filter.includes(q); return !r.hidden; }).length;
    table.hidden = !shown;
    none.hidden = !!shown;
  } });
  main.replaceChildren(h("div", { class: "filters" }, h("div", { class: "input" }, icon("search"), filter),
    h("span", { class: "filters-count", title: "What the tasks that ended in the last 24 hours spent in all" },
      "Last 24 hours · " + plural(all.length, "event") + " · " + usd(total))),
  table, none);
  follow();
}
