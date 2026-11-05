import { api } from "../../lib/api.js";
import { branchBadge } from "../../lib/branches.js";
import { branchSubject } from "../../lib/chat.js";
import { diffPanel } from "../../lib/diff.js";
import { h } from "../../lib/dom.js";
import { icon } from "../../lib/icons.js";
import { commitReviews, reviewDrafting } from "../../lib/reviews.js";

export const under = "branches";
export const accepts = (rest) => rest.length > 0;
export const heading = false;
export const crumbs = ({ root, at, rest }) => [{ label: "Branches", href: root + "/branches" }, { label: rest.join("/"), href: at.branchPage(rest.join("/")) }, { label: "Changes" }];
export const subject = ({ org, repo, path, at, rest, can }) => branchSubject(org, repo, path, at, rest.join("/"), can);
export default ({ path, at, rest, can, fail }) => repositoryChanges(path, at, rest.join("/"), can, fail);

// repositoryChanges is a branch's Changes tab: what it would merge into its base, from where it left it, file by
// file, and the reviews of its head beneath, with the form to submit one, so a reviewer reads the change and gives
// their verdict on one page.
async function repositoryChanges(path, at, name, can, fail) {
  let b;
  try {
    b = await api("GET", path + "/branches/" + encodeURIComponent(name));
  } catch (err) {
    if (err.status !== 404) throw err;
    return [h("section", { class: "panel" }, h("h3", {}, "Changes"), h("p", {}, "There is no branch ", h("code", {}, name), " any more."))];
  }
  const title = h("div", { class: "row" }, h("div", { class: "title" }, icon("git-branch"), h("h2", {}, h("code", {}, b.name)), b.status ? branchBadge(b.status) : h("span", { class: "chip" }, "default")),
    h("a", { class: "button small", href: at.branchPage(b.name), "data-nav": "" }, "Branch"));
  if (!b.base) return [h("section", { class: "panel" }, title, h("p", {}, "The default branch merges into nothing, so it has no changes to review."))];
  // A reviewer who may post to the chat drafts comments on the lines, which go with their review of the head.
  const drafting = reviewDrafting(path, b, can);
  const sections = [h("section", { class: "panel" }, title, h("p", {}, "What it would merge into ", h("code", {}, b.base), ", from where it left it: commits ",
      h("code", {}, b.base), " gained since don't show.", drafting ? " Comment on a line with the + beside it: your comments go with your review of the head." : "")),
    await diffPanel(path + "/branches/" + encodeURIComponent(b.name) + "/diff", at, "Changes", drafting)];
  if (b.commit) sections.push(commitReviews(path, b.commit, can, fail, drafting));
  return sections;
}
