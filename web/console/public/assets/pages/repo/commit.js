import { commitAnnotations } from "../../lib/annotations.js";
import { api } from "../../lib/api.js";
import { diffPanel } from "../../lib/diff.js";
import { h } from "../../lib/dom.js";
import { shortSha, whenExactly, who } from "../../lib/format.js";
import { commitDrafting, commitReviews } from "../../lib/reviews.js";
import { runsPanel } from "../../lib/runs.js";

export const under = "tree";
export const accepts = (rest) => rest.length === 1;
export const heading = false;
export const crumbs = ({ rest }) => [{ label: "Commit" }, { label: shortSha(rest[0]) }];
export default ({ path, at, rest, can, fail }) => repositoryCommit(path, at, rest[0], can, fail);

// repositoryCommit is a commit's page: its message, its author and committer with the users behind them, its parents,
// the push that first brought it, with its pusher, and the annotations principals set on it, each deletable by a
// viewer who may: its principal with annotations.write, anyone with annotations.delete_any. It links its files and
// the history that leads to it, and lists the runs at it.
async function repositoryCommit(path, at, sha, can, fail) {
  let c;
  try {
    c = await api("GET", path + "/commits/" + encodeURIComponent(sha));
  } catch (err) {
    if (err.status !== 404 && err.status !== 400) throw err;
    return [h("section", { class: "panel" }, h("h3", {}, "Commit"), h("p", {}, "This repository holds no commit ", h("code", {}, sha), "."))];
  }
  const [first, ...rest] = c.message.split("\n");
  const body = rest.join("\n").trim();
  const person = (p) => [who(p), " ", h("span", { class: "muted" }, p.email), h("p", {}, whenExactly(p.at) + ", written in " + p.zone)];
  const parents = c.parents.length ? c.parents.map((p, i) => [i ? ", " : null, h("a", { href: at.commit(p), "data-nav": "" }, h("code", {}, shortSha(p)))]) : "None: it starts its history.";
  const pusher = c.push.pusher ? c.push.pusher.displayName : "a user since gone";
  const facts = h("dl", { class: "facts" },
    h("dt", {}, "Commit"), h("dd", {}, h("code", {}, c.sha)),
    h("dt", {}, c.parents.length > 1 ? "Parents" : "Parent"), h("dd", {}, parents),
    h("dt", {}, "Author"), h("dd", {}, person(c.author)),
    h("dt", {}, "Committer"), h("dd", {}, person(c.committer)),
    h("dt", {}, "Pushed"), h("dd", {}, "By " + pusher + " over " + c.push.transport.toUpperCase() + ", " + whenExactly(c.push.at)),
    c.signature ? [h("dt", {}, "Signature"), h("dd", {}, "Signed. Cobranch doesn't check signatures yet.")] : null);
  const links = h("div", { class: "row" }, h("a", { class: "button small", href: at.tree(c.sha, ""), "data-nav": "" }, "Browse files"),
    h("a", { class: "button small", href: at.history(c.sha), "data-nav": "" }, "History"));
  return [h("section", { class: "panel" }, h("div", { class: "row" }, h("h2", {}, first || "(no message)"), links), body ? h("pre", { class: "message" }, body) : null, facts),
    commitReviews(path, c, can, fail, commitDrafting(path, c, can)), commitAnnotations(path, c, can, fail), await runsPanel(path, at, can, fail, c.sha),
    await diffPanel(path + "/commits/" + c.sha + "/diff", at, "Changes")];
}
