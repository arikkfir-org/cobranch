import { annotationIcons } from "./annotations.js";
import { h } from "./dom.js";
import { shortSha, subject, whenExactly, who } from "./format.js";

// commitRow makes a history row of a commit: its subject, author and time, and its annotations' icons by its name.
export const commitRow = (at) => (c) => h("tr", {}, h("td", {}, h("a", { class: "subject", href: at.commit(c.sha), "data-nav": "" }, subject(c.message)),
  h("p", {}, who(c.author), " · ", whenExactly(c.author.at))),
  h("td", { class: "sha" }, annotationIcons(c.annotations), h("a", { href: at.commit(c.sha), "data-nav": "" }, h("code", {}, shortSha(c.sha)))));
