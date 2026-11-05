import { api, apiPage } from "../../lib/api.js";
import { branchBadge } from "../../lib/branches.js";
import { issueSubject } from "../../lib/chat.js";
import { ask, openDialog } from "../../lib/dialog.js";
import { h } from "../../lib/dom.js";
import { banner, failure, field, input, selectBox, textarea } from "../../lib/forms.js";
import { since, when, whenExactly } from "../../lib/format.js";
import { avatar, icon } from "../../lib/icons.js";
import { issueStates } from "../../lib/issues.js";
import { pageHeading } from "../../lib/layout.js";
import { rendered } from "../../lib/markdown.js";
import { popover } from "../../lib/menu.js";
import { go } from "../../lib/router.js";
import { remember } from "../../lib/sidebar.js";
import { base, me } from "../../lib/state.js";
import { taskBadge } from "../../lib/tasks.js";

// nav puts Issues in the sidebar, counting the open ones, with the newest five open issues under it, after those the
// viewer opened last.
export const nav = { label: "Issues", order: 10, icon: "circle-dot",
  items: ({ org }) => api("GET", "/orgs/" + org.slug + "/issues?limit=5").then((d) => ({ count: d.counts.open, items: d.issues.map((i) => issueItem(org, i)) })) };
export const allowed = (can) => can("issues.read");
export const crumbs = ({ org, sub }) => [{ label: "Issues", href: base + org.slug + "/issues" },
  ...(sub === "new" ? [{ label: "New" }] : sub === "labels" ? [{ label: "Labels" }] : sub ? [{ label: sub }] : [])];
// subject is an issue's conversation, its comments, on the issue's page.
export const subject = ({ org, can, sub }) => {
  const number = sub && /^(?:[A-Za-z][A-Za-z0-9]*-)?(\d+)$/.exec(sub);
  return number ? issueSubject(org, Number(number[1]), sub, can) : null;
};

// heading is the list's Page header, with New issue for a viewer with issues.write and the tabs Open, Closed and
// All; New issue's and Labels'; and none on an issue's page, which heads itself.
export const heading = ({ org, can, sub }) => {
  if (sub === "new") return pageHeading({ title: "New issue", description: "Say what should change and why. Assign it to an agent to have it start." });
  if (sub === "labels") return pageHeading({ title: "Labels", description: "What " + org.name + " tags its issues with, to find them by.", actions: [] });
  if (sub) return null;
  const params = new URLSearchParams(location.search);
  return pageHeading({ title: "Issues", description: "What " + org.name + " has decided to do, across its repositories. Assign an issue to an agent and it opens a branch and a task.",
    actions: can("issues.write") ? [newIssueButton(org)] : [],
    tabs: listTabs.map(([state, label]) => h("a", { class: "tab", href: base + listPath(org, params, { state: state === "open" ? "" : state }).slice(1), "data-nav": "",
      "data-state": state, "aria-current": state === stateOf(params) ? "page" : null }, label, state === "all" ? null : h("span", { class: "tab-count", hidden: true }))) });
};
export default ({ org, main, can, sub, head }) => showIssues(org, main, can, sub, head);

// The list's tabs, each the states it shows: Open, Closed (done and canceled) and All.
const listTabs = [["open", "Open"], ["closed", "Closed"], ["all", "All"]];
const closedStates = ["done", "canceled"];
const stateOf = (params) => (["closed", "all"].includes(params.get("state")) ? params.get("state") : "open");
// listPath is the list's path with params, each of changes set to its value, or dropped for none.
function listPath(org, params, changes) {
  const p = new URLSearchParams(params);
  for (const [name, value] of Object.entries(changes)) if (value) p.set(name, value); else p.delete(name);
  return "/" + org.slug + "/issues" + (p.toString() ? "?" + p : "");
}
const newIssueButton = (org) => h("a", { class: "btn btn-primary", href: base + org.slug + "/issues/new", "data-nav": "" }, icon("plus"), "New issue");

// The priorities, most urgent first.
const issuePriorities = {
  urgent: { word: "Urgent", icon: "triangle-alert" },
  high: { word: "High", icon: "signal-high" },
  medium: { word: "Medium", icon: "signal-medium" },
  low: { word: "Low", icon: "signal-low" },
  none: { word: "No priority", icon: "minus" },
};
// stateIcon is an issue's state as the Issues table leads its row with it: circle-dot while it is open, and its
// state's own icon once done or canceled, named in its tooltip.
function stateIcon(state) {
  const s = issueStates[state];
  const el = closedStates.includes(state) ? icon(s.icon, s.word) : icon("circle-dot", s.word);
  el.classList.add(closedStates.includes(state) ? "tone-" + s.tone : "tone-success");
  return el;
}
// stateTag is an issue's state as a Tag in its tone.
const stateTag = (state) => h("span", { class: "tag tone-" + issueStates[state].tone }, issueStates[state].word);
// issuePath is an issue's page under the Console's root: by its identifier, though the page reads only its number.
const issuePath = (org, i) => "/" + org.slug + "/issues/" + i.identifier;
// issueItem is an issue as the sidebar's Issues lists it: its identifier and title.
const issueItem = (org, i) => ({ key: String(i.number), label: i.identifier + " " + i.title, href: base + issuePath(org, i).slice(1), icon: "circle-dot" });
// branchHref is a live branch's page.
const branchHref = (org, repo, name) => base + org.slug + "/" + repo + "/branches/" + name.split("/").map(encodeURIComponent).join("/");
// branchName is the name to give a branch that works on an issue: its identifier and title in lowercase words,
// cut at a word within 60 characters, as Linear's "Copy git branch name" does.
function branchName(i) {
  const words = i.title.toLowerCase().normalize("NFKD").replace(/[̀-ͯ'’]/g, "").replace(/[^a-z0-9]+/g, "-").replace(/^-+|-+$/g, "");
  const name = (i.identifier.toLowerCase() + "-" + words).replace(/-$/, "");
  return name.length <= 60 ? name : name.slice(0, 61).replace(/-[^-]*$/, "");
}
// issueChoices are whom an issue may be assigned to, what it may be about and what it may carry: the
// organization's members and agents, the repositories the viewer reads, and the organization's labels.
async function issueChoices(org) {
  const [members, agents, repositories, labels] = await Promise.all([api("GET", "/orgs/" + org.slug + "/members").then((d) => d.members),
    api("GET", "/orgs/" + org.slug + "/agents").then((d) => d.agents),
    api("GET", "/orgs/" + org.slug + "/repos").then((d) => d.repositories), api("GET", "/orgs/" + org.slug + "/labels").then((d) => d.labels)]);
  return { members, agents, repositories, labels, agentIds: new Set(agents.map((a) => a.id)) };
}
// A label's colour is one of the design system's tones, so it reads in both themes.
const labelTones = { gray: "neutral", blue: "info", green: "success", yellow: "warning", red: "danger" };
const labelColors = { gray: "Gray", blue: "Blue", green: "Green", yellow: "Yellow", red: "Red" };
// labelTag is a label as a Tag in its colour's tone.
const labelTag = (l) => h("span", { class: "tag tone-" + labelTones[l.color], title: l.description || l.name }, l.name);
// person is a principal as a Person cell or fact shows them: their avatar, an agent's its own, and their name.
const person = (p, agentIds) => h("span", { class: "cell-person" }, avatar(p.displayName, agentIds.has(p.id)), p.displayName);
// item is a Menu item that checks one choice of several (radio) or toggles one (checkbox), and runs pick.
const item = (word, checked, pick, lead, role = "menuitemradio") => h("button", { class: "menu-item", type: "button", role, "aria-checked": String(checked), onclick: pick },
  lead || null, h("span", {}, word));

// showIssues is the Issues page's: the list, the form that creates one, the labels, or an issue's page, by what
// follows issues/.
function showIssues(org, main, can, sub, head) {
  if (sub === "new" && can("issues.write")) return showNewIssue(org, main, can);
  if (sub === "labels") return showLabels(org, main, can, head);
  if (sub && sub !== "new") return showIssue(org, main, can, sub);
  return showIssueList(org, main, can, head);
}

// showIssueList is the organization's issues as a table under its filters, newest first: those open, closed or all,
// as the tab the page's query names says, filtered by a text that finds titles, descriptions and identifiers, and by
// the repository, the assignee and a label the query names. The text filters as it is typed; the others reload the
// page.
async function showIssueList(org, main, can, head) {
  const choices = await issueChoices(org);
  const error = failure();
  const tbody = h("tbody", {});
  const table = h("table", { class: "table issues-table", hidden: true }, h("thead", {}, h("tr", {}, ["Issue", "Repository", "Assignee", "Branch", "Updated"].map((t) => h("th", {}, t)))), tbody);
  const none = h("div", { class: "card", hidden: true });
  const count = h("span", { class: "filters-count" });
  const more = h("button", { class: "btn btn-sm", type: "button", hidden: true, onclick: () => load(next, true) }, "More issues");
  let next = null;
  let typing = 0;
  const text = h("input", { type: "search", placeholder: "Filter issues", "aria-label": "Filter issues", value: new URLSearchParams(location.search).get("q") || "",
    oninput: () => {
      clearTimeout(typing);
      typing = setTimeout(() => {
        history.replaceState(null, "", base + listPath(org, new URLSearchParams(location.search), { q: text.value.trim() }).slice(1));
        load(null, false);
      }, 250);
    } });
  // load reads the list's first page for the query the address names now, or the page at from, which more adds
  // under those shown, and redraws the counts and the tabs' links to keep the filters.
  async function load(from, adding) {
    const params = new URLSearchParams(location.search);
    more.disabled = true;
    try {
      const page = await apiPage(from || listQuery(org, params));
      const { issues, counts } = page.data;
      if (!adding) tbody.replaceChildren();
      tbody.append(...issues.map((i) => issueRow(org, i, choices.agentIds)));
      next = page.next;
      more.hidden = !next;
      const state = stateOf(params);
      count.textContent = state === "all" ? (counts.open + counts.closed) + " issues" : (state === "open" ? counts.open : counts.closed) + " " + state;
      table.hidden = !tbody.children.length;
      none.hidden = !table.hidden;
      none.replaceChildren(noIssues(org, can, params, counts));
      for (const tab of head?.querySelectorAll(".tab[data-state]") || []) {
        const s = tab.dataset.state;
        tab.href = base + listPath(org, params, { state: s === "open" ? "" : s }).slice(1);
        const n = tab.querySelector(".tab-count");
        if (n) { n.textContent = String(s === "open" ? counts.open : counts.closed); n.hidden = false; }
      }
    } catch (err) {
      error.fail(err);
    } finally {
      more.disabled = false;
    }
  }
  main.replaceChildren(error.el, h("div", { class: "filters" }, h("div", { class: "input" }, icon("search"), text), ...issueFilters(org, choices), count),
    table, none, h("div", {}, more));
  await load(null, false);
}

// listQuery is the API's query for the list's first page that params name: its states and filters, a hundred at a time.
function listQuery(org, params) {
  const query = new URLSearchParams({ limit: "100" });
  const state = stateOf(params);
  if (state !== "open") (state === "closed" ? closedStates : Object.keys(issueStates)).forEach((s) => query.append("state", s));
  for (const name of ["repository", "assignee", "label", "q"]) if (params.get(name)) query.set(name, params.get(name));
  return "/orgs/" + org.slug + "/issues?" + query;
}

// issueFilters are the list's Repository, Assignee and Label buttons, each opening a Menu of its choices, the one the
// query names checked, and saying it once one is; Label's ends with the way to the labels.
function issueFilters(org, { members, agents, repositories, labels }) {
  const pick = (name, label, options, extra) => {
    const current = new URLSearchParams(location.search).get(name) || "";
    const chosen = current && options.find((o) => o.value === current);
    const button = h("button", { class: "btn btn-sm", type: "button", "aria-haspopup": "menu" }, icon("chevron-down"), chosen ? label + ": " + chosen.word : label);
    button.addEventListener("click", () => popover(button, "end", (close) => [
      options.map((o) => item(o.word, o.value === current, () => { close(); go(listPath(org, new URLSearchParams(location.search), { [name]: o.value }), true); }, o.lead)),
      extra || null]));
    return button;
  };
  const people = [{ value: "", word: "Anyone" }, { value: me.id, word: "Me" }, { value: "none", word: "Nobody" },
    ...members.filter((m) => m.userId !== me.id).map((m) => ({ value: m.userId, word: m.displayName, lead: avatar(m.displayName) })),
    ...agents.map((a) => ({ value: a.id, word: a.name, lead: avatar(a.name, true) }))];
  return [
    pick("repository", "Repository", [{ value: "", word: "Any repository" }, { value: "none", word: "No repository" },
      ...repositories.map((r) => ({ value: r.id, word: r.slug, lead: icon("folder-git-2") }))]),
    pick("assignee", "Assignee", people),
    pick("label", "Label", [{ value: "", word: "Any label" }, ...labels.map((l) => ({ value: l.id, word: l.name, lead: labelTag(l) }))],
      [h("hr", { class: "menu-divider" }), h("a", { class: "menu-item", href: base + org.slug + "/issues/labels", "data-nav": "", role: "menuitem" }, h("span", {}, "Manage labels"))]),
  ];
}

// noIssues is the card the list shows when it has none to show: none yet, none open or closed, or none the filters
// match; New issue on those that invite one.
function noIssues(org, can, params, counts) {
  const filtered = ["repository", "assignee", "label", "q"].some((n) => params.get(n));
  const state = stateOf(params);
  const [title, text, invite] = filtered ? ["No issue matches", "Nothing in this tab matches the filters.", false]
    : state === "closed" ? ["No closed issues", "An issue shows here once it is done or canceled.", false]
      : counts.closed ? ["No open issues", "Every issue of " + org.name + "'s is done or canceled.", true]
        : ["No issues yet", "An issue says what should change and why. Assign one to an agent and it opens a branch for it.", true];
  return h("div", { class: "empty" }, h("div", { class: "empty-icon" }, icon("circle-dot")), h("h2", { class: "empty-title" }, title),
    h("p", { class: "empty-description" }, text),
    invite && can("issues.write") ? h("a", { class: "btn btn-primary", href: base + org.slug + "/issues/new", "data-nav": "" }, "New issue") : null);
}

// nothing is what an empty cell shows: a dash, as Table/Cell Kind=Unassigned draws it.
const nothing = () => h("span", { class: "cell-unassigned" }, "–");

// issueRow is an issue's row in the Issues table: its state, key, title and labels, its repository, its assignee, the
// live branch linked to it last, and when it last changed.
const issueRow = (org, i, agentIds) => h("tr", {},
  h("td", {}, h("span", { class: "cell-issue" }, stateIcon(i.state), h("code", {}, i.identifier),
    h("a", { href: base + issuePath(org, i).slice(1), "data-nav": "" }, i.title), i.labels.map(labelTag))),
  i.repository ? h("td", { class: "cell-mono" }, i.repository.slug) : h("td", {}, nothing()),
  h("td", {}, i.assignee ? person(i.assignee, agentIds) : h("span", { class: "cell-unassigned" }, "Unassigned")),
  i.branch ? h("td", { class: "cell-mono" }, h("a", { href: branchHref(org, i.branch.repository.slug, i.branch.name), "data-nav": "" }, i.branch.name))
    : h("td", {}, nothing()),
  h("td", { class: "cell-muted", title: whenExactly(i.updatedAt) }, since(i.updatedAt)));

// showNewIssue is the form that creates an issue: the repository it is about, its title and description, whom it is
// assigned to and its labels, by name, those the organization lacks made first for a viewer with issues.manage.
async function showNewIssue(org, main, can) {
  const { members, agents, repositories, labels } = await issueChoices(org);
  const error = failure();
  const repository = h("select", { name: "repositoryId" }, h("option", { value: "" }, "No repository"), repositories.map((r) => h("option", { value: r.id }, r.slug)));
  const title = input({ name: "title", required: true, maxlength: 200, placeholder: "Show pushes before the worker finishes" });
  const description = textarea({ name: "description", rows: 4, maxlength: 65536 });
  const assign = can("agents.assign");
  const assignee = h("select", { name: "assigneeId" }, h("option", { value: "" }, "Nobody yet"),
    members.map((m) => h("option", { value: m.userId }, m.displayName + (m.userId === me.id ? " (you)" : ""))),
    assign ? agents.map((a) => h("option", { value: a.id }, a.name + " · agent")) : null);
  const named = input({ name: "labels", placeholder: labels.slice(0, 2).map((l) => l.name).join(", ") || "bug, docs", autocomplete: "off" });
  const create = h("button", { class: "btn btn-primary", type: "submit" }, "Create issue");
  main.replaceChildren(error.el, h("form", { class: "card new-issue", onsubmit: async (e) => {
    e.preventDefault();
    create.disabled = true;
    try {
      const labelIds = await labelsNamed(org, labels, named.value, can("issues.manage"));
      const i = await api("POST", "/orgs/" + org.slug + "/issues", { title: title.value, description: description.value,
        assigneeId: assignee.value || undefined, repositoryId: repository.value || undefined, labelIds });
      go(issuePath(org, i));
    } catch (err) {
      error.fail(err);
      create.disabled = false;
    }
  } },
  h("div", { class: "card-body" },
    field("Repository", selectBox(repository), "What it is about, if it is about one repository; its branches open there."),
    field("Title", title),
    field("Description", description, "Markdown. Evidence and \"Done when\" help an agent act on it."),
    field("Assignee", selectBox(assignee), assign ? "A person, an agent, or nobody yet." : "A person, or nobody yet."),
    field("Labels", named, can("issues.manage") ? "Comma-separated; new ones are made." : "Comma-separated, of the organization's labels.")),
  h("footer", { class: "card-footer" }, h("a", { class: "btn", href: base + org.slug + "/issues", "data-nav": "" }, "Cancel"), create)));
  title.focus();
}

// labelsNamed answers the IDs of the labels a comma-separated text names, by name in any case, making those the
// organization lacks for a viewer who may (manage), and refusing them otherwise.
async function labelsNamed(org, labels, text, manage) {
  const names = [...new Set(text.split(",").map((n) => n.trim()).filter(Boolean))];
  const ids = [];
  for (const name of names) {
    const known = labels.find((l) => l.name.toLowerCase() === name.toLowerCase());
    if (known) { ids.push(known.id); continue; }
    if (!manage) throw new Error("There is no label " + name + ", and making one takes issues.manage.");
    const made = await api("POST", "/orgs/" + org.slug + "/labels", { name, color: "gray", description: "" });
    labels.push(made);
    ids.push(made.id);
  }
  return ids;
}

// showIssue is an issue's page: its key, state and title over its rendered description, the branches linked to it
// and its activity, oldest first, without its comments, which the conversation pane holds; beside them, its
// properties, each changed from its menu for a viewer with issues.write, from the version the page shows, and Assign
// to an agent. It opens the issue by the number its identifier ends in, so a link made before the organization
// changed its key still opens it.
async function showIssue(org, main, can, identifier, notice) {
  const number = /^(?:[A-Za-z][A-Za-z0-9]*-)?(\d+)$/.exec(identifier);
  if (!number) return go("/" + org.slug + "/issues", true);
  const path = "/orgs/" + org.slug + "/issues/" + number[1];
  let issue;
  try {
    issue = await api("GET", path);
  } catch (err) {
    if (err.status !== 404) throw err;
    return main.replaceChildren(h("div", { class: "card" }, h("div", { class: "empty" }, h("div", { class: "empty-icon" }, icon("circle-dot")),
      h("h2", { class: "empty-title" }, "No such issue"), h("p", { class: "empty-description" }, "There is no issue " + identifier + " in " + org.name + "."),
      h("a", { class: "btn", href: base + org.slug + "/issues", "data-nav": "" }, "Back to the issues"))));
  }
  if (issue.identifier !== identifier) return go(issuePath(org, issue), true);
  const write = can("issues.write");
  remember(org, "issues", issueItem(org, issue));
  const [events, branches, choices] = await Promise.all([api("GET", path + "/activity").then((d) => d.events),
    api("GET", path + "/branches").then((d) => d.branches), issueChoices(org)]);
  const error = failure();
  const refresh = (message) => showIssue(org, main, can, issue.identifier, message);
  const tag = '"' + issue.version + '"';
  // save changes the fields patch names from the version shown, and shows the issue as it then is: when someone
  // changed it meanwhile, as they left it, saying so.
  const save = (patch) => api("PATCH", path, patch, { "Content-Type": "application/merge-patch+json", "If-Match": tag })
    .then(() => refresh()).catch((err) => (err.status === 412 ? refresh("Someone changed it meanwhile: here it is as they left it. Make your change again.") : error.fail(err)));
  const page = { org, issue, can, choices, save, fail: error.fail, path, tag };
  main.replaceChildren(notice ? banner("warning", null, notice) : "", error.el, h("div", { class: "issue-columns" },
    h("div", { class: "issue-main" }, issueHeader(page), issue.descriptionHtml ? rendered(issue.descriptionHtml)
      : h("p", { class: "page-header-description" }, write ? "No description yet. Edit adds one." : "No description."),
    branchesCard(org, issue, branches), issueActivity(events)),
    h("aside", { class: "issue-side" }, h("section", { class: "card" }, h("div", { class: "facts" }, issueFacts(page))),
      write && can("agents.assign") && choices.agents.length ? assignToAgent(page) : null)));
}

// issueHeader is the top of an issue's page: its key line, its key and state, then, for a viewer with issues.write,
// Edit, which edits its title and description, and More: its branch name to copy, and Delete for issues.manage; then
// its title.
function issueHeader({ org, issue, can, save, fail, path, tag }) {
  const name = branchName(issue);
  const more = h("button", { class: "icon-btn icon-btn-ghost icon-btn-sm", type: "button", "aria-haspopup": "menu", "aria-label": "More", title: "More" }, icon("ellipsis"));
  more.addEventListener("click", () => popover(more, "end", (close) => [
    h("button", { class: "menu-item", type: "button", role: "menuitem", title: name, onclick: () => { close(); navigator.clipboard.writeText(name).catch(fail); } },
      icon("copy"), h("span", {}, "Copy branch name")),
    can("issues.manage") ? [h("hr", { class: "menu-divider" }), h("button", { class: "menu-item menu-item-danger", type: "button", role: "menuitem", onclick: () => {
      close();
      ask("Delete " + issue.identifier + "?", "It goes with its activity and comments. An issue that won't be done is Canceled instead.", "Delete",
        () => api("DELETE", path, undefined, { "If-Match": tag }).then(() => go("/" + org.slug + "/issues")));
    } }, icon("trash-2"), h("span", {}, "Delete issue"))] : null]));
  const edit = () => {
    const title = input({ name: "title", required: true, maxlength: 200, value: issue.title });
    const description = textarea({ name: "description", rows: 10, maxlength: 65536 });
    description.value = issue.description;
    openDialog({ title: "Edit " + issue.identifier, action: "Save", body: [field("Title", title), field("Description", description, "Markdown.")],
      submit: () => save({ title: title.value, description: description.value }) });
  };
  return h("header", { class: "issue-header" },
    h("div", { class: "issue-key" }, h("code", { class: "row-mono" }, issue.identifier), stateTag(issue.state), h("span", { class: "issue-spacer" }),
      can("issues.write") ? h("button", { class: "btn btn-ghost btn-sm", type: "button", onclick: edit }, icon("pencil"), "Edit") : null, more),
    h("h1", { class: "page-header-title" }, issue.title));
}

// branchesCard is the card of the branches linked to an issue: each as a Branch row, its name, linked to its page
// while it lives, over its repository and how it was linked, and its status, or that it is deleted.
function branchesCard(org, issue, branches) {
  const row = (b) => h("li", { class: "branch-row" }, icon("git-branch"), h("div", { class: "row-text" },
    b.deleted ? h("span", { class: "row-title" }, b.name) : h("a", { class: "row-title", href: branchHref(org, b.repository.slug, b.name), "data-nav": "" }, b.name),
    h("span", { class: "row-meta", title: "Linked " + whenExactly(b.linkedAt) }, b.repository.slug + " · linked " + (b.linkedBy === "name" ? "by name" : "by hand"))),
  b.deleted ? h("span", { class: "tag" }, "Deleted") : b.status ? branchBadge(b.status) : null);
  return h("section", { class: "card" }, h("header", { class: "card-header" }, h("h2", { class: "card-title" }, "Branches"), h("span", { class: "card-meta" }, branches.length + " linked")),
    branches.length ? h("ul", { class: "list" }, branches.map(row))
      : h("div", { class: "card-body" }, h("p", { class: "page-header-description" }, "No branch works on it yet: push one whose name holds ",
        h("code", {}, issue.identifier.toLowerCase()), ", or link one from its page.")));
}

// issueActivity is an issue's activity, oldest first: each event a line of who did what, and when.
const issueActivity = (events) => h("section", { class: "issue-activity" },
  h("div", { class: "section-header" }, h("div", { class: "section-header-text" }, h("h2", { class: "section-header-title" }, "Activity"))),
  h("ol", { class: "issue-events" }, events.map((e) => h("li", { class: "event-row" }, h("span", {}, (e.actor ? e.actor.displayName : "Someone since gone") + " " + eventWords(e)),
    h("time", { datetime: e.at, title: whenExactly(e.at) }, since(e.at))))));

// eventWords says what an event did, a change each field it changed, joined by dots.
function eventWords(e) {
  if (e.kind === "issue.created") return "created it";
  if (e.kind === "issue.branch_linked" || e.kind === "issue.branch_unlinked") {
    const b = e.branch;
    const what = b && b.name ? b.repository + "/" + b.name : "a branch of a repository you don't read";
    return (e.kind === "issue.branch_linked" ? "linked " : "unlinked ") + what + (b && b.linkedBy === "name" ? " by its name" : "");
  }
  if (e.kind !== "issue.changed") return e.kind;
  const word = (names, v) => (names[v] ? names[v].word : v);
  return e.changes.map((c) => {
    switch (c.field) {
      case "title": return "renamed it to “" + c.to + "”";
      case "description": return "edited its description";
      case "state": return "moved it to " + word(issueStates, c.to);
      case "priority": return "set its priority to " + word(issuePriorities, c.to);
      case "assignee": return c.to ? "assigned " + c.to : "unassigned " + (c.from || "it");
      case "repository": return c.to ? "made it about " + c.to : "made it about no repository";
      case "labels": return c.to ? "labeled it " + c.to : "took off its labels";
    }
    return "changed its " + c.field;
  }).join(" · ");
}

// issueFacts are an issue's properties as Fact rows: its state, priority, assignee, the task its agent works on it
// in, labels, repository, and when and by whom it was created and closed. For a viewer with issues.write each of the
// first is a Small Ghost button that opens a Menu of its choices, checked as the issue stands, and saves a pick at
// once.
function issueFacts({ org, issue, can, choices, save }) {
  const write = can("issues.write");
  const fact = (label, ...value) => h("div", { class: "fact-row" }, h("span", { class: "fact-label" }, label), h("div", { class: "fact-value" }, value));
  // menu is a fact's value as a ghost button opening the items build(close) returns, or the value alone for a reader.
  const menu = (label, value, build) => {
    if (!write) return fact(label, value);
    const button = h("button", { class: "btn btn-ghost btn-sm", type: "button", "aria-haspopup": "menu", "aria-label": label + ": change it" }, value, icon("chevron-down"));
    button.addEventListener("click", () => popover(button, "start", build));
    return fact(label, button);
  };
  const { members, agents, repositories, labels } = choices;
  const held = issue.labels.map((l) => l.id);
  const offered = agents.filter((a) => can("agents.assign") || a.id === issue.assignee?.id);
  return [
    menu("Status", issueStates[issue.state].word, (close) => Object.entries(issueStates).map(([s, st]) =>
      item(st.word, s === issue.state, () => { close(); save({ state: s }); }, icon(st.icon)))),
    menu("Priority", issuePriorities[issue.priority].word, (close) => Object.entries(issuePriorities).map(([p, pr]) =>
      item(pr.word, p === issue.priority, () => { close(); save({ priority: p }); }, icon(pr.icon)))),
    menu("Assignee", issue.assignee ? issue.assignee.displayName : "Nobody", (close) => [item("Nobody", !issue.assignee, () => { close(); save({ assigneeId: null }); }),
      members.map((m) => item(m.displayName + (m.userId === me.id ? " (you)" : ""), m.userId === issue.assignee?.id, () => { close(); save({ assigneeId: m.userId }); }, avatar(m.displayName))),
      offered.map((a) => item(a.name, a.id === issue.assignee?.id, () => { close(); save({ assigneeId: a.id }); }, avatar(a.name, true)))]),
    issue.task ? fact("Task", h("a", { class: "cell-person", href: base + org.slug + "/tasks?" + new URLSearchParams({ agent: issue.assignee.id }), "data-nav": "" },
      "#" + issue.task.number), taskBadge(issue.task.state)) : null,
    menu("Labels", issue.labels.length ? issue.labels.map((l) => l.name).join(", ") : "None", (close) => labels.length
      ? labels.map((l) => item(l.name, held.includes(l.id), () => {
        close();
        save({ labelIds: held.includes(l.id) ? held.filter((id) => id !== l.id) : [...held, l.id] });
      }, labelTag(l), "menuitemcheckbox"))
      : [h("a", { class: "menu-item", href: base + org.slug + "/issues/labels", "data-nav": "", role: "menuitem" }, h("span", {}, "No labels yet: make one"))]),
    menu("Repository", issue.repository ? issue.repository.slug : "No repository", (close) => [
      item("No repository", !issue.repository, () => { close(); save({ repositoryId: null }); }),
      repositories.map((r) => item(r.slug, r.id === issue.repository?.id, () => { close(); save({ repositoryId: r.id }); }, icon("folder-git-2")))]),
    fact("Created", h("span", { title: whenExactly(issue.createdAt) }, when(issue.createdAt) + " by " + (issue.createdBy ? issue.createdBy.displayName : "someone since gone"))),
    issue.closedAt ? fact("Closed", h("span", { title: whenExactly(issue.closedAt) }, when(issue.closedAt))) : null,
  ];
}

// assignToAgent is Assign to an agent, which opens a Menu of the organization's agents and assigns the issue to the
// one picked, which takes it as a task.
function assignToAgent({ issue, choices, save }) {
  const button = h("button", { class: "btn", type: "button", "aria-haspopup": "menu" }, icon("sparkles"), "Assign to an agent");
  button.addEventListener("click", () => popover(button, "start", (close) => choices.agents.map((a) =>
    item(a.name, a.id === issue.assignee?.id, () => { close(); save({ assigneeId: a.id }); }, avatar(a.name, true)))));
  return button;
}

// showLabels lists the organization's labels by name, each with what it is for and the issues that carry it; for a
// viewer with issues.manage, New label and each row's Edit and Delete, from the version shown, in dialogs.
async function showLabels(org, main, can, head) {
  const path = "/orgs/" + org.slug + "/labels";
  const labels = (await api("GET", path)).labels;
  const manage = can("issues.manage");
  const error = failure();
  const refresh = () => showLabels(org, main, can, head);
  const color = (value) => h("select", { name: "color" }, Object.entries(labelColors).map(([c, word]) => h("option", { value: c, selected: c === value }, word)));
  // edit opens the dialog that makes a label, or changes l from the version shown.
  const edit = (l) => {
    const name = input({ name: "name", required: true, maxlength: 50, value: l ? l.name : "" });
    const hue = color(l ? l.color : "gray");
    const description = input({ name: "description", maxlength: 200, value: l ? l.description : "" });
    openDialog({ title: l ? "Edit " + l.name : "New label", action: l ? "Save" : "Create",
      body: [field("Name", name), field("Colour", selectBox(hue), "One of the design system's tones, so it reads in both themes."), field("Description", description, "What it is for.")],
      submit: () => (l ? api("PATCH", path + "/" + l.id, { name: name.value, color: hue.value, description: description.value },
        { "Content-Type": "application/merge-patch+json", "If-Match": '"' + l.version + '"' }) : api("POST", path, { name: name.value, color: hue.value, description: description.value }))
        .then(refresh) });
    name.focus();
  };
  const remove = (l) => ask("Delete " + l.name + "?", l.issues ? "It comes off " + (l.issues === 1 ? "its issue." : "its " + l.issues + " issues.") : "No issue carries it.", "Delete",
    () => api("DELETE", path + "/" + l.id, undefined, { "If-Match": '"' + l.version + '"' }).then(refresh));
  const actions = head?.querySelector(".page-header-actions");
  if (actions) actions.replaceChildren(manage ? h("button", { class: "btn btn-primary", type: "button", onclick: () => edit(null) }, icon("plus"), "New label") : "");
  const row = (l) => h("tr", {}, h("td", {}, labelTag(l)), h("td", { class: "cell-muted" }, l.description),
    h("td", { class: "cell-number" }, h("a", { href: base + org.slug + "/issues?state=all&label=" + l.id, "data-nav": "" }, l.issues === 1 ? "1 issue" : l.issues + " issues")),
    manage ? h("td", { class: "cell-actions" }, h("button", { class: "btn btn-ghost btn-sm", type: "button", onclick: () => edit(l) }, "Edit"),
      h("button", { class: "btn btn-ghost btn-sm", type: "button", onclick: () => remove(l) }, "Delete")) : null);
  main.replaceChildren(error.el, labels.length
    ? h("table", { class: "table" }, h("thead", {}, h("tr", {}, h("th", {}, "Label"), h("th", {}, "Description"), h("th", { class: "cell-number" }, "Issues"), manage ? h("th", {}, "") : null)),
      h("tbody", {}, labels.map(row)))
    : h("div", { class: "card" }, h("div", { class: "empty" }, h("div", { class: "empty-icon" }, icon("circle-dot")), h("h2", { class: "empty-title" }, "No labels yet"),
      h("p", { class: "empty-description" }, manage ? "A label tags issues to find them by. Make the first." : "Nobody has made a label yet."),
      manage ? h("button", { class: "btn btn-primary", type: "button", onclick: () => edit(null) }, icon("plus"), "New label") : null)));
}
