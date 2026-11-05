import { annotationIcons } from "./annotations.js";
import { copyIcon } from "./code.js";
import { h } from "./dom.js";
import { fromNow, shortSha, subject, whenExactly, who } from "./format.js";
import { authorAvatar } from "./icons.js";

// commitRow makes a history row of a commit: its subject, author and time, and its annotations' icons by its name.
export const commitRow = (at) => (c) => h("tr", {}, h("td", {}, h("a", { class: "subject", href: at.commit(c.sha), "data-nav": "" }, subject(c.message)),
  h("p", {}, who(c.author), " · ", whenExactly(c.author.at))),
  h("td", { class: "sha" }, annotationIcons(c.annotations), h("a", { href: at.commit(c.sha), "data-nav": "" }, h("code", {}, shortSha(c.sha)))));

// commitListRow makes a Commit row of a commit, in a card's list: its author's avatar, its subject over who wrote it
// and when, its annotations' icons, its short SHA and a button that copies the whole.
export const commitListRow = (at) => (c) => h("li", { class: "commit-row" }, authorAvatar(c.author, 24),
  h("div", { class: "row-text" }, h("a", { class: "row-title", href: at.commit(c.sha), "data-nav": "" }, subject(c.message)),
    h("span", { class: "row-meta", title: whenExactly(c.author.at) }, (c.author.principal ? c.author.principal.displayName : c.author.name) + " committed " + fromNow(c.author.at))),
  annotationIcons(c.annotations), h("a", { class: "row-mono", href: at.commit(c.sha), "data-nav": "" }, shortSha(c.sha)), copyIcon(c.sha, "Copy the commit's SHA"));
