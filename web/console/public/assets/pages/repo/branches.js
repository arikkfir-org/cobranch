import { annotationStatuses, commitAnnotations } from "../../lib/annotations.js";
import { api, apiPage } from "../../lib/api.js";
import { branchBadge, branchStatuses } from "../../lib/branches.js";
import { branchSubject } from "../../lib/chat.js";
import { h } from "../../lib/dom.js";
import { environmentsPanel } from "../../lib/environments.js";
import { plural, shortSha, subject as messageSubject, when, whenExactly, who } from "../../lib/format.js";
import { commitRow } from "../../lib/history.js";
import { icon } from "../../lib/icons.js";
import { issueBadge } from "../../lib/issues.js";
import { commitReviews, reviewDrafting } from "../../lib/reviews.js";
import { render } from "../../lib/router.js";
import { manualJobs, runBadge, runsPanel } from "../../lib/runs.js";
import { base, setCarried } from "../../lib/state.js";

export const nav = { label: "Branches", order: 10 };
export const view = (rest) => rest.length ? "repository.branch" : "repository.branches";
// A branch's page has a heading of its own, and the pane shows its conversation.
export const heading = (rest) => !rest.length;
export const crumbs = ({ root, rest }) => [{ label: "Branches", href: root + "/branches" }, ...(rest.length ? [{ label: rest.join("/") }] : [])];
export const subject = ({ org, repo, path, at, rest, can }) => rest.length ? branchSubject(org, repo, path, at, rest.join("/"), can) : null;
export default ({ org, path, at, rest, can, orgCan, fail }) => rest.length ? repositoryBranch(org, path, at, rest.join("/"), can, orgCan, fail) : repositoryBranches(path, at);

// repositoryBranches lists the repository's live branches, main first, each with its status as a request to merge
// into main, how far ahead of and behind main it is, and the commit it is at; then what merges need.
async function repositoryBranches(path, at) {
  const branches = (await api("GET", path + "/branches")).branches;
  const counts = (b) => b.ahead === undefined ? h("p", { class: "muted" }, "Not worked out yet") : h("p", {}, b.ahead + " ahead · " + b.behind + " behind");
  const row = (b) => h("tr", {}, h("td", {}, h("a", { href: at.branchPage(b.name), "data-nav": "" }, h("code", {}, b.name)), " ", b.status ? branchBadge(b.status) : h("span", { class: "chip" }, "default")),
    h("td", {}, b.commit ? [h("a", { class: "subject", href: at.commit(b.head), "data-nav": "" }, messageSubject(b.commit.message)), h("p", {}, who(b.commit.author), " · ", whenExactly(b.commit.author.at))]
      : h("p", {}, "At ", h("code", {}, shortSha(b.head)), ", no commit a push recorded.")),
    h("td", { class: "counts" }, b.status ? counts(b) : null),
    h("td", {}, when(b.updatedAt)));
  if (!branches.length) return [h("section", { class: "panel" }, h("h3", {}, "Branches"), h("p", {}, "No branches yet: nothing has been pushed."))];
  return [h("section", { class: "panel" }, h("h3", {}, "Branches"),
    h("table", { class: "commits branches" }, h("thead", {}, h("tr", {}, h("th", {}, "Branch"), h("th", {}, "Last commit"), h("th", {}, "Against main"), h("th", {}, "Updated"))),
      h("tbody", {}, branches.map(row)))), mergeRequirements(branches)];
}

// mergeRequirements names what a merge into main needs: always a branch up to date with it, and whatever main's
// /cobranch.yaml requires under branches; and the other branches the file names, with theirs.
function mergeRequirements(branches) {
  const main = branches.find((b) => b.name === "main");
  const others = branches.filter((b) => b.name !== "main" && b.requirements);
  const needs = (r) => [...r.annotations.map((n) => h("li", {}, "Annotation ", h("code", {}, n), " Successful on its head")),
    r.linearHistory ? h("li", {}, "No merge commit among its commits to merge") : null,
    r.reviews && r.reviews.approvals ? h("li", {}, plural(r.reviews.approvals, "approval"), " of its head, by people other than its author and its pusher") : null,
    r.reviews && r.reviews.ai ? h("li", {}, "The AI reviewer's approval of its head") : null,
    r.reviews ? h("li", {}, "No reviewer whose latest review of its head requests changes") : null];
  const named = (b) => {
    const r = b.requirements;
    const parts = [...r.annotations.map((n) => h("code", {}, n)), r.linearHistory ? "linear history" : null,
      r.reviews ? (r.reviews.approvals ? plural(r.reviews.approvals, "approval") : "reviews") : null, r.reviews && r.reviews.ai ? "the AI reviewer's approval" : null].filter(Boolean);
    return h("li", {}, h("code", {}, b.name), ": ", parts.length ? parts.flatMap((x, i) => i ? [", ", x] : [x]) : "named, with no requirements");
  };
  const file = () => h("code", {}, "/cobranch.yaml");
  return h("section", { class: "panel" }, h("h3", {}, "Merge requirements"),
    h("p", {}, "A branch merges into ", h("code", {}, "main"), " once it is up to date with it", main && main.requirements ? ", and needs:" : "."),
    main && main.requirements ? [h("ul", {}, needs(main.requirements)),
      h("p", {}, "A push into ", h("code", {}, "main"), " brings only Ready branches' commits; a principal holding ", h("code", {}, "repo.bypass"), " pushes past.")] : null,
    main && main.requirements ? null : h("p", {}, "Name ", h("code", {}, "main"), " under ", h("code", {}, "branches"), " in ", file(), ", pushed to ", h("code", {}, "main"), ", to require annotations, linear history, approvals or the AI reviewer's."),
    others.length ? [h("p", {}, file(), " also names:"), h("ul", {}, others.map(named))] : null);
}

// repositoryBranch is a branch's page: whose commits it would merge into main and how many, a Merge button and a
// Delete branch button for a viewer who may, why it is Blocked, requirement by requirement, the AI review of its
// head, the issue it works on, the jobs a member starts on it by hand, its commits, the reviews, annotations and
// runs of its head, and its environments. main's page names what merges into it need instead, and its
// environments, and a branch that isn't live any more, its deletion.
async function repositoryBranch(org, path, at, name, can, orgCan, fail) {
  let b;
  try {
    b = await api("GET", path + "/branches/" + encodeURIComponent(name));
  } catch (err) {
    if (err.status !== 404) throw err;
    return deletedBranchPage(path, at, name);
  }
  const title = h("div", { class: "row" }, h("div", { class: "title" }, icon("git-branch"), h("h2", {}, h("code", {}, b.name)), b.status ? branchBadge(b.status) : h("span", { class: "chip" }, "default")),
    h("div", { class: "row" }, b.base ? h("a", { class: "button small", href: at.changes(b.name), "data-nav": "" }, "Changes") : null,
      h("a", { class: "button small", href: at.history(b.name), "data-nav": "" }, "History"), deleteControl(path, b, can, fail)));
  if (!b.status) return [h("section", { class: "panel" }, title, h("p", {}, "The default branch: every other branch is a request to merge into it.")), settingsError(b), manualJobs(path, at, b, can, fail), mergeRequirements([b]),
    await environmentsPanel(path, at, b.name)].filter(Boolean);
  const sections = [h("section", { class: "panel" }, title, h("p", {}, wantsToMerge(b)), mergeControl(path, b, can, fail), worksOn(org, path, b, orgCan, fail))];
  if (b.settingsError) sections.push(settingsError(b));
  if (b.manualJobs && b.manualJobs.length && can("jobs.run")) sections.push(manualJobs(path, at, b, can, fail));
  // A Merged branch has nothing left to check or to merge.
  if (b.status !== "merged") sections.push(branchChecks(b), aiReviewPanel(org, path, b, can, fail));
  if (b.commit && b.status !== "merged") sections.push(await branchCommits(path, at, b));
  if (b.commit) sections.push(commitReviews(path, b.commit, can, fail, reviewDrafting(path, b, can)), commitAnnotations(path, b.commit, can, fail), await runsPanel(path, at, can, fail, b.head));
  sections.push(await environmentsPanel(path, at, b.name));
  return sections;
}

// settingsError says why the /cobranch.yaml at b's head doesn't parse, and that its jobs run nothing until it does;
// null when it parses.
function settingsError(b) {
  if (!b.settingsError) return null;
  return h("section", { class: "panel blocked" }, h("h3", {}, h("code", {}, "/cobranch.yaml"), " doesn't parse"),
    h("p", {}, "Its jobs run nothing at this head until it does:"), h("pre", {}, b.settingsError));
}

// branchChecks lists how b meets each merge requirement, as its status last worked them out, under why it is
// Blocked or Processing, as the design system's branch screen does.
function branchChecks(b) {
  const panel = h("section", { class: "panel" + (b.status === "blocked" ? " blocked" : "") }, h("h3", {}, "Merge requirements"));
  if (!b.checks.length) {
    panel.append(h("p", {}, "Cobranch works them out once it has processed the push that last moved the branch."));
    return panel;
  }
  const reason = whyNot(b);
  const s = branchStatuses[b.status];
  if (reason) panel.append(h("div", { class: "why tone-" + s.tone }, icon(s.icon), h("p", {}, h("strong", {}, s.word + ": "), reason)));
  const items = b.checks.map((c) => checkItem(b, c));
  if (!b.checks.some((c) => c.kind === "linear_history")) items.push(checkLine("circle", "neutral", "Linear history", "Not required"));
  if (!b.requirements || !b.requirements.reviews) items.push(checkLine("circle", "neutral", "Approvals", "Not required"));
  panel.append(h("ul", { class: "checks" }, items));
  return panel;
}
// checkLine is a line of a branch's merge requirements: an icon in its tone, what is required, and how it stands.
const checkLine = (iconName, tone, label, detail) => h("li", {}, h("span", { class: "tone-" + tone }, icon(iconName)), h("span", {}, label), h("span", { class: "detail" }, detail));
// checkItem is one of b's checks as a line: a required annotation as it stands on b's head, linear history, the
// approvals of b's head, the AI reviewer's, each reviewer requesting changes of it, and being up to date with b's
// base.
function checkItem(b, c) {
  const on = " on " + shortSha(b.head);
  const met = c.met ? ["circle-check", "success"] : ["circle-x", "danger"];
  if (c.kind === "annotation") {
    const label = ["Annotation ", h("code", {}, c.name)];
    if (!c.status) return checkLine("circle-x", "danger", label, "Not" + on + " yet");
    const s = annotationStatuses[c.status];
    return checkLine(s.icon, s.tone, label, s.word + on);
  }
  if (c.kind === "linear_history") return checkLine(...met, "Linear history", c.met ? "No merge commits" : c.count ? plural(c.count, "merge commit") : "Not worked out yet");
  if (c.kind === "approvals") return checkLine(...met, "Approvals", c.count + " of " + c.required + on);
  if (c.kind === "changes_requested") return checkLine("circle-alert", "warning", [c.name, " requested changes"], "On " + shortSha(b.head));
  if (c.kind === "ai") {
    const s = aiCheckStates[c.ai] || aiCheckStates.unreviewed;
    return checkLine(s.icon, s.tone, "The AI reviewer's approval", s.word + (s.head ? on : ""));
  }
  return checkLine(...met, ["Up to date with ", h("code", {}, b.base)], plural(c.count, "commit") + " behind");
}
// aiCheckStates are the states of the AI reviewer's check: a word for each, its icon and tone, and whether it is of
// the head; why one blocks a branch, said in full; and the three that keep it Processing.
const aiCheckStates = {
  waiting: { word: "Waits for a member to start its review", icon: "circle-pause", tone: "neutral", head: true, pending: true },
  queued: { word: "Review queued", icon: "clock", tone: "neutral", head: true, pending: true },
  running: { word: "Reviewing", icon: "loader-circle", tone: "info", head: true, pending: true },
  approved: { word: "Approved", icon: "circle-check", tone: "success", head: true },
  changes_requested: { word: "Changes requested", icon: "circle-alert", tone: "warning", head: true, why: "The AI reviewer requested changes on its head." },
  not_approved: { word: "Not approved", icon: "circle-x", tone: "danger", head: true, why: "The AI reviewer reviewed its head without approving it." },
  unreviewed: { word: "Not reviewed", icon: "circle-x", tone: "danger", head: true, why: "The AI reviewer hasn't reviewed its head: ask for a review." },
  failed: { word: "Review failed", icon: "circle-x", tone: "danger", head: true, why: "The AI review failed: " },
  cancelled: { word: "Review cancelled", icon: "circle-x", tone: "danger", head: true, why: "The AI review was cancelled: " },
  no_model: { word: "No model", icon: "circle-x", tone: "danger", why: "The AI reviewer has no model: an organization admin picks one on its agent's page." },
  suspended: { word: "Suspended", icon: "circle-x", tone: "danger", why: "The AI reviewer is suspended." },
  off: { word: "Off for this branch", icon: "circle-x", tone: "danger", why: "main's /cobranch-review.yaml leaves this branch out of the AI reviewer's reviews." },
  missing: { word: "No reviewer", icon: "circle-x", tone: "danger", why: "The organization has no AI reviewer." },
};
// whyNot says, in a line, what holds b back when it is Blocked or Processing: the first requirement it fails, or
// what it waits for.
function whyNot(b) {
  if (b.status === "processing") {
    const running = b.checks.find((c) => c.kind === "annotation" && (c.status === "pending" || c.status === "in_progress"));
    const reviewing = b.checks.find((c) => c.kind === "ai" && aiCheckStates[c.ai] && aiCheckStates[c.ai].pending);
    if (running) return [h("code", {}, running.name), " is " + annotationStatuses[running.status].word.toLowerCase() + " on its head."];
    return reviewing ? "The AI reviewer's review of its head: " + aiCheckStates[reviewing.ai].word.toLowerCase() + "."
      : "Cobranch is processing the push that last moved it.";
  }
  const c = b.status === "blocked" && b.checks.find((c) => !c.met);
  if (!c) return null;
  if (c.kind === "annotation") return [h("code", {}, c.name), c.status ? " failed on its head." : " isn't on its head yet."];
  if (c.kind === "linear_history") return [h("code", {}, b.base), " requires linear history, and ", c.count ? plural(c.count, "merge commit") : "a merge commit", " is among its commits."];
  if (c.kind === "approvals") return [h("code", {}, b.base), " requires ", plural(c.required, "approval"), " of its head, and it has ", c.count ? c.count : "none", "."];
  if (c.kind === "changes_requested") return [c.name, " requested changes on its head."];
  if (c.kind === "ai") {
    const s = aiCheckStates[c.ai] || aiCheckStates.unreviewed;
    return s.why + (c.reason || "");
  }
  return ["It is ", plural(c.count, "commit"), " behind ", h("code", {}, b.base), ": bring it up to date."];
}

// branchCommits lists the commits b would merge: the first of its first-parent history, as many as it is ahead of
// its base, which are exactly those while none of them is a merge; at most a page of them.
async function branchCommits(path, at, b) {
  const panel = h("section", { class: "panel" }, h("div", { class: "row" }, h("h3", {}, "Commits"),
    b.ahead === undefined ? null : h("span", { class: "muted" }, b.ahead + " ahead of " + b.base + " · " + b.behind + " behind")));
  if (!b.ahead) {
    panel.append(h("p", {}, b.ahead === 0 ? "None to merge." : "Cobranch counts them once it has processed the push that last moved the branch."));
    return panel;
  }
  const limit = Math.min(b.ahead, 100);
  const page = await apiPage(path + "/commits?" + new URLSearchParams({ branch: b.name, limit }));
  panel.append(h("table", { class: "commits" }, h("tbody", {}, page.data.commits.map(commitRow(at)))));
  if (b.ahead > limit) panel.append(h("div", {}, h("a", { class: "button small", href: at.history(b.name), "data-nav": "" }, "All its history")));
  return panel;
}

// deletedBranchPage is the page of a branch of name that isn't live: who deleted it and when, the head it was at,
// and its merge, as its latest deletion recorded them, with its chat as it was in the pane; or that there is none, for a name
// no branch had.
async function deletedBranchPage(path, at, name) {
  let d;
  try {
    d = await api("GET", path + "/deleted-branches/" + encodeURIComponent(name));
  } catch (err) {
    if (err.status !== 404) throw err;
    return [h("section", { class: "panel" }, h("h3", {}, "Branch"), h("p", {}, "There is no branch ", h("code", {}, name), "."))];
  }
  const by = d.deletedBy ? d.deletedBy.displayName : "a principal since gone, or before Cobranch recorded who";
  const head = h("a", { href: at.commit(d.head), "data-nav": "" }, h("code", {}, shortSha(d.head)));
  const merged = d.merge ? ["Merged into ", h("code", {}, "main"), " by ", d.merge.by ? d.merge.by.displayName : "a principal since gone", " · ", whenExactly(d.merge.at),
    ": its commits are in ", h("a", { href: at.history("main"), "data-nav": "" }, "main's history"), "."]
    : ["Never merged into ", h("code", {}, "main"), ": no branch holds its commits any more, though its head's page still shows them."];
  return [h("section", { class: "panel" }, h("div", { class: "row" }, h("div", { class: "title" }, icon("trash-2"), h("h2", {}, h("code", {}, d.name)), h("span", { class: "chip" }, "Deleted"))),
    h("p", {}, "Deleted by ", by, " · ", whenExactly(d.deletedAt)),
    h("p", {}, "It was at ", head, d.commit ? [" · ", messageSubject(d.commit.message)] : null, "."),
    h("p", {}, merged))];
}

// mayDelete reports whether the viewer may delete b: with repo.push, and repo.bypass too for a branch main's
// /cobranch.yaml names. main is never deleted.
const mayDelete = (b, can) => b.name !== "main" && can("repo.push") && (!b.requirements || can("repo.bypass"));

// deleteControl offers to delete b, at the head the page shows, for a viewer who may: a plain confirmation for a
// Merged or Empty branch, and a danger button whose confirmation names the commits main lacks for any other.
function deleteControl(path, b, can, fail) {
  if (!mayDelete(b, can)) return null;
  const done = b.status === "merged" || b.status === "empty";
  const button = h("button", { class: "button small" + (done ? "" : " danger"), type: "button" }, icon("trash-2"), "Delete branch");
  button.addEventListener("click", () => {
    const lacks = b.ahead ? plural(b.ahead, "commit") + " main lacks" : "commits main may lack";
    const question = done ? "Delete branch " + b.name + "? Its commits stay in main's history."
      : "Delete branch " + b.name + "? It has " + lacks + ", and no branch will hold them any more.";
    if (!confirm(question)) return;
    button.disabled = true;
    deleteBranch(path, b).then(render).catch((err) => { button.disabled = false; fail(err); });
  });
  return button;
}

// deleteBranch deletes b at the head the page showed, which refuses one that moved since.
const deleteBranch = (path, b) => api("DELETE", path + "/branches/" + encodeURIComponent(b.name), undefined, { "If-Match": '"' + b.head + '"' });

// mergeControl offers to merge b into its base, at the head the page shows, for a viewer who may: Merge once it is
// Ready, with repo.merge, and Merge anyway otherwise, with repo.bypass too. A merge commit or a fast-forward, where
// the base requires linear history a fast-forward alone, and none for a branch behind its base. A viewer who may
// delete b also has it deleted once merged, unless they uncheck it; a branch that moved meanwhile is kept, and the
// merge stands.
function mergeControl(path, b, can, fail) {
  const ready = b.status === "ready";
  if (b.ahead === 0 || !can("repo.merge") || (!ready && !can("repo.bypass"))) return null;
  // Until the push worker counts a branch's commits, Cobranch picks the method main allows.
  const known = b.ahead != null;
  const linear = b.checks.some((c) => c.kind === "linear_history");
  const methods = !known ? [] : [["merge", "Merge commit"], ["fast_forward", "Fast-forward"]].filter(([m]) => (m === "merge" ? !linear : !b.behind));
  if (known && !methods.length) return null;
  const method = methods.length > 1 ? h("select", { "aria-label": "How to merge" }, methods.map(([m, label]) => h("option", { value: m }, label))) : null;
  const how = methods.length > 1 ? null : h("span", { class: "muted" }, !known ? "Cobranch picks how, as " + b.base + " allows."
    : linear ? "By fast-forward: " + b.base + " requires linear history."
    : "By a merge commit: it is " + plural(b.behind, "commit") + " behind " + b.base + ".");
  const button = h("button", { class: "button " + (ready ? "primary" : "danger"), type: "submit" }, ready ? "Merge" : "Merge anyway");
  const remove = mayDelete(b, can) ? h("input", { name: "deleteBranch", type: "checkbox", checked: true }) : null;
  const merge = (e) => {
    e.preventDefault();
    if (!ready && !confirm("Merge " + b.name + " into " + b.base + " though it is " + branchStatuses[b.status].word + "?")) return;
    button.disabled = true;
    api("POST", path + "/branches/" + encodeURIComponent(b.name) + "/merge", { head: b.head, method: method ? method.value : methods.length ? methods[0][0] : undefined })
      .then(() => remove && remove.checked ? deleteBranch(path, b).catch((err) => { setCarried(new Error("Merged, but the branch was kept: " + err.message)); }) : null)
      .then(render).catch((err) => { button.disabled = false; fail(err); });
  };
  return h("form", { class: "row merge", onsubmit: merge }, method, button, remove ? h("label", { class: "check" }, remove, "Delete the branch once merged") : null, how);
}

// worksOn is the issue b works on, linked to its page with its state and how b was linked, for a viewer with
// issues.read, and for one with issues.write a field to link it to an issue by identifier, and Unlink. Nothing for
// a viewer without issues.read.
function worksOn(org, path, b, orgCan, fail) {
  if (!orgCan("issues.read")) return null;
  const at = path + "/branches/" + encodeURIComponent(b.name) + "/issue";
  const i = b.issue;
  const line = h("div", { class: "works-on" }, icon("circle-dot"), i
    ? ["Works on ", h("a", { href: base + org.slug + "/issues/" + i.identifier, "data-nav": "" }, h("code", {}, i.identifier), " ", i.title), issueBadge(i.state),
      h("span", { class: "muted" }, i.linkedBy === "name" ? "linked by its name" : "linked by hand")]
    : h("span", {}, "Works on no issue. Name it after one, such as ", h("code", {}, org.issueKey.toLowerCase() + "-42-" + "what-it-does"), ", or link one."));
  if (!orgCan("issues.write")) return line;
  const identifier = h("input", { name: "issue", required: true, placeholder: org.issueKey + "-42", "aria-label": "Issue", pattern: "\\s*(?:[A-Za-z][A-Za-z0-9]*-)?\\d+\\s*" });
  const link = (e) => {
    e.preventDefault();
    const number = Number(/(\d+)\s*$/.exec(identifier.value)[1]);
    api("PUT", at, { number }).then(render).catch(fail);
  };
  const unlink = () => api("DELETE", at).then(render).catch(fail);
  line.append(h("form", { onsubmit: link }, identifier, h("button", { class: "button small", type: "submit" }, i ? "Link another" : "Link")));
  if (i) line.append(h("button", { class: "button small", type: "button", onclick: unlink }, "Unlink"));
  return line;
}

// wantsToMerge says what b would merge into its base: how many commits, after the author of its head.
function wantsToMerge(b) {
  const into = [" into ", h("code", {}, b.base)];
  const updated = [" · updated ", when(b.updatedAt)];
  if (b.ahead === undefined) return ["Cobranch is still working out what it would merge", ...into, "."];
  if (b.status === "merged") return ["Merged", ...into, " by ", b.merge && b.merge.by ? b.merge.by.displayName : "a principal since gone", " · ",
    b.merge ? whenExactly(b.merge.at) : when(b.updatedAt)];
  if (!b.ahead) return ["Nothing to merge: every commit of it is in ", h("code", {}, b.base), " already", ...updated];
  const commits = b.ahead === 1 ? "1 commit" : b.ahead + " commits";
  return [b.commit ? [who(b.commit.author), " wants to merge "] : "It would merge ", commits, ...into, ...updated];
}

// aiReviewPanel says how the AI reviewer stands on a branch's head: its latest task there, with why it waits,
// failed or was cancelled, and, for a viewer holding repo.push, a button that asks for another review, which
// supersedes the open one and starts even for a head pushed from outside the organization.
function aiReviewPanel(org, path, b, can, fail) {
  const r = b.aiReview;
  const panel = h("section", { class: "panel" }, h("h3", {}, "AI review"));
  if (!r) {
    panel.append(h("p", {}, "The AI reviewer has no task for ", h("code", {}, shortSha(b.head)), ". It reviews a branch's pushes once an admin gives it a model, as ",
      h("code", {}, "main"), "'s ", h("code", {}, "/cobranch-review.yaml"), " says; its review shows among the head's."));
  } else {
    panel.append(h("p", { class: "row" }, runBadge(r.state), h("a", { href: base + org.slug + "/tasks/" + r.task, "data-nav": "" }, "Task #" + r.task),
      r.reason ? h("span", { class: "muted" }, r.reason) : null));
  }
  if (can("repo.push")) {
    const button = h("button", { class: "button small", type: "button" }, r && r.state === "waiting" ? "Start review" : r ? "Review again" : "Ask for a review");
    button.onclick = () => {
      button.disabled = true;
      api("POST", path + "/branches/" + encodeURIComponent(b.name) + "/review").then(render).catch((err) => { button.disabled = false; fail(err); });
    };
    panel.append(h("div", { class: "row" }, button));
  }
  return panel;
}
