import { annotationIcons } from "./annotations.js";
import { copyIcon } from "./code.js";
import { h } from "./dom.js";
import { fromNow, plural, shortSha, subject, whenExactly } from "./format.js";
import { authorAvatar } from "./icons.js";

// commitListRow makes a Commit row of a commit, in a card's list: its author's avatar, its subject over who wrote it
// and when it was committed, or merged with how many parents, its annotations' icons, its short SHA and a button that
// copies the whole.
export const commitListRow = (at) => (c) => h("li", { class: "commit-row" }, authorAvatar(c.author, 24),
  h("div", { class: "row-text" }, h("a", { class: "row-title", href: at.commit(c.sha), "data-nav": "" }, subject(c.message)),
    h("span", { class: "row-meta", title: whenExactly(c.committer.at) }, (c.author.principal ? c.author.principal.displayName : c.author.name) +
      (c.parents.length > 1 ? " merged " + fromNow(c.committer.at) + " · " + plural(c.parents.length, "parent") : " committed " + fromNow(c.committer.at)))),
  annotationIcons(c.annotations), h("a", { class: "row-mono", href: at.commit(c.sha), "data-nav": "" }, shortSha(c.sha)), copyIcon(c.sha, "Copy the commit's SHA"));
