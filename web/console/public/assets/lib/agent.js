// An agent's page, and what the Agents pages share (pages/org/agents.js, its tabs, Settings · Agents): money and
// tokens as the Console writes them, a task's state as a quiet badge, a task as a row of its agent's page, what happened
// to tasks as events, the month's spend report, and the dialogs that make an agent and give one a task.

import { api } from "./api.js";
import { openDialog } from "./dialog.js";
import { h } from "./dom.js";
import { ago, capitalized, plural } from "./format.js";
import { banner, checkbox, field, input, selectBox, textarea } from "./forms.js";
import { avatar, icon } from "./icons.js";
import { go } from "./router.js";
import { remember } from "./sidebar.js";
import { agents as agentIds, base, me, roles } from "./state.js";

// micros is an amount the API writes in US dollars, such as 3.30, in millionths of a dollar, for sums and shares.
export const micros = (amount) => Math.round(Number(amount || 0) * 1e6);
// usd writes millionths of a dollar as the Console shows a sum of money: $3.30.
export const usd = (m) => "$" + (m / 1e6).toLocaleString("en-US", { minimumFractionDigits: 2, maximumFractionDigits: 2 });
// dollars writes an amount as the API sends it, which keeps the digits a fraction of a cent takes: $0.00015.
export const dollars = (amount) => "$" + (amount || "0.00");

// providerNames are the model providers' names as they write them, where capitalizing the installation's name for one
// would misspell it.
const providerNames = { openai: "OpenAI", deepseek: "DeepSeek", xai: "xAI" };
export const providerName = (name) => providerNames[name] || capitalized(name);

// tokens is a count of tokens as a column of them shows it: millions from a tenth of one, thousands, or a dash for none.
export function tokens(n) {
  if (!n) return "–";
  if (n >= 100000) return (n / 1e6).toFixed(1) + " M";
  return n >= 1000 ? Math.round(n / 1000) + " K" : String(n);
}

// since is how long ago a moment was, as a sentence ends with it: 14 min ago, 2 h ago, yesterday, then its date.
export const since = (at) => { const a = ago(at); return /\d (min|h)$/.test(a) ? a + " ago" : a; };

// lasted is how long a task ran, from one moment to another: 48 s, 14 min, 2 h 5 min.
export function lasted(from, to) {
  const s = Math.max(0, Math.round((new Date(to) - new Date(from)) / 1000));
  if (s < 60) return s + " s";
  if (s < 3600) return Math.round(s / 60) + " min";
  return Math.floor(s / 3600) + " h " + Math.round((s % 3600) / 60) + " min";
}

// taskWords are the words and tones a task's state shows in (Badge/Quiet), as the agent's screens name them.
export const taskWords = { queued: ["Queued", "neutral"], running: ["Working", "ai"], waiting: ["Waiting", "warning"],
  succeeded: ["Done", "success"], failed: ["Failed", "danger"], cancelled: ["Cancelled", "neutral"] };
export const activeTask = (t) => ["queued", "running", "waiting"].includes(t.state);
export const workingTask = (t) => t.state === "running" || t.state === "waiting";
// quiet is a quiet badge (Badge/Quiet): a dot in the tone, then the word.
export const quiet = (word, tone) => h("span", { class: "badge-quiet tone-" + tone }, word);
export const stateBadge = (state) => quiet(...(taskWords[state] || [state, "neutral"]));
// statusBadge is an agent's status: Working while one of its tasks runs or waits for an answer, Idle otherwise.
export const statusBadge = (working) => working ? quiet("Working", "ai") : quiet("Idle", "neutral");

// whoAvatar is a principal's avatar, an agent's when the organization's agents (state.js) count it.
export const whoAvatar = (p, size = 20) => avatar(p.displayName, agentIds.has(p.id), size);
// agentPage is the address of an agent's page.
export const agentPage = (org, id) => base + org.slug + "/agents/" + id;
// taskPage is the address of a task's page, under its agent's: t's own agent's, or agent's for a task that doesn't name
// it (TaskCost).
export const taskPage = (org, t, agent) => agentPage(org, t.agent ? t.agent.id : agent) + "/tasks/" + t.number;
// taskTitle is a task's title, or what stands for one its asker keeps private (a redacted place in the queue).
export const taskTitle = (t) => t.title || "Asked for in private";
// hiddenTask says whether t was asked in another's private conversation: the API answers its number, agent, kind, state
// and times alone, and no page of it.
export const hiddenTask = (t) => t.private && !t.title;
// taskLink is a link to t's page, of class cls, holding children; or the children alone for a task asked in another's
// private conversation, which opens no page.
export const taskLink = (org, t, cls, ...children) => hiddenTask(t) ? h("span", { class: cls }, ...children)
  : h("a", { class: cls, href: taskPage(org, t), "data-nav": "" }, ...children);
// place is where a task works: its repository and branch, or anywhere.
const place = (t) => t.repositorySlug ? [t.repositorySlug, t.branch].filter(Boolean).join(" · ") : "Anywhere";

// spendOf is what a task spent, against its cap while it may still spend (the cap fails it), and a dash while queued.
function spendOf(t) {
  if (t.state === "queued") return "–";
  return t.cap && activeTask(t) ? dollars(t.spent) + " of " + dollars(t.cap) : dollars(t.spent);
}

// momentOf is when a task row says it happened: how long ago it was queued, for how long it has run, or how long ago it
// ended and how long it took.
function momentOf(t) {
  if (t.state === "queued" || !t.startedAt) return since(t.createdAt);
  if (!t.endedAt) return "for " + lasted(t.startedAt, new Date());
  return ago(t.endedAt) + " · " + lasted(t.startedAt, t.endedAt);
}

// taskRow is one of an agent's tasks as its page lists it: its number, title and where it works, its state, what it
// spent against its cap, when, and who assigned it; it opens the task, unless it was asked in another's private
// conversation.
export const taskRow = (org, t) => taskLink(org, t, "agent-task",
  h("span", { class: "agent-task-number" }, "#" + t.number),
  h("span", { class: "row-text" }, h("span", { class: "row-title" }, taskTitle(t)), h("span", { class: "row-meta" }, place(t))),
  stateBadge(t.state), h("span", { class: "agent-task-spend" }, spendOf(t)), h("span", { class: "agent-task-when" }, momentOf(t)),
  t.assignedBy ? whoAvatar(t.assignedBy) : h("span", { class: "agent-task-nobody" }));

// events are what happened to tasks, newest first: each was queued, perhaps started, and perhaps ended in its state.
// The tasks' own moments are all there is, so a task that waited and went on shows no event for it.
export function events(tasks) {
  const out = [];
  for (const t of tasks) {
    out.push({ at: t.createdAt, kind: "queued", task: t });
    if (t.startedAt) out.push({ at: t.startedAt, kind: "started", task: t });
    if (t.endedAt) out.push({ at: t.endedAt, kind: t.state, task: t });
  }
  return out.sort((a, b) => new Date(b.at) - new Date(a.at));
}

// eventGlyphs are the icon and tone each kind of event shows.
export const eventGlyphs = { queued: ["clock", "neutral"], started: ["sparkles", "ai"], succeeded: ["circle-check", "success"],
  failed: ["circle-x", "danger"], cancelled: ["ban", "neutral"] };
const verbs = { queued: "Queued", started: "Started", succeeded: "Finished", failed: "Failed", cancelled: "Cancelled" };

// eventText says what happened, with the task's number when numbered: who queued it, who it acts for, what it said it
// did, or why it failed or was cancelled.
export function eventText(e, numbered) {
  const t = e.task;
  const head = verbs[e.kind] + (numbered ? " #" + t.number : "");
  const by = (p) => p ? p.displayName : null;
  if (e.kind === "queued") return head + (by(t.assignedBy) ? " by " + by(t.assignedBy) : "");
  if (e.kind === "started") return head + (by(t.assignedBy) ? ", acting for " + by(t.assignedBy) : "");
  if (e.kind === "succeeded") return head + (t.summary ? ": " + t.summary : "");
  if (e.kind === "cancelled") return head + (by(t.cancelledBy) ? " by " + by(t.cancelledBy) : "") + (t.reason ? ". " + t.reason : "");
  return head + (t.reason ? ". " + t.reason : "");
}
// eventIcon is an event's icon in its tone.
export const eventIcon = (e) => h("span", { class: "event-icon tone-" + eventGlyphs[e.kind][1] }, icon(eventGlyphs[e.kind][0]));

// spendReport is what the organization's agents and models spent this month in the viewer's time zone (GET
// /orgs/{org}/spend), or in UTC when the API doesn't know the zone.
export async function spendReport(org) {
  const path = "/orgs/" + org.slug + "/spend";
  const zone = (() => { try { return Intl.DateTimeFormat().resolvedOptions().timeZone; } catch { return ""; } })();
  if (!zone) return api("GET", path);
  return api("GET", path + "?" + new URLSearchParams({ zone })).catch((err) => { if (err.status === 400) return api("GET", path); throw err; });
}

// emptyState is an Empty state: an icon in a sunken circle, a title, what would fill it, and the action that does.
export const emptyState = (glyph, title, text, action) => h("div", { class: "empty" }, h("span", { class: "empty-icon" }, icon(glyph)),
  h("h2", { class: "empty-title" }, title), h("p", { class: "empty-description" }, text), action || "");
// card is a Card with a header of its title and its meta, holding body.
export const card = (title, meta, ...body) => h("section", { class: "card" },
  h("header", { class: "card-header" }, h("h2", { class: "card-title" }, title), meta ? h("span", { class: "card-meta" }, meta) : ""), ...body);

// mayEdit is whether the viewer changes agent a: its creator does, and so does whoever manages the organization's
// principals.
export const mayEdit = (a, can) => (a.createdBy && a.createdBy.id === me.id) || can("org.principals.manage");
// editorPage is the address of Settings · Agents with agent a's editor open.
export const editorPage = (org, a) => base + org.slug + "/settings/agents?" + new URLSearchParams({ agent: a.id });

// showAgent draws one of the organization's agents (Agent): what it works on, its queue and its latest tasks, what it
// runs on and what it spent this month, and what happened to its tasks; Edit opens it in Settings · Agents and Assign a
// task gives it one, once it runs on a model.
export async function showAgent(org, main, can, id) {
  let a;
  try {
    a = await api("GET", "/orgs/" + org.slug + "/agents/" + encodeURIComponent(id));
  } catch (err) {
    if (err.status !== 404 && err.status !== 400) throw err;
    return main.replaceChildren(emptyState("bot", "No such agent", "There is no such agent in " + org.name + ", or you may not see it."));
  }
  const [tasks, report, providers] = await Promise.all([
    api("GET", "/orgs/" + org.slug + "/tasks?" + new URLSearchParams({ agent: a.id, limit: "100" })).then((d) => d.tasks),
    spendReport(org).catch(() => null),
    api("GET", "/orgs/" + org.slug + "/model-providers").then((d) => d.modelProviders, () => []),
  ]);
  named(org, a);
  const refresh = () => showAgent(org, main, can, id);
  const working = tasks.filter(workingTask);
  main.replaceChildren(agentHeader(org, a, can, tasks, refresh),
    a.model ? "" : banner("warning", "No model yet", "Pick a model in Settings · Agents. Until then " + a.name + " can't take a task.",
      mayEdit(a, can) ? h("a", { class: "btn btn-ghost btn-sm", href: editorPage(org, a), "data-nav": "" }, "Open Settings · Agents") : ""),
    h("div", { class: "agent-columns" },
      h("div", { class: "agent-column" }, ...taskCards(org, a, can, tasks, working, refresh)),
      h("div", { class: "agent-column" }, factsCard(a, providers, report), activityCard(a, tasks))));
}

// named shows agent a's name in the breadcrumbs and the window's title once its page has read it, and puts it first
// among the sidebar's agents.
function named(org, a) {
  const last = document.querySelector(".app-crumbs [aria-current] code");
  if (last) last.textContent = a.name;
  document.title = a.name + " · Cobranch";
  remember(org, "agents", { key: a.id, label: a.name, href: agentPage(org, a.id), avatar: "agent" });
}

// agentHeader is the top of an agent's page: its avatar, name, Agent tag and status, what it runs on, who made it and
// what it is for, then Edit and Assign a task for a viewer who may.
function agentHeader(org, a, can, tasks, refresh) {
  const meta = [a.provider ? providerName(a.provider) : "", a.model || "no model yet",
    a.builtIn ? "built in" : a.createdBy ? "created " + since(a.createdAt) + " by " + a.createdBy.displayName : "",
    a.description || (tasks.length ? "" : "no tasks")].filter(Boolean).join(" · ");
  const assign = can("agents.assign") ? h("button", { class: "btn btn-primary", type: "button", disabled: !a.model,
    title: a.model ? "" : a.name + " needs a model first", onclick: () => assignTask(org, [a], a, refresh) }, icon("plus"), "Assign a task") : "";
  return h("header", { class: "page-header agent-header" }, avatar(a.name, true, 40),
    h("div", { class: "page-header-text" },
      h("div", { class: "app-page-heading-title" }, h("h1", { class: "page-header-title" }, a.name), h("span", { class: "tag tone-ai" }, icon("sparkles"), "Agent"),
        statusBadge(tasks.some(workingTask))),
      h("p", { class: "page-header-description agent-header-meta", title: meta }, meta)),
    h("div", { class: "page-header-actions" },
      mayEdit(a, can) ? h("a", { class: "btn", href: editorPage(org, a), "data-nav": "" }, icon("pencil"), "Edit") : "", assign));
}

// taskCards are an agent's tasks by where they stand: what it works on now, its queue, oldest first as it takes them,
// and the latest ten it ended.
function taskCards(org, a, can, tasks, working, refresh) {
  const rows = (list) => h("div", { class: "agent-tasks" }, list.map((t) => taskRow(org, t)));
  const queued = tasks.filter((t) => t.state === "queued").sort((x, y) => x.number - y.number);
  const ended = tasks.filter((t) => !activeTask(t)).slice(0, 10);
  const give = can("agents.assign") && a.model ? h("button", { class: "btn btn-primary btn-sm", type: "button", onclick: () => assignTask(org, [a], a, refresh) }, "Give it work") : "";
  return [
    card("Working on", working.length && working[0].startedAt ? "started " + since(working[0].startedAt) : "",
      working.length ? rows(working) : emptyState("sparkles", "Nothing right now", "Give " + a.name + " a task, and it shows here while " + a.name + " works on it.", give)),
    card("Queue", queued.length ? plural(queued.length, "task") : "",
      queued.length ? rows(queued) : emptyState("clock", "No tasks queued", "Tasks wait here until " + a.name + " starts them.")),
    card("History", ended.length ? "latest " + ended.length : "",
      ended.length ? rows(ended) : emptyState("circle-check", "Nothing ended yet", "The tasks " + a.name + " finished, failed or had cancelled show here.")),
  ];
}

// fact is one of a card's facts (Fact row): its label and its value, Kind=Code when the value is code (a slug, a
// branch, a secret's name).
export const fact = (label, value, code) => h("div", { class: "fact-row" + (code ? " fact-row-code" : "") },
  h("span", { class: "fact-label" }, label), h("div", { class: "fact-value" }, value));

// factsCard is what agent a runs on and as: its model, the secret its provider's key is in, the secrets its tasks
// receive, its standing instructions, whom it acts as, its cap, and what it spent this month.
function factsCard(a, providers, report) {
  const provider = providers.find((p) => p.name === a.provider);
  const key = !provider ? "–" : provider.broken ? h("span", { class: "fact-row-danger" }, "Its key is gone") : provider.secretName || "A secret you don't manage";
  const secrets = Object.keys(a.secrets);
  const month = report && report.agents.find((s) => s.agent.id === a.id);
  const spentNow = month ? micros(month.spent) : 0;
  const thisMonth = usd(spentNow) + (month && month.tasks ? " over " + plural(month.tasks, "task") : " · no tasks");
  return card("Agent", "", h("div", { class: "agent-facts" },
    fact("Model", a.model || "Not set", !!a.model), fact("Provider key", key, !!provider && !provider.broken && !!provider.secretName),
    fact("Secrets", secrets.length ? secrets.join(", ") : "None", secrets.length > 0), fact("Instructions", h("span", { class: "fact-row-clamp" }, a.instructions || "None")),
    fact("Acts as", "Whoever assigned the task; never more than them."),
    fact("Spend cap", a.cap ? dollars(a.cap) + " a task" : a.model ? "None" : "None until a model is set"),
    fact("This month", report ? thisMonth : "–")));
}

// activityCard is what happened to agent a's latest tasks, the newest five events.
function activityCard(a, tasks) {
  const latest = events(tasks).slice(0, 5);
  return card("Activity", "", latest.length ? h("div", { class: "agent-facts" }, latest.map((e) => fact(since(e.at), eventText(e, true))))
    : emptyState("activity", "No activity yet", a.name + " has not run a task."));
}

// repoSelect picks one of the organization's repositories by ID, or none.
const repoSelect = (repos, none) => h("select", {}, h("option", { value: "" }, none), repos.map((r) => h("option", { value: r.id }, r.name)));

// assignTask asks for a task for one of list, the agents that may take one, picked when given, perhaps in a repository
// the viewer reads and on one of its branches, and assigns it; done follows, or else the agent's page opens. The task
// does no more than both the agent and the viewer may.
export async function assignTask(org, list, picked, done) {
  const repos = await api("GET", "/orgs/" + org.slug + "/repos").then((d) => d.repositories, () => []);
  const agent = h("select", { required: true }, list.map((a) => h("option", { value: a.id, selected: picked && a.id === picked.id }, a.name)));
  const title = input({ required: true, maxlength: 200, placeholder: "Write the release notes for 1.4" });
  const instructions = textarea({ maxlength: 20000, rows: 5, placeholder: "What to do, in Markdown." });
  const repo = repoSelect(repos, "No repository");
  const branch = input({ placeholder: "main" });
  openDialog({ title: picked ? "Give " + picked.name + " a task" : "Assign a task", action: "Assign",
    body: [picked ? "" : field("Agent", selectBox(agent)), field("Title", title), field("Instructions", instructions),
      field("Repository", selectBox(repo), "Where it works, if anywhere."), field("Branch", branch, "A branch of that repository; a new one of its own when left empty."),
      h("p", { class: "field-help" }, "The task does no more than both the agent and you may, as you are whenever it acts.")],
    submit: async () => {
      const body = { agentId: agent.value, title: title.value, instructions: instructions.value };
      if (repo.value) body.repositoryId = repo.value;
      if (repo.value && branch.value.trim()) body.branch = branch.value.trim();
      await api("POST", "/orgs/" + org.slug + "/tasks", body);
      return done ? done() : go("/" + org.slug + "/agents/" + agent.value);
    } });
  title.focus();
}

// newAgent asks for an agent's name, what it is for, its standing instructions and its first roles (at the
// organization, and perhaps one on a repository, each one whose every permission the viewer holds there), makes it on
// the organization's default model, and opens its page.
export async function newAgent(org) {
  const repos = await api("GET", "/orgs/" + org.slug + "/repos").then((d) => d.repositories, () => []);
  const name = input({ required: true, maxlength: 100, placeholder: "Release notes writer" });
  const description = input({ maxlength: 1000, placeholder: "Writes the release notes of every version" });
  const instructions = textarea({ maxlength: 20000, rows: 4, placeholder: "What every task of it starts from, in Markdown." });
  const picks = roles.map((r) => checkbox(h("input", { type: "checkbox", value: r.id }), r.name));
  const repo = repoSelect(repos, "No repository");
  const repoRole = h("select", {}, h("option", { value: "" }, "No role"));
  // The roles a repository grants are those of the one picked.
  repo.addEventListener("change", () => {
    repoRole.replaceChildren(h("option", { value: "" }, "No role"));
    const chosen = repos.find((r) => r.id === repo.value);
    if (chosen) api("GET", "/orgs/" + org.slug + "/repos/" + chosen.slug + "/roles").then((d) => repoRole.append(...d.roles.map((r) => h("option", { value: r.id }, r.name))), () => {});
  });
  openDialog({ title: "New agent", action: "Make the agent",
    body: [field("Name", name), field("Description", description), field("Instructions", instructions, "It starts on the organization's default model, which Settings · Agents changes."),
      h("div", { class: "field" }, h("span", { class: "field-label" }, "Roles"), h("div", { class: "agent-picks" }, picks),
        h("span", { class: "field-help" }, "Only roles whose every permission you hold.")),
      field("A role on a repository", h("div", { class: "agent-pair" }, selectBox(repo), selectBox(repoRole)))],
    submit: async () => {
      const grants = picks.map((l) => l.querySelector("input")).filter((c) => c.checked).map((c) => ({ roleId: c.value }));
      if (repo.value && repoRole.value) grants.push({ roleId: repoRole.value, repositoryId: repo.value });
      const made = await api("POST", "/orgs/" + org.slug + "/agents", { name: name.value, description: description.value, instructions: instructions.value, grants });
      go("/" + org.slug + "/agents/" + made.id);
    } });
  name.focus();
}
