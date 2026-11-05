// A page whose views are tabs, as an organization's Settings and a repository's are: each tab a module of the page's
// own directory, pages/<org|repo>/<page>/<tab>.js, its index.js the first tab, at the page's own path, and the others at
// <page>/<tab>. The page's module (pages/org/settings.js) picks the tab its route names and draws it under the tab
// bar; no list names the tabs, so a new tab is a new file. A tab's module exports tab, { label, order }, which places
// it in the bar; allowed(can), when it has one, which says whether the viewer may open it; count(ctx), when it has
// one, the promise of a number its tab shows; view, the name the API records its page views under; and a default that
// draws it.

import { tabPages } from "./assets.js";
import { h } from "./dom.js";

// tabsOf is the tabs of the page whose directory is dir ("org/settings", "repo/settings") that the viewer, who holds
// what can says, may open, in their order, each as [part, module].
export const tabsOf = (dir, can) => [...(tabPages.get(dir) || [])]
  .filter(([, m]) => m.tab && (!m.allowed || m.allowed(can)))
  .sort(([, a], [, b]) => a.tab.order - b.tab.order);

// tabBar is the bar of tabs (Tab), each a link to its tab at href(part), the tab at part current, labelled by label
// for assistive technology; a tab with a count shows it once its count(ctx) answers, and nothing when it fails.
export function tabBar(tabs, part, href, ctx, label) {
  return h("nav", { class: "tab-bar", "aria-label": label }, tabs.map(([p, m]) => {
    const count = m.count ? h("span", { class: "tab-count", hidden: true }) : null;
    if (count) Promise.resolve().then(() => m.count(ctx)).then((n) => {
      if (n === null || n === undefined) return;
      count.textContent = String(n);
      count.hidden = false;
    }).catch(() => { /* the tab shows no count */ });
    return h("a", { class: "tab", href: href(p), "data-nav": "", "aria-current": p === part ? "page" : null }, m.tab.label, count);
  }));
}
