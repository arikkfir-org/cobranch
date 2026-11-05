import { api, apiPage } from "./api.js";
import { h } from "./dom.js";
import { failure } from "./forms.js";
import { since, whenExactly } from "./format.js";
import { avatar, icon } from "./icons.js";
import { popover } from "./menu.js";
import { go } from "./router.js";
import { base, me } from "./state.js";

// The issue states as the design system draws them, in the order the side column offers them: Done and Canceled
// close an issue. A list groups the open ones nearest to done first.
export const issueStates = {
  backlog: { word: "Backlog", icon: "circle-dashed", tone: "neutral" },
  todo: { word: "Todo", icon: "circle", tone: "neutral" },
  in_progress: { word: "In progress", icon: "loader-circle", tone: "info" },
  in_review: { word: "In review", icon: "eye", tone: "warning" },
  done: { word: "Done", icon: "circle-check", tone: "success" },
  canceled: { word: "Canceled", icon: "circle-slash", tone: "neutral" },
};
// issueBadge is an issue's state as a badge, as branchBadge draws a branch's.
export function issueBadge(state) {
  const s = issueStates[state];
  return h("span", { class: "badge tone-" + s.tone }, icon(s.icon), s.word);
}

// The list's tabs, each the states it shows: Open, Closed (done and canceled) and All.
const listTabs = [["open", "Open"], ["closed", "Closed"], ["all", "All"]];
const closedStates = ["done", "canceled"];
const stateOf = (params) => (["closed", "all"].includes(params.get("state")) ? params.get("state") : "open");
// listPath is the path of the list at where, an organization's issues or a repository's, with params, each of changes
// set to its value, or dropped for none.
function listPath(where, params, changes) {
  const p = new URLSearchParams(params);
  for (const [name, value] of Object.entries(changes)) if (value) p.set(name, value); else p.delete(name);
  return where + (p.toString() ? "?" + p : "");
}
// listTabLinks are the list's tabs Open, Closed and All, for the list at where under the Console's root, as the
// address's params name them; the list counts them once it reads its issues.
export const listTabLinks = (where, params) => listTabs.map(([state, label]) => h("a", { class: "tab", href: base + listPath(where, params, { state: state === "open" ? "" : state }).slice(1),
  "data-nav": "", "data-state": state, "aria-current": state === stateOf(params) ? "page" : null }, label, state === "all" ? null : h("span", { class: "tab-count", hidden: true })));

// stateIcon is an issue's state as the Issues table leads its row with it: circle-dot while it is open, and its
// state's own icon once done or canceled, named in its tooltip.
function stateIcon(state) {
  const s = issueStates[state];
  const el = closedStates.includes(state) ? icon(s.icon, s.word) : icon("circle-dot", s.word);
  el.classList.add(closedStates.includes(state) ? "tone-" + s.tone : "tone-success");
  return el;
}
// issuePath is an issue's page under the Console's root: by its identifier, though the page reads only its number.
export const issuePath = (org, i) => "/" + org.slug + "/issues/" + i.identifier;
// branchHref is a live branch's page.
export const branchHref = (org, repo, name) => base + org.slug + "/" + repo + "/branches/" + name.split("/").map(encodeURIComponent).join("/");
// issueChoices are whom an issue may be assigned to, what it may be about and what it may carry: the
// organization's members and agents, the repositories the viewer reads, and the organization's labels.
export async function issueChoices(org) {
  const [members, agents, repositories, labels] = await Promise.all([api("GET", "/orgs/" + org.slug + "/members").then((d) => d.members),
    api("GET", "/orgs/" + org.slug + "/agents").then((d) => d.agents),
    api("GET", "/orgs/" + org.slug + "/repos").then((d) => d.repositories), api("GET", "/orgs/" + org.slug + "/labels").then((d) => d.labels)]);
  return { members, agents, repositories, labels, agentIds: new Set(agents.map((a) => a.id)) };
}
// A label's colour is one of the design system's tones, so it reads in both themes.
export const labelTones = { gray: "neutral", blue: "info", green: "success", yellow: "warning", red: "danger" };
// labelTag is a label as a Tag in its colour's tone.
export const labelTag = (l) => h("span", { class: "tag tone-" + labelTones[l.color], title: l.description || l.name }, l.name);
// person is a principal as a Person cell or fact shows them: their avatar, an agent's its own, and their name.
const person = (p, agentIds) => h("span", { class: "cell-person" }, avatar(p.displayName, agentIds.has(p.id)), p.displayName);
// item is a Menu item that checks one choice of several (radio) or toggles one (checkbox), and runs pick; its word
// may be a node, such as a label's Tag.
export const item = (word, checked, pick, lead, role = "menuitemradio") => h("button", { class: "menu-item", type: "button", role, "aria-checked": String(checked), onclick: pick },
  lead || null, h("span", {}, word));
// menuAlign aligns a Menu of width wide to its button's start, or to its end when it would leave the window that way.
const menuAlign = (button, wide) => (button.getBoundingClientRect().left + wide > document.documentElement.clientWidth ? "end" : "start");

// issueList is an organization's issues, or those about one of its repositories, as a table under its filters,
// newest first: those open, closed or all, as the tab the page's query names says, filtered by a text that finds
// titles, descriptions and identifiers, and by the repository, the assignee and a label the query names. The text
// filters as it is typed; the others reload the page. tabs holds the list's tabs (listTabLinks), which it counts and
// keeps the filters in. A repository's list, at <repository>/issues, filters by it alone and leaves out its column.
export async function issueList({ org, can, tabs, repository }) {
  const list = { org, repository, where: "/" + org.slug + (repository ? "/" + repository.slug : "") + "/issues",
    names: ["repository", "assignee", "label", "q"].filter((n) => !(repository && n === "repository")) };
  const choices = await issueChoices(org);
  const error = failure();
  const tbody = h("tbody", {});
  const columns = [["Issue", ""], ["Repository", "col-repository"], ["Assignee", "col-assignee"], ["Branch", "col-branch"], ["Updated", "col-updated"]]
    .filter(([, c]) => !(repository && c === "col-repository"));
  const table = h("table", { class: "table issues-table", hidden: true }, h("thead", {}, h("tr", {}, columns.map(([t, c]) => h("th", { class: c || null }, t)))), tbody);
  const none = h("div", { class: "card issues-none", hidden: true });
  const count = h("span", { class: "filters-count" });
  const more = h("button", { class: "btn btn-sm", type: "button", hidden: true, onclick: () => load(next, true) }, "More issues");
  let next = null;
  let totals = null;
  let typing = 0;
  const text = h("input", { type: "search", placeholder: "Filter issues", "aria-label": "Filter issues", value: paramsOf(list).get("q") || "",
    oninput: () => {
      clearTimeout(typing);
      typing = setTimeout(() => {
        history.replaceState(null, "", base + listPath(list.where, paramsOf(list), { q: text.value.trim() }).slice(1));
        load(null, false);
      }, 250);
    } });
  // load reads the list's first page for the query the address names now, or the page at from, which more adds
  // under those shown, and redraws the counts and the tabs' links to keep the filters. The tabs count every issue
  // of theirs, and the count beside the filters what the filters match of them.
  async function load(from, adding) {
    const params = paramsOf(list);
    const filtered = list.names.some((n) => params.get(n));
    more.disabled = true;
    try {
      const [page, all] = await Promise.all([apiPage(from || listQuery(list, params)),
        filtered && !adding ? api("GET", listQuery(list, new URLSearchParams(), "1")).then((d) => d.counts) : null]);
      const { issues, counts } = page.data;
      if (!adding) totals = all || counts;
      if (!adding) tbody.replaceChildren();
      tbody.append(...issues.map((i) => issueRow(list, i, choices.agentIds)));
      next = page.next;
      more.hidden = !next;
      const state = stateOf(params);
      const of = (c, s = state) => (s === "all" ? c.open + c.closed : s === "open" ? c.open : c.closed);
      const word = state === "all" ? "issues" : state;
      count.textContent = (filtered ? of(counts) + " of " + of(totals) : of(counts)) + " " + word;
      table.hidden = !tbody.children.length;
      none.hidden = !table.hidden;
      none.replaceChildren(noIssues(list, can, params, totals, choices));
      for (const tab of tabs?.querySelectorAll(".tab[data-state]") || []) {
        const s = tab.dataset.state;
        tab.href = base + listPath(list.where, params, { state: s === "open" ? "" : s }).slice(1);
        const n = tab.querySelector(".tab-count");
        if (n) { n.textContent = String(of(totals, s)); n.hidden = false; }
      }
    } catch (err) {
      error.fail(err);
    } finally {
      more.disabled = false;
    }
  }
  await load(null, false);
  return [error.el, h("div", { class: "filters issues-filters" }, h("div", { class: "input" }, icon("search"), text), ...issueFilters(list, choices), count),
    table, none, h("div", {}, more)];
}

// paramsOf is the address's query as a list reads it: a repository's list leaves out the repository it is about.
function paramsOf(list) {
  const params = new URLSearchParams(location.search);
  if (list.repository) params.delete("repository");
  return params;
}

// listQuery is the API's query for list's first page that params name: its states and filters, limit (a hundred) at a
// time, and the repository a repository's list is about.
function listQuery(list, params, limit = "100") {
  const query = new URLSearchParams({ limit });
  const state = stateOf(params);
  if (state !== "open") (state === "closed" ? closedStates : Object.keys(issueStates)).forEach((s) => query.append("state", s));
  for (const name of list.names) if (params.get(name)) query.set(name, params.get(name));
  if (list.repository) query.set("repository", list.repository.id);
  return "/orgs/" + list.org.slug + "/issues?" + query;
}

// issueFilters are list's Repository, Assignee and Label buttons, each opening a Menu of its choices, the one the
// query names checked, and saying it once one is: Label by the label's name alone. Assignee's offers Me and Nobody,
// then the members, then the agents; Label's ends with the way to the labels. A repository's list has no Repository.
function issueFilters(list, { members, agents, repositories, labels }) {
  const pick = (name, label, options, extra, says = (o) => label + ": " + o.text) => {
    const current = paramsOf(list).get(name) || "";
    const chosen = current && options.find((o) => o.value === current);
    const button = h("button", { class: "btn btn-sm", type: "button", "aria-haspopup": "menu" }, icon("chevron-down"), chosen ? says(chosen) : label);
    button.addEventListener("click", () => popover(button, menuAlign(button, 260), (close) => [
      options.map((o) => (o.divider ? h("hr", { class: "menu-divider" })
        : item(o.word, o.value === current, () => { close(); go(listPath(list.where, paramsOf(list), { [name]: o.value }), true); }, o.lead))),
      extra || null], "issues-menu"));
    return button;
  };
  const option = (value, text, word = text, lead = null) => ({ value, text, word, lead });
  const divider = { divider: true };
  const people = [option("", "Anyone"), option(me.id, "Me", "Me", avatar(me.displayName)), option("none", "Nobody"),
    ...(members.length ? [divider] : []), ...members.map((m) => option(m.userId, m.displayName, m.displayName, avatar(m.displayName))),
    ...(agents.length ? [divider] : []), ...agents.map((a) => option(a.id, a.name, a.name, avatar(a.name, true)))];
  return [
    list.repository ? null : pick("repository", "Repository", [option("", "Any repository", "Any repository", icon("folder")), option("none", "No repository", "No repository", icon("folder")),
      ...repositories.map((r) => option(r.id, r.slug))]),
    pick("assignee", "Assignee", people),
    pick("label", "Label", [option("", "Any label"), ...labels.map((l) => option(l.id, l.name, labelTag(l)))],
      [h("hr", { class: "menu-divider" }), h("a", { class: "menu-item", href: base + list.org.slug + "/issues/labels", "data-nav": "", role: "menuitem" }, icon("pencil"), h("span", {}, "Manage labels"))],
      (o) => o.text),
  ].filter(Boolean);
}

// noIssues is the card the list shows when it has none to show: none yet, none open or closed, or none the filters
// match, which says what they ask for and clears them; New issue on those that invite one, about the repository a
// repository's list is about.
function noIssues(list, can, params, counts, choices) {
  const state = stateOf(params);
  const newIssue = can("issues.write") ? h("a", { class: "btn btn-primary", href: base + list.org.slug + "/issues/new" + (list.repository ? "?" + new URLSearchParams({ repository: list.repository.id }) : ""),
    "data-nav": "" }, "New issue") : null;
  const [glyph, title, text, action] = list.names.some((n) => params.get(n))
    ? ["circle-dot", "No issue matches", "No " + (state === "all" ? "" : state + " ") + "issue matches" + asked(params, choices) + ". Clear the filters, or widen the search.",
      h("a", { class: "btn", href: base + listPath(list.where, params, { repository: "", assignee: "", label: "", q: "" }).slice(1), "data-nav": "" }, "Clear filters")]
    : state === "closed" ? ["circle-x", "No closed issues", "Closing an issue moves it here, with the branch that did it and what changed.", null]
      : counts.open + counts.closed ? ["circle-check", "No open issues", "Every issue is closed. New issue opens the next one; Closed shows what was done.", newIssue]
        : list.repository ? ["circle-dot", "No issues yet", "An issue says what should change in this repository and why. Assign one to an agent and it opens a branch here.", newIssue]
          : ["circle-dot", "No issues yet", "An issue says what should change and why. Assign one to an agent and it opens a branch for it.", newIssue];
  return h("div", { class: "empty" }, h("div", { class: "empty-icon" }, icon(glyph)), h("h2", { class: "empty-title" }, title),
    h("p", { class: "empty-description" }, text), action);
}

// asked says what the filters params name ask for, as No issue matches puts it: the text, the label, the repository
// and the assignee.
function asked(params, { members, agents, repositories, labels }) {
  const [q, repository, assignee, label] = ["q", "repository", "assignee", "label"].map((n) => params.get(n));
  const named = (list, id, key) => list.find((x) => (x.userId || x.id) === id)?.[key];
  const who = assignee === "none" ? "nobody" : assignee === me.id ? "you" : named(members, assignee, "displayName") || named(agents, assignee, "name");
  return [q ? " “" + q + "”" : "", label ? " with label " + (named(labels, label, "name") || "that label") : "",
    repository === "none" ? " in no repository" : repository ? " in " + (named(repositories, repository, "slug") || "that repository") : "",
    assignee ? " assigned to " + (who || "them") : ""].join("");
}

// nothing is what an empty cell shows: a dash, as Table/Cell Kind=Unassigned draws it.
const nothing = () => h("span", { class: "cell-unassigned" }, "–");

// issueRow is an issue's row in list's table: its state, key, title and labels, its repository unless the list is
// a repository's, its assignee, the live branch linked to it last, and when it last changed.
const issueRow = (list, i, agentIds) => h("tr", {},
  h("td", {}, h("span", { class: "cell-issue" }, stateIcon(i.state), h("code", {}, i.identifier),
    h("a", { href: base + issuePath(list.org, i).slice(1), "data-nav": "" }, i.title), i.labels.map(labelTag))),
  list.repository ? null : i.repository ? h("td", { class: "cell-mono col-repository" }, i.repository.slug) : h("td", { class: "col-repository" }, nothing()),
  h("td", {}, i.assignee ? person(i.assignee, agentIds) : h("span", { class: "cell-unassigned" }, "Unassigned")),
  i.branch ? h("td", { class: "cell-mono col-branch" }, h("a", { href: branchHref(list.org, i.branch.repository.slug, i.branch.name), "data-nav": "" }, i.branch.name))
    : h("td", { class: "col-branch" }, nothing()),
  h("td", { class: "cell-muted col-updated", title: whenExactly(i.updatedAt) }, since(i.updatedAt)));
