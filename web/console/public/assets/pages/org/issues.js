import { api, apiPage } from "../../lib/api.js";
import { branchBadge } from "../../lib/branches.js";
import { issueChat } from "../../lib/chat.js";
import { h } from "../../lib/dom.js";
import { when, whenExactly } from "../../lib/format.js";
import { icon } from "../../lib/icons.js";
import { issueBadge, issueStates } from "../../lib/issues.js";
import { rendered } from "../../lib/markdown.js";
import { go } from "../../lib/router.js";
import { base, me } from "../../lib/state.js";
import { taskBadge } from "../../lib/tasks.js";

export const nav = { label: "Issues", order: 10 };
export const allowed = (can) => can("issues.read");
export default ({ org, main, can, sub }) => showIssues(org, main, can, sub);

const issueGroups = ["in_review", "in_progress", "todo", "backlog", "done", "canceled"];
// The priorities, most urgent first, as a list sorts each group.
const issuePriorities = {
  urgent: { word: "Urgent", icon: "triangle-alert", tone: "danger" },
  high: { word: "High", icon: "signal-high", tone: "neutral" },
  medium: { word: "Medium", icon: "signal-medium", tone: "neutral" },
  low: { word: "Low", icon: "signal-low", tone: "neutral" },
  none: { word: "No priority", icon: "minus", tone: "neutral" },
};
const priorityRank = (p) => Object.keys(issuePriorities).indexOf(p);
// priorityIcon is an issue's priority as its icon alone, named in its tooltip.
function priorityIcon(priority) {
  const p = issuePriorities[priority];
  return h("span", { class: "tone-" + p.tone }, icon(p.icon, p.word));
}
// avatar is a principal's initials in a circle, named in its tooltip.
const avatar = (p) => h("span", { class: "avatar", title: p.displayName, role: "img", "aria-label": p.displayName },
  p.displayName.split(/\s+/).filter(Boolean).slice(0, 2).map((w) => w[0].toUpperCase()).join("") || "?");
// issuePath is an issue's page under the Console's root: by its identifier, though the page reads only its number.
const issuePath = (org, i) => "/" + org.slug + "/issues/" + i.identifier;
// branchName is the name to give a branch that works on an issue: its identifier and title in lowercase words,
// cut at a word within 60 characters, as Linear's "Copy git branch name" does.
function branchName(i) {
  const words = i.title.toLowerCase().normalize("NFKD").replace(/[\u0300-\u036f'\u2019]/g, "").replace(/[^a-z0-9]+/g, "-").replace(/^-+|-+$/g, "");
  const name = (i.identifier.toLowerCase() + "-" + words).replace(/-$/, "");
  return name.length <= 60 ? name : name.slice(0, 61).replace(/-[^-]*$/, "");
}
// issueChoices are whom an issue may be assigned to, what it may be about and what it may carry: the
// organization's members and agents, the repositories the viewer reads, and the organization's labels.
async function issueChoices(org) {
  const [members, agents, repositories, labels] = await Promise.all([api("GET", "/orgs/" + org.slug + "/members").then((d) => d.members),
    api("GET", "/orgs/" + org.slug + "/agents").then((d) => d.agents),
    api("GET", "/orgs/" + org.slug + "/repos").then((d) => d.repositories), api("GET", "/orgs/" + org.slug + "/labels").then((d) => d.labels)]);
  return { members, agents, repositories, labels };
}
// A label's colour is one of the design system's tones, so it reads in both themes.
const labelTones = { gray: "neutral", blue: "info", green: "success", yellow: "warning", red: "danger" };
const labelColors = { gray: "Gray", blue: "Blue", green: "Green", yellow: "Yellow", red: "Red" };
// labelChip is a label as the design system's Label draws it: a dot and its name in its colour.
const labelChip = (l) => h("span", { class: "label tone-" + labelTones[l.color], title: l.description || l.name }, l.name);
// labelPicks are the organization's labels as boxes to tick, those chosen ticked.
const labelPicks = (labels, chosen) => h("div", { class: "label-picks" }, labels.map((l) =>
  h("label", { class: "check" }, h("input", { type: "checkbox", value: l.id, checked: chosen.includes(l.id), "aria-label": l.name }), labelChip(l))));
const pickedLabels = (picks) => [...picks.querySelectorAll("input:checked")].map((i) => i.value);
const colorSelect = (value) => h("select", { name: "color", "aria-label": "Colour" }, Object.entries(labelColors).map(([c, word]) => h("option", { value: c, selected: c === value }, word)));
const stateSelect = (value) => h("select", { name: "state" }, Object.entries(issueStates).map(([s, { word }]) => h("option", { value: s, selected: s === value }, word)));
const prioritySelect = (value) => h("select", { name: "priority" }, Object.entries(issuePriorities).map(([p, { word }]) => h("option", { value: p, selected: p === value }, word)));
// assigneeSelect picks one of the members, one of the agents for a viewer who assigns them (assign), or nobody;
// an assignee who is neither any more stays its choice. Assigning an agent hands it the issue as its task.
function assigneeSelect(members, agents, assign, current) {
  const picked = (id) => !!current && id === current.id;
  const gone = current && !members.some((m) => m.userId === current.id) && !agents.some((a) => a.id === current.id);
  const offered = agents.filter((a) => assign || picked(a.id));
  return h("select", { name: "assigneeId" }, h("option", { value: "" }, "Nobody"),
    members.map((m) => h("option", { value: m.userId, selected: picked(m.userId) }, m.displayName + (m.userId === me.id ? " (you)" : ""))),
    offered.length ? h("optgroup", { label: "Agents, which take it as a task" }, offered.map((a) => h("option", { value: a.id, selected: picked(a.id) }, a.name))) : null,
    gone ? h("option", { value: current.id, selected: true }, current.displayName) : null);
}
const repositorySelect = (repositories, current) => h("select", { name: "repositoryId" }, h("option", { value: "" }, "None"),
  repositories.map((r) => h("option", { value: r.id, selected: current && r.id === current.id }, r.slug)));

// showIssues is the Issues page's: the list, the form that creates one, the labels, or an issue's page, by what
// follows issues/.
function showIssues(org, main, can, sub) {
  if (sub === "new" && can("issues.write")) return showNewIssue(org, main, can);
  if (sub === "labels") return showLabels(org, main, can);
  if (sub && sub !== "new") return showIssue(org, main, can, sub);
  return showIssueList(org, main, can);
}

// showIssueList lists the organization's open issues by state, the most urgent first within each, filtered by the
// assignee, the repository, a label and a text that the page's query names; a switch adds the closed ones.
async function showIssueList(org, main, can) {
  const params = new URLSearchParams(location.search);
  const { members, agents, repositories, labels } = await issueChoices(org);
  const error = h("p", { class: "alert", hidden: true });
  const fail = (err) => { error.textContent = err.message; error.hidden = false; };
  const filter = (name, value) => {
    const p = new URLSearchParams(location.search);
    if (value) p.set(name, value); else p.delete(name);
    go("/" + org.slug + "/issues" + (p.toString() ? "?" + p : ""), true);
  };
  const assignee = h("select", { "aria-label": "Assignee", onchange: (e) => filter("assignee", e.target.value) }, h("option", { value: "" }, "Anyone"),
    h("option", { value: me.id }, "Me"), h("option", { value: "none" }, "Nobody"),
    members.filter((m) => m.userId !== me.id).map((m) => h("option", { value: m.userId }, m.displayName)),
    agents.length ? h("optgroup", { label: "Agents" }, agents.map((a) => h("option", { value: a.id }, a.name))) : null);
  assignee.value = params.get("assignee") || "";
  const repository = h("select", { "aria-label": "Repository", onchange: (e) => filter("repository", e.target.value) }, h("option", { value: "" }, "Any repository"),
    h("option", { value: "none" }, "No repository"), repositories.map((r) => h("option", { value: r.id }, r.slug)));
  repository.value = params.get("repository") || "";
  const label = h("select", { "aria-label": "Label", onchange: (e) => filter("label", e.target.value) }, h("option", { value: "" }, "Any label"),
    labels.map((l) => h("option", { value: l.id }, l.name)));
  label.value = params.get("label") || "";
  const text = h("input", { type: "search", "aria-label": "Search", placeholder: "Search titles, descriptions, identifiers", value: params.get("q") || "",
    onchange: (e) => filter("q", e.target.value.trim()) });
  const closed = params.get("closed") === "1";
  const switcher = h("label", { class: "check" }, h("input", { type: "checkbox", checked: closed, onchange: (e) => filter("closed", e.target.checked ? "1" : "") }), "Show closed");
  const query = new URLSearchParams({ limit: "100" });
  if (closed) issueGroups.forEach((s) => query.append("state", s));
  for (const [name, value] of [["assignee", assignee.value], ["repository", repository.value], ["label", label.value], ["q", text.value.trim()]]) if (value) query.set(name, value);
  const groups = h("div", { class: "groups" });
  const more = h("button", { class: "button small", type: "button", hidden: true, onclick: () => load(next) }, "More issues");
  const loaded = [];
  let next = null;
  const draw = () => {
    const shown = issueGroups.map((state) => {
      const of = loaded.filter((i) => i.state === state).sort((a, b) => priorityRank(a.priority) - priorityRank(b.priority));
      return of.length ? h("section", { class: "panel" }, h("div", { class: "title" }, issueBadge(state), h("span", { class: "muted" }, String(of.length))),
        h("table", { class: "issues" }, h("tbody", {}, of.map((i) => issueRow(org, i))))) : null;
    }).filter(Boolean);
    const filtered = params.get("assignee") || params.get("repository") || params.get("label") || params.get("q");
    groups.replaceChildren(...(shown.length ? shown : [h("section", { class: "panel" }, h("p", {}, filtered ? "No issues match." : closed ? "No issues yet."
      : can("issues.write") ? "No open issues. Create the first." : "No open issues."))]));
  };
  async function load(from) {
    more.disabled = true;
    try {
      const page = await apiPage(from);
      loaded.push(...page.data.issues);
      next = page.next;
      more.hidden = !next;
      draw();
    } catch (err) {
      fail(err);
    } finally {
      more.disabled = false;
    }
  }
  main.replaceChildren(h("div", { class: "row" }, h("h1", {}, "Issues"), h("div", {},
    h("a", { class: "button small", href: base + org.slug + "/issues/labels", "data-nav": "" }, "Labels"), " ",
    can("issues.write") ? h("a", { class: "button small primary", href: base + org.slug + "/issues/new", "data-nav": "" }, "New issue") : null)),
  error, h("div", { class: "filters" }, assignee, repository, label, text, switcher), groups, h("div", {}, more));
  await load("/orgs/" + org.slug + "/issues?" + query);
}

// issueRow is a list's row of an issue, as the design system's Issue row draws it: its priority, identifier and
// title, its labels and repository, its assignee's avatar, and when it last changed.
const issueRow = (org, i) => h("tr", {}, h("td", { class: "priority" }, priorityIcon(i.priority)),
  h("td", { class: "key" }, h("a", { href: base + issuePath(org, i).slice(1), "data-nav": "" }, h("code", {}, i.identifier))),
  h("td", { class: "name" }, h("a", { class: "subject", href: base + issuePath(org, i).slice(1), "data-nav": "" }, i.title)),
  h("td", { class: "meta" }, i.labels.map(labelChip), i.repository ? h("span", { class: "chip" }, i.repository.slug) : null, i.assignee ? avatar(i.assignee) : null),
  h("td", { class: "when muted" }, when(i.updatedAt)));

// showNewIssue asks for a title, then the rest, as an issue's side column does, and creates the issue.
async function showNewIssue(org, main, can) {
  const { members, agents, repositories, labels } = await issueChoices(org);
  const error = h("p", { class: "alert", hidden: true });
  const title = h("input", { name: "title", required: true, maxlength: 200, placeholder: "Show pushes before the client returns" });
  const picks = labelPicks(labels, []);
  const description = h("textarea", { name: "description", rows: 8, maxlength: 65536, placeholder: "What it is and why, in Markdown" });
  const state = stateSelect("backlog"), priority = prioritySelect("none"), assignee = assigneeSelect(members, agents, can("agents.assign"), null), repository = repositorySelect(repositories, null);
  const create = h("button", { class: "button primary", type: "submit" }, "Create");
  main.replaceChildren(h("div", { class: "heading" }, h("p", {}, h("a", { href: base + org.slug + "/issues", "data-nav": "" }, "Issues"), " / new"), h("h1", {}, "New issue")),
    error, h("section", { class: "panel" }, h("form", { onsubmit: (e) => {
      e.preventDefault();
      create.disabled = true;
      api("POST", "/orgs/" + org.slug + "/issues", { title: title.value, description: description.value, state: state.value, priority: priority.value,
        assigneeId: assignee.value || undefined, repositoryId: repository.value || undefined, labelIds: pickedLabels(picks) })
        .then((i) => go(issuePath(org, i))).catch((err) => { create.disabled = false; error.textContent = err.message; error.hidden = false; });
    } },
      h("label", {}, "Title", title), h("label", {}, "Description", h("span", {}, "Markdown, as the Console renders a repository's."), description),
      h("div", { class: "fields" }, h("label", {}, "State", state), h("label", {}, "Priority", priority), h("label", {}, "Assignee", assignee),
        h("label", {}, "Repository", h("span", {}, "What it is about, if one thing."), repository)),
      labels.length ? h("div", { class: "field" }, h("strong", {}, "Labels"), picks) : null,
      h("div", {}, create))));
  title.focus();
}

// showIssue is an issue's page: its identifier and state, its title and rendered description, which the viewer may
// edit, its activity, oldest first, and its comments, live; beside them, the side column changes its state,
// priority, assignee and
// repository, each at once and from the version the page shows, and offers a branch name. It opens the issue by
// the number its identifier ends in, so a link made before the organization changed its key still opens it.
async function showIssue(org, main, can, identifier, notice) {
  const number = /^(?:[A-Za-z][A-Za-z0-9]*-)?(\d+)$/.exec(identifier);
  if (!number) return go("/" + org.slug + "/issues", true);
  const path = "/orgs/" + org.slug + "/issues/" + number[1];
  let issue;
  try {
    issue = await api("GET", path);
  } catch (err) {
    if (err.status !== 404) throw err;
    return main.replaceChildren(h("h1", {}, "No such issue"), h("p", {}, "There is no issue ", h("code", {}, identifier), " in " + org.name + "."),
      h("div", {}, h("a", { class: "button", href: base + org.slug + "/issues", "data-nav": "" }, "Back to the issues")));
  }
  if (issue.identifier !== identifier) return go(issuePath(org, issue), true);
  const write = can("issues.write");
  const [events, branches, choices, comments] = await Promise.all([api("GET", path + "/activity").then((d) => d.events),
    api("GET", path + "/branches").then((d) => d.branches), write ? issueChoices(org) : null, issueChat(path, can)]);
  const error = h("p", { class: "alert", hidden: true });
  const fail = (err) => { error.textContent = err.message; error.hidden = false; };
  const refresh = (message) => showIssue(org, main, can, issue.identifier, message);
  const tag = '"' + issue.version + '"';
  // save changes the fields patch names from the version shown, and shows the issue as it then is: when someone
  // changed it meanwhile, as they left it, saying so.
  const save = (patch) => api("PATCH", path, patch, { "Content-Type": "application/merge-patch+json", "If-Match": tag })
    .then(() => refresh()).catch((err) => (err.status === 412 ? refresh("Someone changed it meanwhile: here it is as they left it. Make your change again.") : fail(err)));
  const heading = h("div", { class: "heading" }, h("p", {}, h("a", { href: base + org.slug + "/issues", "data-nav": "" }, "Issues"), " / ", h("code", {}, issue.identifier)),
    h("div", { class: "title" }, h("code", {}, issue.identifier), issueBadge(issue.state)), h("h1", {}, issue.title));
  const body = h("section", { class: "panel" });
  const show = () => body.replaceChildren(h("div", { class: "row" }, h("h3", {}, "Description"),
    write ? h("button", { class: "button small", type: "button", onclick: edit }, "Edit") : null),
  issue.descriptionHtml ? rendered(issue.descriptionHtml) : h("p", {}, "No description."));
  function edit() {
    const title = h("input", { name: "title", required: true, maxlength: 200, value: issue.title });
    const description = h("textarea", { name: "description", rows: 12, maxlength: 65536 });
    description.value = issue.description;
    body.replaceChildren(h("form", { onsubmit: (e) => { e.preventDefault(); save({ title: title.value, description: description.value }); } },
      h("label", {}, "Title", title), h("label", {}, "Description", h("span", {}, "Markdown."), description),
      h("div", { class: "row" }, h("div", {}, h("button", { class: "button primary", type: "submit" }, "Save"), " ",
        h("button", { class: "button", type: "button", onclick: show }, "Cancel")))));
    title.focus();
  }
  show();
  const linked = h("section", { class: "panel" }, h("h3", {}, "Branches"), branches.length
    ? h("ul", { class: "linked" }, branches.map((b) => issueBranchLine(org, b)))
    : h("p", {}, "No branch works on it yet: push one whose name holds ", h("code", {}, issue.identifier.toLowerCase()), ", or link one from its page."));
  const activity = h("section", { class: "panel" }, h("h3", {}, "Activity"), h("ul", { class: "activity" }, events.flatMap(issueEventLines)));
  const notes = h("p", { class: "alert", hidden: !notice }, notice || "");
  main.replaceChildren(heading, notes, error, h("div", { class: "issue" }, h("div", { class: "column" }, body, linked, activity, comments),
    issueSide(org, issue, can, choices, save, tag, fail)));
}

// issueBranchLine is a branch linked to an issue as a branch row: its repository and name, linked to its page while
// it lives, its status, and how it was linked.
function issueBranchLine(org, b) {
  const name = h("code", {}, b.repository.slug + " / " + b.name);
  const page = base + org.slug + "/" + b.repository.slug + "/branches/" + b.name.split("/").map(encodeURIComponent).join("/");
  return h("li", {}, icon("git-branch"), b.deleted ? name : h("a", { href: page, "data-nav": "" }, name),
    b.deleted ? h("span", { class: "chip" }, "deleted") : b.status ? branchBadge(b.status) : null,
    h("span", { class: "muted" }, b.linkedBy === "name" ? "by its name" : "by hand"));
}

// issueSide is an issue's side column: its fields, each a select that saves at once for a viewer with issues.write
// and a fact otherwise, who created it and when, the branch name to copy, and Delete for a viewer with issues.manage.
function issueSide(org, issue, can, choices, save, tag, fail) {
  const field = (label, select, patch, fact) => h("label", {}, label, choices ? Object.assign(select, { onchange: (e) => { e.target.disabled = true; save(patch(e.target.value)); } })
    : h("span", { class: "fact" }, fact));
  const name = branchName(issue);
  const code = h("code", {}, name);
  const copy = h("button", { class: "button small", type: "button", onclick: () => navigator.clipboard.writeText(name).then(() => { copy.textContent = "Copied"; })
    .catch(() => getSelection().selectAllChildren(code)) }, "Copy");
  const remove = () => {
    if (!confirm("Delete " + issue.identifier + " and its activity? An issue that won't be done is Canceled instead.")) return;
    api("DELETE", "/orgs/" + org.slug + "/issues/" + issue.number, undefined, { "If-Match": tag }).then(() => go("/" + org.slug + "/issues")).catch(fail);
  };
  return h("aside", { class: "side" },
    field("State", choices && stateSelect(issue.state), (v) => ({ state: v }), issueBadge(issue.state)),
    field("Priority", choices && prioritySelect(issue.priority), (v) => ({ priority: v }), [priorityIcon(issue.priority), " ", issuePriorities[issue.priority].word]),
    field("Assignee", choices && assigneeSelect(choices.members, choices.agents, can("agents.assign"), issue.assignee), (v) => ({ assigneeId: v || null }),
      issue.assignee ? issue.assignee.displayName : "Nobody"),
    issue.task ? h("div", { class: "field" }, h("strong", {}, "Its task"), h("a", { class: "row", href: base + org.slug + "/tasks?" + new URLSearchParams({ agent: issue.assignee.id }),
      "data-nav": "", title: "The agent's tasks" }, h("span", {}, "#" + issue.task.number), taskBadge(issue.task.state))) : null,
    field("Repository", choices && repositorySelect(choices.repositories, issue.repository), (v) => ({ repositoryId: v || null }), issue.repository ? h("code", {}, issue.repository.slug) : "None"),
    issueLabelsField(org, issue, choices, save),
    h("dl", { class: "facts" }, h("dt", {}, "Created by"), h("dd", {}, issue.createdBy ? issue.createdBy.displayName : "Someone since gone"),
      h("dt", {}, "Created"), h("dd", {}, whenExactly(issue.createdAt)), issue.closedAt ? [h("dt", {}, "Closed"), h("dd", {}, whenExactly(issue.closedAt))] : null),
    h("div", { class: "branch-name" }, h("h3", {}, "Branch name"), h("p", {}, "Its identifier and title, for the branch that works on it."), h("div", { class: "row" }, code, copy)),
    can("issues.manage") ? h("div", {}, h("button", { class: "button small danger", type: "button", onclick: remove }, "Delete")) : null);
}

// issueLabelsField is the side column's labels: boxes to tick, each saving at once, for a viewer with issues.write,
// and the labels it carries otherwise.
function issueLabelsField(org, issue, choices, save) {
  if (!choices) return h("div", { class: "field" }, h("strong", {}, "Labels"), issue.labels.length ? h("div", { class: "label-picks" }, issue.labels.map(labelChip)) : h("span", { class: "fact" }, "None"));
  if (!choices.labels.length) return h("div", { class: "field" }, h("strong", {}, "Labels"), h("span", { class: "fact" }, "The organization has no ",
    h("a", { href: base + org.slug + "/issues/labels", "data-nav": "" }, "labels"), " yet."));
  const picks = labelPicks(choices.labels, issue.labels.map((l) => l.id));
  picks.addEventListener("change", () => {
    picks.querySelectorAll("input").forEach((i) => { i.disabled = true; });
    save({ labelIds: pickedLabels(picks) });
  });
  return h("div", { class: "field" }, h("strong", {}, "Labels"), picks);
}

// issueEventLines are an event of an issue's activity as lines: who did it, what, and when; one line per field a
// change changed.
function issueEventLines(e) {
  const line = (...what) => h("li", {}, h("span", {}, h("strong", {}, e.actor ? e.actor.displayName : "Someone since gone"), " ", ...what), h("span", { class: "muted" }, whenExactly(e.at)));
  if (e.kind === "issue.created") return [line("created it")];
  if (e.kind === "issue.branch_linked" || e.kind === "issue.branch_unlinked") {
    const b = e.branch;
    const what = b && b.name ? h("code", {}, b.repository + " / " + b.name) : "a branch of a repository you don't read";
    return [line(e.kind === "issue.branch_linked" ? "linked " : "unlinked ", what, b && b.linkedBy === "name" ? " by its name" : "")];
  }
  if (e.kind !== "issue.changed") return [line(h("code", {}, e.kind))];
  return e.changes.map((c) => line(...issueChangeWords(c)));
}

// issueChangeWords says what a change did to a field, with its values as people read them.
function issueChangeWords(c) {
  const quoted = (s) => "“" + s + "”";
  const word = (names, v) => (names[v] ? names[v].word : v);
  switch (c.field) {
    case "title": return ["renamed it from ", quoted(c.from), " to ", quoted(c.to)];
    case "description": return ["changed its description"];
    case "state": return ["moved it from ", word(issueStates, c.from), " to ", word(issueStates, c.to)];
    case "priority": return ["changed its priority from ", word(issuePriorities, c.from), " to ", word(issuePriorities, c.to)];
    case "assignee": return c.to ? (c.from ? ["reassigned it from ", c.from, " to ", c.to] : ["assigned it to ", c.to]) : ["unassigned ", c.from || "it"];
    case "repository": return c.to ? ["made it about ", h("code", {}, c.to)] : c.from ? ["made it about no repository instead of ", h("code", {}, c.from)] : ["changed its repository"];
    case "labels": return c.to ? (c.from ? ["changed its labels from ", c.from, " to ", c.to] : ["labeled it ", c.to]) : ["took off its labels ", c.from || ""];
  }
  return ["changed its ", c.field];
}

// showLabels lists the organization's labels by name, each with what it is for and the issues that carry it; for a
// viewer with issues.manage, each row edits or deletes its label from the version shown, and a form adds one.
async function showLabels(org, main, can, notice) {
  const path = "/orgs/" + org.slug + "/labels";
  const labels = (await api("GET", path)).labels;
  const manage = can("issues.manage");
  const error = h("p", { class: "alert", hidden: true });
  const fail = (err) => { error.textContent = err.message; error.hidden = false; };
  const refresh = (message) => showLabels(org, main, can, message);
  const stale = (err) => (err.status === 412 ? refresh("Someone changed it meanwhile: here it is as they left it. Make your change again.") : fail(err));
  const issuesOf = (l) => h("a", { href: base + org.slug + "/issues?closed=1&label=" + l.id, "data-nav": "" }, l.issues === 1 ? "1 issue" : l.issues + " issues");
  const row = (l) => {
    const tr = h("tr", {}, h("td", {}, labelChip(l)), h("td", { class: "what" }, l.description), h("td", { class: "count" }, issuesOf(l)),
      manage ? h("td", { class: "actions" }, h("button", { class: "button small", type: "button", onclick: () => tr.replaceWith(editRow(l)) }, "Edit"), " ",
        h("button", { class: "button small danger", type: "button", onclick: () => remove(l) }, "Delete")) : null);
    return tr;
  };
  const remove = (l) => {
    if (!confirm("Delete the label " + l.name + "?" + (l.issues ? " It comes off " + (l.issues === 1 ? "its issue." : "its " + l.issues + " issues.") : ""))) return;
    api("DELETE", path + "/" + l.id, undefined, { "If-Match": '"' + l.version + '"' }).then(() => refresh()).catch(stale);
  };
  const fields = (l) => [h("input", { name: "name", required: true, maxlength: 50, value: l ? l.name : "", "aria-label": "Name", placeholder: "Name" }),
    colorSelect(l ? l.color : "gray"), h("input", { name: "description", maxlength: 200, value: l ? l.description : "", "aria-label": "Description", placeholder: "What it is for" })];
  const editRow = (l) => {
    const [name, color, description] = fields(l);
    return h("tr", {}, h("td", { colspan: 4 }, h("form", { onsubmit: (e) => {
      e.preventDefault();
      api("PATCH", path + "/" + l.id, { name: name.value, color: color.value, description: description.value },
        { "Content-Type": "application/merge-patch+json", "If-Match": '"' + l.version + '"' }).then(() => refresh()).catch(stale);
    } }, name, color, description, h("div", {}, h("button", { class: "button small primary", type: "submit" }, "Save"), " ",
      h("button", { class: "button small", type: "button", onclick: () => refresh() }, "Cancel")))));
  };
  const [name, color, description] = fields(null);
  const add = manage ? h("section", { class: "panel" }, h("h3", {}, "New label"), h("form", { class: "labels-new", onsubmit: (e) => {
    e.preventDefault();
    api("POST", path, { name: name.value, color: color.value, description: description.value }).then(() => refresh()).catch(fail);
  } }, h("div", { class: "fields" }, h("label", {}, "Name", name), h("label", {}, "Colour", color), h("label", {}, "Description", description)),
    h("div", {}, h("button", { class: "button primary", type: "submit" }, "Create")))) : null;
  main.replaceChildren(h("div", { class: "heading" }, h("p", {}, h("a", { href: base + org.slug + "/issues", "data-nav": "" }, "Issues"), " / labels"), h("h1", {}, "Labels")),
    h("p", { class: "alert", hidden: !notice }, notice || ""), error,
    h("section", { class: "panel" }, labels.length ? h("table", { class: "labels-table" }, h("tbody", {}, labels.map(row)))
      : h("p", {}, manage ? "No labels yet. Create the first below." : "No labels yet.")), ...(add ? [add] : []));
}
