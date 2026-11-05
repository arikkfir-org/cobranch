import { annotationStatuses } from "../../lib/annotations.js";
import { api, apiPage } from "../../lib/api.js";
import { diffView } from "../../lib/diff.js";
import { h } from "../../lib/dom.js";
import { fromNow, plural, shortSha, whenExactly } from "../../lib/format.js";
import { authorAvatar, icon } from "../../lib/icons.js";
import { popover } from "../../lib/menu.js";
import { commitReviews } from "../../lib/reviews.js";
import { render } from "../../lib/router.js";
import { me } from "../../lib/state.js";

export const under = "";
export const accepts = (rest) => rest.length === 1;
export const crumbs = ({ rest }) => [{ label: shortSha(rest[0]) }];
export default ({ path, at, rest, can, fail }) => repositoryCommit(path, at, rest[0], can, fail);

// repositoryCommit is a commit's page, as the Commit screen draws it: its subject beside Browse files and its SHA,
// its message's body, who wrote, committed and pushed it, onto which branches, and its parents, the annotations
// principals set on it and its jobs, then its changes, and the reviews of it, which a reviewer submits on its branch.
async function repositoryCommit(path, at, sha, can, fail) {
  let c;
  try {
    c = await api("GET", path + "/commits/" + encodeURIComponent(sha));
  } catch (err) {
    if (err.status !== 404 && err.status !== 400) throw err;
    return [h("section", { class: "card empty-centred" }, h("div", { class: "empty" }, h("span", { class: "empty-icon" }, icon("git-commit-horizontal")),
      h("h2", { class: "empty-title" }, "No commit " + shortSha(sha)),
      h("p", { class: "empty-description" }, "This repository holds no commit " + shortSha(sha) + ". It may live on a branch that was deleted, or in another repository.")))];
  }
  const [first, ...rest] = c.message.split("\n");
  const body = rest.filter((line) => !coAuthor.test(line)).join("\n").trim();
  const header = h("header", { class: "commit-header" },
    h("div", { class: "commit-title-line" }, h("h1", { class: "commit-title" }, first || "(no message)"),
      h("a", { class: "btn", href: at.tree(c.sha, ""), "data-nav": "" }, icon("folder"), "Browse files"), shaButton(c.sha)),
    body ? h("p", { class: "commit-body" }, body) : null, commitMeta(at, c), commitJobs(path, at, c, can, fail));
  const branch = (c.push.branches || [])[0]; // a REST server of the release before answers none, mid-upgrade
  const reviews = c.reviews.length ? commitReviews(path, c, (p) => p !== "reviews.submit" && can(p), fail, null) : null;
  if (reviews && branch) reviews.querySelector(".card-header").append(h("a", { class: "card-meta", href: at.changes(branch), "data-nav": "" }, "Review on " + branch));
  return [header, ...await diffView(path + "/commits/" + c.sha + "/diff", at), reviews].filter(Boolean);
}

// coAuthor is a Co-authored-by trailer: the meta line names its people, so the body leaves it out.
const coAuthor = /^co-authored-by:\s*(.*?)\s*(<[^>]*>)?\s*$/i;

// shaButton is the button that shows a commit's short SHA and copies the whole, showing a check for two seconds once
// it has.
function shaButton(sha) {
  const glyph = h("span", { class: "commit-sha-icon" }, icon("copy"));
  const button = h("button", { class: "btn", type: "button", title: "Copy " + sha, "aria-label": "Copy the commit's SHA, " + sha }, glyph, shortSha(sha));
  button.onclick = () => navigator.clipboard.writeText(sha).then(() => {
    glyph.replaceChildren(icon("check"));
    setTimeout(() => glyph.replaceChildren(icon("copy")), 2000);
  }, () => {});
  return button;
}

// commitMeta is the line under a commit's body: who wrote it and when it was committed, the branches its push moved,
// its parents, who committed it when someone else did, who pushed it and how, whether it is signed, and the people
// its message names as co-authors.
function commitMeta(at, c) {
  const name = (p) => (p.principal ? p.principal.displayName : p.name);
  const samePerson = (a, b) => (a.principal && b.principal ? a.principal.id === b.principal.id : a.name === b.name && a.email === b.email);
  const branches = (c.push.branches || []).map((b, i) => [i ? ", " : null, h("a", { href: at.branchPage(b), "data-nav": "" }, b)]);
  const parents = c.parents.map((p, i) => [i ? ", " : null, h("a", { href: at.commit(p), "data-nav": "" }, shortSha(p))]);
  const pusher = c.push.pusher ? c.push.pusher.displayName : "a user since gone";
  const how = c.push.transport === "api" ? " through the API" : " over " + c.push.transport.toUpperCase();
  const coAuthors = c.message.split("\n").map((line) => line.match(coAuthor)).filter((m) => m && m[1]).map((m) => m[1]);
  const clause = (...parts) => h("span", { class: "commit-meta-part" }, ...parts);
  return h("p", { class: "commit-meta" },
    clause(authorAvatar(c.author, 20), h("strong", { title: c.author.email }, name(c.author)),
      h("span", { title: "Written " + whenExactly(c.author.at) + ", committed " + whenExactly(c.committer.at) }, "committed " + fromNow(c.committer.at) + (branches.length ? " on" : "")),
      branches.length ? [icon("git-branch"), h("span", {}, branches)] : null),
    parents.length ? clause(h("span", {}, c.parents.length > 1 ? "· parents" : "· parent"), h("span", { class: "commit-meta-shas" }, parents)) : null,
    samePerson(c.author, c.committer) ? null : clause(h("span", { title: c.committer.email }, "· committed by " + name(c.committer))),
    clause(h("span", { title: "Pushed " + whenExactly(c.push.at) }, "· pushed by " + pusher + how,
      c.signature ? h("span", { title: "Cobranch doesn't check signatures yet" }, ", signed") : null)),
    coAuthors.length ? clause(h("span", {}, "· Co-authored-by " + coAuthors.join(", "))) : null);
}

// annotationWords are what a commit's Jobs line says of each annotation's status, after its name.
const annotationWords = { pending: "pending", in_progress: "running", failed: "failed", successful: "passed" };

// commitJobs is the line of a commit's annotations, each a badge whose menu opens its details and deletes it for a
// viewer who may (its principal with annotations.write, anyone with annotations.delete_any), then the jobs that ran
// on it, once counted; with neither, it hides.
function commitJobs(path, at, c, can, fail) {
  const deletable = (a) => can("annotations.delete_any") || (can("annotations.write") && a.principal && a.principal.id === me.id);
  // It deletes the version it shows: one changed meanwhile is refused, saying so.
  const remove = (a) => api("DELETE", path + "/commits/" + c.sha + "/annotations/" + encodeURIComponent(a.name), undefined, { "If-Match": `"${a.version}"` })
    .then(render).catch(fail);
  const badge = (a) => {
    const s = annotationStatuses[a.status];
    const label = a.name + " " + annotationWords[a.status];
    const title = (a.summary ? a.summary + " · " : "") + (a.principal ? "Set by " + a.principal.displayName : "Set by a principal since gone");
    if (!a.link && !deletable(a)) return h("span", { class: "badge-quiet tone-" + s.tone, title }, label);
    const button = h("button", { class: "badge-quiet commit-annotation tone-" + s.tone, type: "button", title, "aria-haspopup": "menu" }, label);
    button.onclick = () => popover(button, "start", (close) => [
      a.link ? h("a", { class: "menu-item", href: a.link, target: "_blank", rel: "noopener noreferrer", role: "menuitem", onclick: close }, icon("link"), h("span", {}, "Open " + a.name)) : null,
      deletable(a) ? h("button", { class: "menu-item menu-item-danger", type: "button", role: "menuitem",
        onclick: () => { close(); if (confirm("Delete annotation " + a.name + " of " + shortSha(c.sha) + "?")) remove(a); } }, icon("trash-2"), h("span", {}, "Delete annotation")) : null])
      ?.classList.add("commit-annotation-menu");
    return button;
  };
  const jobs = h("a", { class: "commit-jobs-link", href: at.runs(c.sha), "data-nav": "", hidden: true });
  const line = h("div", { class: "commit-jobs", hidden: !c.annotations.length }, c.annotations.map(badge), jobs);
  // A count that fails to arrive leaves the line without one.
  apiPage(path + "/runs?" + new URLSearchParams({ sha: c.sha })).then((page) => {
    const n = page.data.runs.length;
    if (!n) return;
    jobs.textContent = (page.next ? n + "+ jobs" : plural(n, "job")) + " on this commit";
    jobs.hidden = false;
    line.hidden = false;
  }, () => {});
  return line;
}
