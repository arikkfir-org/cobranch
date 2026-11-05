import { annotationStatuses, commitAnnotations } from "../../lib/annotations.js";
import { api, apiPage } from "../../lib/api.js";
import { aiCheckStates, branchBadge, branchStatuses, checkShort, processingWhat } from "../../lib/branches.js";
import { branchSubject } from "../../lib/chat.js";
import { ask, dialog } from "../../lib/dialog.js";
import { diffView } from "../../lib/diff.js";
import { h } from "../../lib/dom.js";
import { banner, checkbox, field, input } from "../../lib/forms.js";
import { environmentsOf } from "../../lib/environments.js";
import { fromNow, plural, shortSha, subject as messageSubject, whenExactly } from "../../lib/format.js";
import { commitListRow } from "../../lib/history.js";
import { authorAvatar, icon } from "../../lib/icons.js";
import { issueBadge } from "../../lib/issues.js";
import { popover } from "../../lib/menu.js";
import { commitReviews, reviewDrafting } from "../../lib/reviews.js";
import { go, render } from "../../lib/router.js";
import { branchJobs } from "../../lib/runs.js";
import { base, me, setCarried } from "../../lib/state.js";
import { mergeCard, repositoryBranches } from "./index.js";

// branches/ shows a branch's page, and the repository's branches without a name: their address before they became
// the repository's first tab (index.js), which they light.
export const under = "";
export const view = (rest) => rest.length ? branchView() : "repository.branches";
// A branch's page has a heading of its own, and the pane shows its conversation.
export const heading = (rest) => !rest.length;
export const crumbs = ({ root, rest }) => rest.length ? [{ label: "Branches", href: root }, { label: rest.join("/") }] : [{ label: "Branches" }];
export const subject = ({ org, repo, path, at, rest, can }) => rest.length ? branchSubject(org, repo, path, at, rest.join("/"), can) : null;
export default (ctx) => ctx.rest.length ? repositoryBranch(ctx, ctx.rest.join("/")) : repositoryBranches(ctx);

// branchTabs are a branch's tabs, by the name its page's tab= names: what it is and would merge, then its changes,
// the runs of its head and its environments; each counts as the page view it names. The default branch merges
// nothing, so it has no changes.
const branchTabs = {
  "": { label: "Overview", view: "repository.branch" },
  changes: { label: "Files changed", view: "repository.changes" },
  jobs: { label: "Jobs", view: "repository.runs" },
  environment: { label: "Environment", view: "repository.environments" },
};
// tabShown is the branch's tab the page's address names, its overview when it names none it has.
const tabShown = () => {
  const tab = new URLSearchParams(location.search).get("tab") || "";
  return Object.hasOwn(branchTabs, tab) ? tab : "";
};
const branchView = () => branchTabs[tabShown()].view;

// repositoryBranch is a branch's page: its heading, with Merge and its menu for a viewer who may; how its merge
// stands, requirement by requirement; and its tabs. A branch that isn't live any more shows its deletion.
async function repositoryBranch(ctx, name) {
  let b;
  try {
    b = await api("GET", ctx.path + "/branches/" + encodeURIComponent(name));
  } catch (err) {
    if (err.status !== 404) throw err;
    return deletedBranchPage(ctx, name);
  }
  const tab = b.base || tabShown() !== "changes" ? tabShown() : "";
  // The base's requirements, which b's checks stand against: b's own are those of merges into it.
  const base = b.base && checking.includes(b.status) ? await api("GET", ctx.path + "/branches/" + encodeURIComponent(b.base)).catch(() => null) : null;
  // b's environments, which its header opens when exactly one of them has an address, and its Environment tab draws.
  const envs = api("GET", ctx.path + "/environments?" + new URLSearchParams({ branch: b.name })).then((d) => d.environments);
  return [branchHeader(ctx, b, envs), settingsError(b), b.status ? mergeStatus(ctx, b, base && base.requirements, tab) : null, branchTabBar(ctx, b, tab),
    ...await tabContent(ctx, b, tab, envs)].filter(Boolean);
}

// tabContent is what b's tab shows: its overview, its changes with its chat's threads on their lines and the reviews of
// its head, the runs of its head and the jobs it starts by hand, or its environments.
async function tabContent(ctx, b, tab, envs) {
  const { org, repo, path, at, can, fail } = ctx;
  if (tab === "changes") {
    const drafting = reviewDrafting(path, b, can);
    const branch = path + "/branches/" + encodeURIComponent(b.name);
    // Only a signed-in viewer keeps Viewed marks.
    return [...await diffView(branch + "/diff", at, drafting, branchSubject(org, repo, path, at, b.name, can), me ? branch + "/viewed" : null),
      b.commit ? commitReviews(path, b.commit, can, fail, drafting) : null];
  }
  if (tab === "jobs") return branchJobs(path, at, b, can, fail);
  if (tab === "environment") return environmentsOf(ctx, b, await envs);
  if (!b.status) return [await recentCommits(ctx, b), mergeCard(ctx, b), b.commit ? commitAnnotations(path, b.commit, can, fail) : null];
  return [b.status === "merged" ? null : await branchCommits(ctx, b), b.commit ? commitAnnotations(path, b.commit, can, fail) : null,
    b.commit ? commitReviews(path, b.commit, can, fail, reviewDrafting(path, b, can)) : null];
}

// branchTabBar is b's tabs (Tab), each a link to its page, Files changed with the files its diff changes and Jobs with
// the runs of its head, once counted.
function branchTabBar({ path, at }, b, shown) {
  const tabs = Object.entries(branchTabs).filter(([t]) => b.base || t !== "changes");
  return h("nav", { class: "tab-bar", "aria-label": b.name }, tabs.map(([t, { label }]) => {
    const tab = h("a", { class: "tab", href: at.branchPage(b.name, t), "data-nav": "", "aria-current": t === shown ? "page" : null }, label);
    const count = (n) => n && tab.append(h("span", { class: "tab-count" }, n));
    // A count that fails to arrive leaves the tab without one.
    if (t === "changes") api("GET", path + "/branches/" + encodeURIComponent(b.name) + "/diff-counts").then((c) => count(c.files), () => {});
    if (t === "jobs") api("GET", path + "/runs?" + new URLSearchParams({ sha: b.head, limit: "1" })).then((page) => count(page.total), () => {});
    return tab;
  }));
}

// branchHeader is b's heading: its name and status, who wants to merge what into its base, when it was created and the
// issue it works on; and Environment, which opens the address of its one environment that has one, then Merge and the
// branch's menu, for a viewer who may use them.
function branchHeader(ctx, b, envs) {
  const title = h("div", { class: "branch-title" }, icon("git-branch"), h("h1", {}, b.name),
    b.status ? branchBadge(b.status) : h("span", { class: "tag" }, "Default branch"));
  const merge = mayMerge(ctx, b) ? h("button", { class: "btn btn-primary", type: "button", disabled: b.status !== "ready",
    title: b.status === "ready" ? null : "It merges once it is Ready", onclick: () => mergeDialog(ctx, b) }, icon("git-merge"), "Merge") : null;
  const more = h("button", { class: "icon-btn", type: "button", "aria-haspopup": "menu", "aria-label": "More", title: "More" }, icon("ellipsis"));
  more.onclick = () => branchMenu(more, ctx, b);
  const actions = h("div", { class: "page-header-actions" }, merge, more);
  // Environments that fail to arrive leave the header without the button.
  envs.then((list) => {
    const open = list.filter((e) => e.url);
    if (open.length === 1) actions.prepend(h("a", { class: "btn", href: open[0].url, target: "_blank", rel: "noopener", title: "Open " + open[0].url }, "Environment", icon("external-link")));
  }, () => {});
  return h("header", { class: "page-header branch-header" }, h("div", { class: "page-header-text" }, title, h("p", { class: "branch-meta" }, branchMeta(ctx, b))), actions);
}

// branchMeta is the line under b's name: whose head it is and what it would merge, or what it is, when it was created,
// and the issue it works on, for a viewer who may read issues.
function branchMeta({ org, at, orgCan }, b) {
  const author = b.commit && b.commit.author;
  const who = author ? [authorAvatar(author), h("strong", {}, author.principal ? author.principal.displayName : author.name)] : [];
  const into = [" into ", h("code", {}, b.base)];
  const head = h("a", { href: at.commit(b.head), "data-nav": "" }, h("code", {}, shortSha(b.head)));
  let what;
  if (!b.status) what = [...who, author ? " pushed " : "At ", head, " · updated ", fromNow(b.updatedAt)];
  else if (b.status === "merged") what = ["Merged", ...into, b.merge && b.merge.by ? " by " + b.merge.by.displayName : "", " ", fromNow(b.merge ? b.merge.at : b.updatedAt)];
  else if (b.ahead === undefined) what = [...who, " pushed ", head, "; Cobranch is working out what it would merge", ...into];
  else if (!b.ahead) what = ["Nothing to merge: every commit of it is in ", h("code", {}, b.base), " already"];
  else what = [...who, " wants to merge ", plural(b.ahead, "commit"), ...into];
  const issue = b.issue && orgCan("issues.read") ? [" · linked to ", h("a", { href: base + org.slug + "/issues/" + b.issue.identifier, "data-nav": "",
    title: b.issue.title }, b.issue.identifier)] : null;
  return [...what, b.status ? [" · created ", h("span", { title: whenExactly(b.createdAt) }, fromNow(b.createdAt))] : null, issue];
}

// settingsError says why the /cobranch.yaml at b's head doesn't parse, and that its jobs run nothing until it does;
// null when it parses.
function settingsError(b) {
  if (!b.settingsError) return null;
  return h("div", { class: "banner banner-danger", role: "alert" }, icon("octagon-alert"), h("div", { class: "banner-text" },
    h("strong", { class: "banner-title" }, "/cobranch.yaml doesn't parse at " + shortSha(b.head)), h("p", {}, "Its jobs run nothing at this head until it does:"),
    h("pre", { class: "settings-error" }, b.settingsError)));
}

// The tone of each branch status's merge card: Merged and Empty have nothing left to hold them back.
const statusTones = { blocked: "danger", processing: "info", ready: "success", empty: "neutral", merged: "neutral" };

// checking are the statuses whose merge requirements a branch's page lists, as its checks stand against them.
const checking = ["blocked", "processing", "ready"];

// mergeStatus is how b's merge stands: why it is Blocked or Processing, or that it is Ready, Empty or Merged, over
// each of its merge requirements as its status last worked them out, as the Branch screen draws them. required is
// its base's requirements, when they could be read; tab is the tab shown, whose sections its action may link.
function mergeStatus(ctx, b, required, tab) {
  const s = branchStatuses[b.status];
  const [title, message, action] = statusReason(ctx, b, tab);
  const rows = checking.includes(b.status) ? checkRows(ctx, b, required) : [];
  return h("section", { class: "card merge-status tone-" + statusTones[b.status] },
    h("div", { class: "merge-status-reason" }, icon(s.icon), h("div", { class: "banner-text" }, h("strong", { class: "banner-title" }, title),
      message ? h("p", {}, message) : null), action),
    rows.length ? h("ul", { class: "list" }, rows) : null);
}

// statusReason is the title of b's merge card, the sentence beneath it, and the link to what it names, if any: the
// reviews on the Overview and Files changed tabs, the annotations on the Overview, which alone show them.
function statusReason({ at }, b, tab) {
  const s = branchStatuses[b.status];
  const go = (href, label) => h("a", { class: "btn btn-ghost btn-sm", href, "data-nav": href.startsWith("#") ? null : "" }, label);
  if (b.status === "merged") return ["Merged into " + b.base, ["By ", b.merge && b.merge.by ? b.merge.by.displayName : "a principal since gone", " · ",
    b.merge ? whenExactly(b.merge.at) : whenExactly(b.updatedAt), ". Delete the branch to tear down its environments."], go(at.history(b.base), "History")];
  if (b.status === "empty") return ["Nothing to merge", ["Every commit of it is in ", h("code", {}, b.base), " already."], null];
  if (b.status === "ready") return ["Ready to merge", ["It meets every requirement of ", h("code", {}, b.base), " at ", shortSha(b.head), "."], null];
  const c = b.status === "blocked" ? b.checks.find((c) => !c.met) : null;
  if (b.status === "processing" || !c) return [s.word + ": " + processingWhat(b), [whyNot(b), " Every push checks the branch again."], null];
  const action = ["changes_requested", "ai", "approvals"].includes(c.kind) && (tab === "" || tab === "changes") ? go("#reviews", "View reviews")
    : c.kind === "annotation" && tab === "" ? go("#annotations", "View annotations") : null;
  return [s.word + ": " + checkShort(b, c), [whyNot(b), " Every push checks the branch again."], action];
}

// checkRows are b's merge requirements as check rows: its required annotations together, then its reviews, the AI
// reviewer's approval, being up to date with its base and linear history, each with how it stands on b's head.
// Where its base, by required, requires reviews and nobody requests changes, a row says so.
function checkRows({ org }, b, required) {
  const on = " on " + shortSha(b.head);
  const of = (kind) => b.checks.filter((c) => c.kind === kind);
  const rows = [];
  const annotations = of("annotation");
  if (annotations.length) {
    const failing = annotations.find((c) => c.status === "failed") || annotations.find((c) => !c.status) || annotations.find((c) => !c.met);
    const s = failing && failing.status ? annotationStatuses[failing.status] : null;
    rows.push(failing ? checkRow(s ? s.icon : "circle-x", s ? s.tone : "danger", "Required annotations pass", failing.name + " " + (s ? s.word.toLowerCase() : "missing") + on)
      : checkRow("circle-check", "success", "Required annotations pass", plural(annotations.length, "annotation") + " successful" + on));
  }
  const approvals = of("approvals")[0];
  if (approvals) rows.push(checkRow(...met(approvals), "At least " + plural(approvals.required, "approval"), approvals.count + " of " + approvals.required + on));
  const requested = of("changes_requested");
  if (requested.length) rows.push(checkRow("circle-alert", "warning", "No open change requests", requested.map((c) => c.name).join(", ") + " requested changes"));
  else if (required && required.reviews) rows.push(checkRow("circle-check", "success", "No open change requests", "None" + on));
  for (const c of of("ai")) {
    const s = aiCheckStates[c.ai] || aiCheckStates.unreviewed;
    const task = b.aiReview ? h("a", { href: base + org.slug + "/tasks/" + b.aiReview.task, "data-nav": "" }, "task #" + b.aiReview.task) : null;
    rows.push(checkRow(s.icon, s.tone, "The AI reviewer approves", [s.word + (s.head ? on : ""), c.reason ? ": " + c.reason : "", task ? [" · ", task] : null]));
  }
  for (const c of of("up_to_date")) rows.push(checkRow(...met(c), ["Up to date with ", h("code", {}, b.base)], plural(c.count, "commit") + " behind"));
  const linear = of("linear_history")[0];
  rows.push(linear ? checkRow(...met(linear), "Linear history", linear.met ? "No merge commits" : linear.count ? plural(linear.count, "merge commit") : "Not worked out yet")
    : checkRow("circle", "neutral", "Linear history", "Not required"));
  return rows;
}
const met = (c) => c.met ? ["circle-check", "success"] : ["circle-x", "danger"];
// checkRow is one requirement of a branch's merge (Check row, until the Patterns page draws it): an icon in its tone,
// what is required, and how it stands.
const checkRow = (iconName, tone, label, detail) => h("li", { class: "check-row" }, h("span", { class: "tone-" + tone }, icon(iconName)),
  h("span", { class: "check-row-label" }, label), h("span", { class: "check-row-detail" }, detail));

// whyNot says, in a sentence, what holds b back when it is Blocked or Processing: the first requirement it fails, or
// what it waits for.
function whyNot(b) {
  if (b.status === "processing") {
    const running = b.checks.find((c) => c.kind === "annotation" && (c.status === "pending" || c.status === "in_progress"));
    const reviewing = b.checks.find((c) => c.kind === "ai" && aiCheckStates[c.ai] && aiCheckStates[c.ai].pending);
    if (running) return running.name + " is " + annotationStatuses[running.status].word.toLowerCase() + " on " + shortSha(b.head) + ".";
    return reviewing ? "The AI reviewer's review of " + shortSha(b.head) + ": " + aiCheckStates[reviewing.ai].word.toLowerCase() + "."
      : "Cobranch is processing the push that last moved it.";
  }
  const c = b.checks.find((c) => !c.met);
  if (!c) return "Cobranch works its requirements out once it has processed the push that last moved it.";
  if (c.kind === "annotation") return c.name + (c.status === "failed" ? " failed on " : " isn't Successful on ") + shortSha(b.head) + ".";
  if (c.kind === "linear_history") return b.base + " requires linear history, and " + (c.count ? plural(c.count, "merge commit") + " are" : "a merge commit is") + " among its commits.";
  if (c.kind === "approvals") return b.base + " requires " + plural(c.required, "approval") + " of its head, and it has " + (c.count || "none") + ".";
  if (c.kind === "changes_requested") return c.name + " requested changes on " + shortSha(b.head) + ".";
  if (c.kind === "ai") {
    const s = aiCheckStates[c.ai] || aiCheckStates.unreviewed;
    return s.why + (c.reason || "");
  }
  return "It is " + plural(c.count, "commit") + " behind " + b.base + ": bring it up to date.";
}

// branchCommits is the card of the commits b would merge: the first of its first-parent history, as many as it is
// ahead of its base, which are exactly those while none of them is a merge; at most a page of them.
async function branchCommits({ path, at }, b) {
  const card = h("section", { class: "card" }, h("div", { class: "card-header" }, h("h2", { class: "card-title" }, "Commits"),
    b.ahead === undefined ? null : h("span", { class: "card-meta" }, b.ahead + " ahead of " + b.base + (b.behind ? " · " + b.behind + " behind" : ""))));
  if (!b.ahead) {
    card.append(h("div", { class: "card-body" }, h("p", { class: "muted" }, b.ahead === 0 ? "None to merge."
      : "Cobranch counts them once it has processed the push that last moved the branch.")));
    return card;
  }
  const limit = Math.min(b.ahead, 100);
  const page = await apiPage(path + "/commits?" + new URLSearchParams({ branch: b.name, limit }));
  card.append(h("ul", { class: "list" }, page.data.commits.map(commitListRow(at))));
  if (b.ahead > limit) card.append(h("div", { class: "card-body" }, h("a", { class: "btn btn-sm", href: at.history(b.name), "data-nav": "" }, "All its history")));
  return card;
}

// recentCommits is the card of the default branch's newest commits, with a link to all its history.
async function recentCommits({ path, at }, b) {
  const page = await apiPage(path + "/commits?" + new URLSearchParams({ branch: b.name, limit: 10 }));
  return h("section", { class: "card" }, h("div", { class: "card-header" }, h("h2", { class: "card-title" }, "Commits"),
    h("a", { class: "card-meta", href: at.history(b.name), "data-nav": "" }, "History")),
  h("ul", { class: "list" }, page.data.commits.map(commitListRow(at))));
}

// deletedBranchPage is the page of a branch of name that isn't live: who deleted it and when, the head it was at,
// and its merge, as its latest deletion recorded them, with its chat as it was in the pane; or that there is none, for
// a name no branch had.
async function deletedBranchPage({ path, at, root }, name) {
  let d;
  try {
    d = await api("GET", path + "/deleted-branches/" + encodeURIComponent(name));
  } catch (err) {
    if (err.status !== 404) throw err;
    return [h("div", { class: "empty" }, h("span", { class: "empty-icon" }, icon("git-branch")), h("h1", { class: "empty-title" }, "No branch " + name),
      h("p", { class: "empty-description" }, "No branch of this repository has had that name."), h("a", { class: "btn", href: root, "data-nav": "" }, "Branches"))];
  }
  const by = d.deletedBy ? d.deletedBy.displayName : "a principal since gone, or before Cobranch recorded who";
  const head = h("a", { href: at.commit(d.head), "data-nav": "" }, h("code", {}, shortSha(d.head)));
  const merged = d.merge ? ["Merged into ", h("code", {}, "main"), " by ", d.merge.by ? d.merge.by.displayName : "a principal since gone", " · ", whenExactly(d.merge.at),
    ": its commits are in ", h("a", { href: at.history("main"), "data-nav": "" }, "main's history"), "."]
    : ["Never merged into ", h("code", {}, "main"), ": no branch holds its commits any more, though its head's page still shows them."];
  return [h("header", { class: "page-header branch-header" }, h("div", { class: "page-header-text" },
    h("div", { class: "branch-title" }, icon("trash-2"), h("h1", {}, d.name), h("span", { class: "tag" }, "Deleted")),
    h("p", { class: "branch-meta" }, "Deleted by ", by, " · ", whenExactly(d.deletedAt)))),
  h("div", { class: "banner banner-" + (d.merge ? "success" : "warning") }, icon(d.merge ? "git-merge" : "triangle-alert"), h("div", { class: "banner-text" },
    h("p", {}, "It was at ", head, d.commit ? [" · ", messageSubject(d.commit.message)] : null, "."), h("p", {}, merged)))];
}

// mayDelete reports whether the viewer may delete b: with repo.push, and repo.bypass too for a branch main's
// /cobranch.yaml names. main is never deleted.
const mayDelete = (b, can) => b.name !== "main" && can("repo.push") && (!b.requirements || can("repo.bypass"));
// mayMerge reports whether the viewer may merge b, Ready, into its base: with repo.merge, while it has commits to merge.
const mayMerge = ({ can }, b) => b.base && b.status !== "merged" && can("repo.merge");

// branchMenu opens b's menu (Branch more-menu) under anchor: a link to an issue and a review by the AI reviewer, for a
// viewer who may ask for them; Merge anyway, for one who may bypass its requirements; its history; and Delete branch.
function branchMenu(anchor, ctx, b) {
  const { path, at, can, orgCan, fail } = ctx;
  const item = (iconName, label, act, danger) => h("button", { class: "menu-item" + (danger ? " menu-item-danger" : ""), type: "button", role: "menuitem", onclick: act },
    icon(iconName), h("span", {}, label));
  const live = b.base && b.status !== "merged";
  const r = b.aiReview;
  const anyway = live && b.status !== "ready" && b.ahead && can("repo.merge") && can("repo.bypass");
  popover(anchor, "end", (close) => {
    const then = (act) => () => { close(); act(); };
    return [b.base && orgCan("issues.write") ? item("circle-dot", b.issue ? "Link to another issue" : "Link to an issue", then(() => linkDialog(ctx, b))) : null,
      live && can("repo.push") ? item("sparkles", r && r.state === "waiting" ? "Start the AI review" : r ? "Ask for another AI review" : "Ask for an AI review",
        then(() => api("POST", path + "/branches/" + encodeURIComponent(b.name) + "/review").then(render).catch(fail))) : null,
      h("a", { class: "menu-item", href: at.history(b.name), "data-nav": "", role: "menuitem", onclick: close }, icon("history"), h("span", {}, "History")),
      anyway ? [h("hr", { class: "menu-divider" }), item("git-branch", "Merge anyway", then(() => mergeDialog(ctx, b, true)))] : null,
      mayDelete(b, can) ? [h("hr", { class: "menu-divider" }), item("x", "Delete branch", then(() => deleteDialog(ctx, b)), true)] : null];
  })?.classList.add("branch-menu");
}

// mergeDialog asks how to merge b into its base, at the head the page shows: a merge commit or a fast-forward, where
// the base requires linear history a fast-forward alone, and only a merge commit for a branch behind it. A viewer who
// may delete b also has it deleted once merged, unless they uncheck it; a branch that moved meanwhile is kept, and the
// merge stands. anyway merges a branch that isn't Ready, past its requirements.
function mergeDialog({ path, can }, b, anyway) {
  // Until the push worker counts a branch's commits, Cobranch picks the method its base allows.
  const known = b.ahead != null;
  const linear = b.checks.some((c) => c.kind === "linear_history");
  const methods = !known ? [] : [["merge", "Merge commit", "keeps its commits, and records the merge in a commit of its own"],
    ["fast_forward", "Fast-forward", "moves " + b.base + " to its head"]].filter(([m]) => (m === "merge" ? !linear : !b.behind));
  const radios = methods.map(([m, label, help], i) => h("input", { type: "radio", name: "method", value: m, checked: i === 0 }));
  const how = methods.length > 1 ? h("div", { class: "field", role: "radiogroup", "aria-label": "How to merge" }, h("span", { class: "field-label" }, "How"),
    methods.map(([, label, help], i) => h("label", { class: "radio" }, radios[i], h("span", {}, label, h("span", { class: "field-help" }, " · " + help)))))
    : h("p", {}, !known ? "Cobranch picks how, as " + b.base + " allows." : !methods.length ? "It can't be merged: " + b.base + " requires linear history, and it is behind it."
      : linear ? "By fast-forward: " + b.base + " requires linear history." : "By a merge commit: it is " + plural(b.behind, "commit") + " behind " + b.base + ".");
  const remove = mayDelete(b, can) ? h("input", { type: "checkbox", checked: true }) : null;
  const warning = anyway ? banner("danger", null, "It is " + branchStatuses[b.status].word + ": " + (b.status === "blocked" ? whyNot(b) : processingWhat(b) + ".") +
    " Merging it bypasses " + b.base + "'s requirements.") : null;
  const merge = async () => {
    if (known && !methods.length) throw new Error("It can't be merged: " + b.base + " requires linear history, and it is behind it.");
    const picked = radios.find((r) => r.checked);
    await api("POST", path + "/branches/" + encodeURIComponent(b.name) + "/merge", { head: b.head, method: picked ? picked.value : methods.length ? methods[0][0] : undefined });
    if (remove && remove.checked) await deleteBranch(path, b).catch((err) => { setCarried(new Error("Merged, but the branch was kept: " + err.message)); });
    render();
  };
  dialog("Merge " + b.name + " into " + b.base, [warning, how, remove ? checkbox(remove, "Delete the branch once merged") : null],
    anyway ? "Merge anyway" : "Merge", anyway ? "btn-danger" : "btn-primary", merge);
}

// deleteDialog asks before it deletes b at the head the page shows: plainly for a Merged or Empty branch, and naming
// the commits main lacks for any other.
function deleteDialog({ path, at }, b) {
  const done = b.status === "merged" || b.status === "empty";
  const lacks = b.ahead ? plural(b.ahead, "commit") + " main lacks" : "commits main may lack";
  ask("Delete " + b.name + "?", done ? "Its commits stay in main's history, and its environments are torn down."
    : "It has " + lacks + ", and no branch will hold them any more. Its environments are torn down.", "Delete branch",
  () => deleteBranch(path, b).then(() => go(at.branchPage(b.name).slice(base.length - 1))));
}

// deleteBranch deletes b at the head the page showed, which refuses one that moved since.
const deleteBranch = (path, b) => api("DELETE", path + "/branches/" + encodeURIComponent(b.name), undefined, { "If-Match": '"' + b.head + '"' });

// linkDialog links b to an issue of the organization, by its identifier or number, in place of the one it works on,
// or unlinks that one.
function linkDialog({ org, path, fail }, b) {
  const issue = input({ name: "issue", required: true, placeholder: org.issueKey + "-42", pattern: "\\s*(?:[A-Za-z][A-Za-z0-9]*-)?\\d+\\s*" });
  const unlink = (e) => {
    e.currentTarget.disabled = true;
    api("DELETE", path + "/branches/" + encodeURIComponent(b.name) + "/issue").then(() => { el.close(); render(); }, (err) => { el.close(); fail(err); });
  };
  const now = b.issue ? h("div", { class: "linked-issue" }, h("span", {}, "It works on ", h("code", {}, b.issue.identifier), " ", b.issue.title), issueBadge(b.issue.state),
    h("button", { class: "btn btn-ghost btn-sm", type: "button", onclick: unlink }, "Unlink")) : null;
  const link = () => {
    const number = issue.validity.valid && /(\d+)\s*$/.exec(issue.value);
    if (!number) return Promise.reject(new Error("Name an issue by its identifier or number, such as " + org.issueKey + "-42."));
    return api("PUT", path + "/branches/" + encodeURIComponent(b.name) + "/issue", { number: Number(number[1]) }).then(render);
  };
  const el = dialog("Link " + b.name + " to an issue", [now, field("Issue", issue, "A branch named after an issue, such as " + org.issueKey.toLowerCase() +
    "-42-what-it-does, links itself.")], "Link", "btn-primary", link);
}
