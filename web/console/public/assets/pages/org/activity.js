import { agentPage } from "../../lib/agent.js";
import { api } from "../../lib/api.js";
import { branchBadge } from "../../lib/branches.js";
import { h } from "../../lib/dom.js";
import { ago, capitalized, plural, shortSha, since, whenExactly } from "../../lib/format.js";
import { avatar, icon } from "../../lib/icons.js";
import { pageHeading } from "../../lib/layout.js";
import { visibilities } from "../../lib/organization.js";
import { base } from "../../lib/state.js";

// nav puts Activity in the sidebar beside Overview.
export const nav = { label: "Activity", order: 5, icon: "activity" };
export const crumbs = [{ label: "Activity" }];
export const view = "organization";

export const heading = () => pageHeading({ title: "Activity", description: "What needs you, what the agents are doing, and what happened" });

// How many repositories the page shows, and how much of what happened.
const shownRepositories = 3;
const shownActivity = 10;

// The organization's Activity: the branches that need the viewer and the repositories pushed to last in its main
// column, and beside them its agents at work and what happened last in the repositories the viewer reads.
export default async function show({ org, main, can }) {
  const path = "/orgs/" + org.slug;
  const [attention, repositories, agents, working, activity] = await Promise.all([
    api("GET", path + "/attention").then((d) => d.branches),
    api("GET", path + "/repos").then((d) => d.repositories),
    api("GET", path + "/agents").then((d) => d.agents),
    api("GET", path + "/tasks?state=running&state=waiting&limit=100").then((d) => d.tasks, () => []),
    api("GET", path + "/activity?limit=" + shownActivity).then((d) => d.activity),
  ]);
  const agentIds = new Set(agents.map((a) => a.id));
  main.replaceChildren(h("div", { class: "activity-columns" },
    h("div", { class: "activity-main" }, attentionCard(org, attention, agentIds), repositoriesCard(org, repositories, can)),
    h("div", { class: "activity-side" }, agentsCard(org, agents, working, can), activityCard(activity))));
}

// branches is how many branches n is, as a count says it.
const branches = (n) => n + (n === 1 ? " branch" : " branches");

// card is a Card titled title, with meta or an action in its header, holding body.
const card = (title, aside, ...body) => h("section", { class: "card" }, h("header", { class: "card-header" }, h("h2", { class: "card-title" }, title), aside), ...body);

// note is the line a card shows while it has nothing to list.
const note = (text) => h("p", { class: "card-body card-note" }, text);

// attentionCard is Needs your attention: each branch that needs the viewer as a Branch row, its name linked to its
// page over its repository, its base and why it needs them, then its status and its head's author.
function attentionCard(org, list, agentIds) {
  const row = ({ repository, branch: b, need }) => h("li", { class: "branch-row" }, icon("git-branch"), h("div", { class: "row-text" },
    h("a", { class: "row-title", href: base + org.slug + "/" + repository + "/branches/" + b.name.split("/").map(encodeURIComponent).join("/"), "data-nav": "" }, b.name),
    h("span", { class: "row-meta" }, [repository, "into " + (b.base || "main"), ...why(b, need)].join(" · "))),
  b.status ? branchBadge(b.status) : null, author(b, agentIds));
  return card("Needs your attention", list.length ? h("span", { class: "card-meta" }, branches(list.length)) : null,
    list.length ? h("ul", { class: "list" }, list.map(row)) : note("Nothing needs you right now."));
}

// author is the avatar of whoever wrote a branch's head, named on hover: an agent's, or a person's.
function author(b, agentIds) {
  const person = b.commit?.author;
  if (!person) return null;
  const el = avatar(person.principal?.displayName || person.name, !!person.principal && agentIds.has(person.principal.id));
  el.title = person.principal?.displayName || person.name;
  return el;
}

// why says why a branch needs the viewer, in the parts its row's meta joins: who opened it and what it waits for
// from them, or what holds it back, or what it waits for.
function why(b, need) {
  const by = "opened by " + (b.commit?.author.principal?.displayName || b.commit?.author.name || "someone");
  switch (need) {
  case "review": return [by, "waiting for your review"];
  case "merge": return [by, "waiting for your merge"];
  case "fix": return [blocker(b.checks.find((c) => !c.met))];
  default: return [waiting(b)];
  }
}

// blocker says what an unmet check holds a Blocked branch back with.
function blocker(c) {
  if (!c) return "blocked";
  switch (c.kind) {
  case "changes_requested": return "changes requested by " + c.name;
  case "annotation": return c.status === "failed" ? c.name + " failed" : "waiting for " + c.name;
  case "approvals": return "needs " + plural(Math.max(c.required - c.count, 1), "more approval");
  case "linear_history": return plural(c.count, "merge commit") + " to take out";
  case "up_to_date": return plural(c.count, "commit") + " behind main";
  case "ai": return aiBlocker[c.ai] || "waiting for the AI review";
  default: return "blocked";
  }
}

// aiBlocker says how the AI review holds a branch back, by its check's state.
const aiBlocker = {
  changes_requested: "changes requested by the AI review",
  not_approved: "not approved by the AI review",
  unreviewed: "not reviewed by AI yet",
  failed: "the AI review failed",
  cancelled: "the AI review was cancelled",
};

// waiting says what a Processing branch of the viewer's waits for: its jobs, the AI review, or Cobranch working out
// its last push.
function waiting(b) {
  const c = b.checks.find((c) => !c.met && (c.kind === "annotation" ? ["pending", "in_progress"].includes(c.status) : c.kind === "ai"));
  if (c?.kind === "annotation") return c.name + (c.status === "pending" ? " pending on " : " running on ") + shortSha(b.head);
  return c ? "waiting for the AI review" : "jobs running on " + shortSha(b.head);
}

// repositoriesCard is Repositories: those pushed to last as Repository rows, each linked to its page over its
// description, its branches and when it was last pushed to, and its visibility, with View all for the rest.
function repositoriesCard(org, list, can) {
  const last = (r) => (r.summary?.lastPushAt ? Date.parse(r.summary.lastPushAt) : 0);
  const shown = [...list].sort((a, b) => last(b) - last(a) || a.slug.localeCompare(b.slug)).slice(0, shownRepositories);
  const meta = (s) => [branches(s?.openBranches || 0), s?.readyBranches ? s.readyBranches + " ready to merge" : null,
    s?.lastPushAt ? "updated " + since(s.lastPushAt) : "no pushes yet"].filter(Boolean).join(" · ");
  const row = (r) => h("li", { class: "repository-row" }, icon("folder-git-2"), h("div", { class: "row-text" },
    h("a", { class: "row-title", href: base + org.slug + "/" + r.slug, "data-nav": "" }, r.slug),
    r.description ? h("span", { class: "row-description" }, r.description) : null, h("span", { class: "row-meta" }, meta(r.summary))),
  h("span", { class: "tag", title: visibilities[r.visibility]?.title }, visibilities[r.visibility]?.name || capitalized(r.visibility)));
  return card("Repositories", list.length ? h("a", { href: base + org.slug + "/repos", "data-nav": "" }, "View all") : null,
    list.length ? h("ul", { class: "list" }, shown.map(row)) : h("div", { class: "empty" }, h("div", { class: "empty-icon" }, icon("folder-git-2")),
      h("h2", { class: "empty-title" }, "No repository yet"),
      h("p", { class: "empty-description" }, can("repos.create") ? "Make " + org.name + "'s first repository and push to it." : "None of " + org.name + "'s repositories is shared with you yet."),
      can("repos.create") ? h("a", { class: "btn btn-primary", href: base + org.slug + "/new", "data-nav": "" }, icon("plus"), "New repository") : null));
}

// The states an agent at work shows, as its Tag says them, in the order the card lists them.
const agentStates = [{ word: "Working", tone: "info" }, { word: "Watching", tone: "ai" }, { word: "Idle", tone: "neutral" }];

// agentsCard is Agents at work: each agent as an Agent row, its name linked to its page over what it does now, and
// whether it works on a task (running, or waiting for an answer, as its own page counts it; a running one is named
// first), watches every push (a built-in agent with a model), or waits for a task.
function agentsCard(org, agents, working, can) {
  const tasks = new Map();
  for (const t of working) if (!tasks.has(t.agent.id) || (t.state === "running" && tasks.get(t.agent.id).state !== "running")) tasks.set(t.agent.id, t);
  const at = (a) => {
    const t = tasks.get(a.id);
    if (t) return { state: agentStates[0], does: t.title ? t.title + (t.branch ? " on " + t.branch : "") : "Working on a private task" };
    return a.builtIn && a.model ? { state: agentStates[1], does: a.description } : { state: agentStates[2], does: "Waiting for a task" };
  };
  const rows = agents.map((a) => ({ a, ...at(a) })).sort((x, y) => agentStates.indexOf(x.state) - agentStates.indexOf(y.state) || x.a.name.localeCompare(y.a.name));
  const row = ({ a, state, does }) => h("li", { class: "agent-row" }, avatar(a.name, true, 24), h("div", { class: "row-text" },
    h("a", { class: "row-title", href: agentPage(org, a.id), "data-nav": "" }, a.name), h("span", { class: "row-meta" }, does)),
  h("span", { class: "tag tone-" + state.tone }, state.word));
  return card("Agents at work", null, rows.length ? h("ul", { class: "list" }, rows.map(row)) : h("div", { class: "card-body card-note activity-none" }, "No agent yet.",
    can("agents.create") ? h("a", { class: "btn btn-sm", href: base + org.slug + "/agents", "data-nav": "" }, "Make an agent") : null));
}

// activityCard is Recent activity: what happened last, newest first, each an Activity/Item of whoever did it, what
// they did and how long ago.
const activityCard = (list) => card("Recent activity", null, list.length ? h("ol", { class: "activity-list" }, list.map((x) => h("li", { class: "activity-item" },
  actorAvatar(x.actor), h("span", { title: "In " + x.repository }, (x.actor.kind ? x.actor.displayName : "Someone since gone") + " " + happened(x)),
  h("time", { datetime: x.at, title: whenExactly(x.at) }, capitalized(ago(x.at)))))) : note("Nothing has happened yet."));

// actorAvatar is the avatar of whoever did something: a person's initials, an agent's mark, or a machine's glyph (a
// job's cog, an API key's key, any other's server).
function actorAvatar(actor) {
  if (actor.kind === "agent") return avatar(actor.displayName, true);
  if (actor.kind === "user" || !actor.kind) return avatar(actor.displayName || "?");
  return h("span", { class: "avatar avatar-machine", "aria-hidden": "true" }, icon({ job: "cog", api_key: "key-round" }[actor.kind] || "server"));
}

// refName is a ref's name as people say it: a branch's or a tag's without its prefix.
const refName = (ref) => ref.replace(/^refs\/(heads|tags)\//, "");

// zero is the object ID Git names a ref's absence with.
const zero = /^0+$/;

// happened says what x did, after whoever did it.
function happened(x) {
  switch (x.kind) {
  case "pushed": {
    const refs = x.refs || [];
    const target = refs.length === 1 ? refName(refs[0].name) : refs.length + " refs";
    if (refs.length === 1 && zero.test(refs[0].new)) return "deleted " + target;
    return x.commits ? "pushed " + plural(x.commits, "commit") + " to " + target : "pushed to " + target;
  }
  case "merged": return "merged " + ((x.merged || []).map((m) => m.branch).join(", ") || "a branch") + " into main";
  case "reviewed": return { approved: "approved " + shortSha(x.sha) + (x.branch ? " on " + x.branch : ""),
    changes_requested: "requested changes on " + (x.branch || shortSha(x.sha)), neutral: "commented on " + (x.branch || shortSha(x.sha)) }[x.review];
  case "delivered": return delivered(x.delivery, x.sha);
  default: return "created " + x.repository;
  }
}

// delivered says what a delivery that ended did to its environment.
function delivered(d, sha) {
  const failed = d.state !== "succeeded";
  if (d.kind === "teardown") return (failed ? "failed to tear down" : "tore down") + " environment " + d.environment;
  return (failed ? "failed to sync" : "synced") + " environment " + d.environment + (sha ? " to " + shortSha(sha) : "");
}
