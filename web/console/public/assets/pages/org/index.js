import { api } from "../../lib/api.js";
import { h } from "../../lib/dom.js";
import { bars, day, stat } from "../../lib/figures.js";
import { plural } from "../../lib/format.js";
import { icon } from "../../lib/icons.js";
import { pageHeading } from "../../lib/layout.js";
import { popover } from "../../lib/menu.js";
import { go } from "../../lib/router.js";
import { base } from "../../lib/state.js";

export const nav = { label: "Overview", order: 0, icon: "house" };
export const crumbs = [{ label: "Overview" }];
export const view = "organization";

// The periods the Overview shows, This month the default: its key in the address, the button's label, how the
// description and the cards' captions name it, how a sub-line or a chart's total does, and the days it asks for, from
// first to last as the browser's calendar reads them (none for the server's default, this month so far).
const periods = [
  { key: "", label: "This month", phrase: "this month", short: "this month", caption: "this month", days: () => ({}) },
  { key: "last-month", label: "Last month", phrase: "last month", short: "last month", caption: "last month",
    days: (now) => ({ from: day(new Date(now.getFullYear(), now.getMonth() - 1, 1)), to: day(new Date(now.getFullYear(), now.getMonth(), 0)) }) },
  { key: "last-30-days", label: "Last 30 days", phrase: "in the last 30 days", short: "in 30 days", caption: "last 30 days",
    days: (now) => ({ from: day(new Date(now.getFullYear(), now.getMonth(), now.getDate() - 29)), to: day(now) }) },
];
const periodOf = () => periods.find((p) => p.key === new URLSearchParams(location.search).get("period")) || periods[0];

const count = (n) => n.toLocaleString("en");
const dollars = (amount) => Number(amount).toLocaleString("en", { style: "currency", currency: "USD" });
const repositories = (n) => count(n) + (n === 1 ? " repository" : " repositories");

// heading is the Overview's Page header: the organization over the period, which show completes with its counts, and
// the button that picks the period.
export const heading = ({ org }) => {
  const p = periodOf();
  return pageHeading({ title: "Overview", description: org.name + " " + p.phrase, actions: [periodButton(org, p)] });
};

// periodButton opens the Menu of the periods, the one shown checked; picking one shows the Overview over it.
function periodButton(org, shown) {
  const button = h("button", { class: "btn", type: "button", "aria-haspopup": "menu" }, icon("calendar"), shown.label, icon("chevron-down"));
  button.addEventListener("click", () => popover(button, "end", (close) => periods.map((p) => h("button", {
    class: "menu-item", type: "button", role: "menuitemradio", "aria-checked": String(p === shown),
    onclick: () => { close(); go("/" + org.slug + (p.key ? "?period=" + p.key : ""), true); } }, h("span", {}, p.label)))));
  return button;
}

// An organization's Overview over a period, in the browser's time zone: how its work stands as Stats, its tasks, spend
// and job minutes by day as Chart/Bars, and the repositories the viewer reads that saw anything, costliest first; or
// an Empty state while it has nothing to count.
export default async function show({ org, main, can, head }) {
  const p = periodOf();
  const query = new URLSearchParams({ ...p.days(new Date()), zone: Intl.DateTimeFormat().resolvedOptions().timeZone });
  const o = await api("GET", "/orgs/" + org.slug + "/overview?" + query);
  const description = head?.querySelector(".page-header-description");
  if (!o.repositories && !o.totals.tasks && !o.totals.jobs && !o.issues?.open) {
    if (description) description.textContent = [org.name, plural(o.members, "member"), "no repositories yet"].join(" · ");
    head?.querySelector(".page-header-actions")?.replaceChildren();
    main.replaceChildren(nothingYet(org, can));
    return;
  }
  if (description) description.textContent = [org.name + " " + p.phrase, plural(o.members, "member"), repositories(o.repositories), plural(o.agents, "agent")].join(" · ");
  main.replaceChildren(h("div", { class: "overview" }, stats(o, p), charts(o, p), byRepository(org, o, p)));
}

// nothingYet is the Card an organization shows until a repository, an issue or a task gives it something to count,
// with New repository for a viewer who may make one.
const nothingYet = (org, can) => h("section", { class: "card empty-centred overview-empty" }, h("div", { class: "empty" },
  h("span", { class: "empty-icon" }, icon("folder-git-2")), h("h2", { class: "empty-title" }, "Nothing to count yet"),
  h("p", { class: "empty-description" }, "Counters and charts fill in once a repository has commits, issues or agent tasks. Start with a repository, or invite the people who will."),
  can("repos.create") ? h("a", { class: "btn btn-primary", href: base + org.slug + "/new", "data-nav": "" }, "New repository") : null));

// stats are the Overview's counters: open issues, for a viewer who reads them, with those opened in the period; the
// branches Ready and Blocked; the agents at work and the tasks queued; and the period's spend and job minutes.
const stats = (o, p) => h("div", { class: "overview-stats" },
  o.issues ? stat("Open issues", "circle-dot", count(o.issues.open), "+" + count(o.issues.opened) + " " + p.short, "neutral") : null,
  stat("Branches ready", "git-branch", count(o.readyBranches), count(o.blockedBranches) + " blocked", o.blockedBranches ? "warning" : "neutral"),
  stat("Agents working", "bot", count(o.workingAgents) + " of " + count(o.agents), plural(o.queuedTasks, "task") + " queued", o.queuedTasks ? "ai" : "neutral"),
  stat("Agent spend", "sparkles", dollars(o.totals.spent), "across " + plural(o.totals.tasks, "task"), "neutral"),
  stat("Job minutes", "clock", count(o.totals.jobMinutes), "across " + plural(o.totals.jobs, "job"), "neutral"));

// charts are the period's tasks, spend and job minutes by day.
const charts = (o, p) => h("div", { class: "overview-charts" },
  bars("Tasks per day", "ai", count(o.totals.tasks) + " " + p.short, o.days, (d) => d.tasks, (d) => plural(d.tasks, "task")),
  bars("Spend per day", "accent", dollars(o.totals.spent) + " " + p.short, o.days, (d) => Number(d.spent), (d) => dollars(d.spent)),
  bars("Job minutes per day", "info", count(o.totals.jobMinutes) + " " + p.short, o.days, (d) => d.jobMinutes, (d) => count(d.jobMinutes) + " min"));

// byRepository is the Card of the repositories the viewer reads that saw anything in the period, costliest first:
// each one's commits, merges, tasks, spend and job minutes.
function byRepository(org, o, p) {
  const columns = ["Commits", "Merged", "Tasks", "Spend", "Job minutes"];
  const row = (r) => h("tr", {},
    h("td", {}, h("span", { class: "overview-repository" }, icon("folder-git-2"), h("a", { class: "cell-mono", href: base + org.slug + "/" + r.repository, "data-nav": "" }, r.repository))),
    [count(r.commits), count(r.merged), count(r.tasks), dollars(r.spent), count(r.jobMinutes)].map((v) => h("td", { class: "cell-number" }, v)));
  return h("section", { class: "card" },
    h("header", { class: "card-header" }, h("h2", { class: "card-title" }, "By repository"), h("span", { class: "card-meta" }, p.caption)),
    o.byRepository.length ? h("table", { class: "table overview-repositories" },
      h("thead", {}, h("tr", {}, h("th", {}, "Repository"), columns.map((c) => h("th", { class: "cell-number" }, c)))),
      h("tbody", {}, o.byRepository.map(row)))
      : h("p", { class: "card-body card-note" }, "Nothing happened in the repositories you read " + p.phrase + "."));
}
