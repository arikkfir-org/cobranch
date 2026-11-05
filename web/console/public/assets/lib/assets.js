// The Console's pages and styles, found by their files' names rather than by a list in its code: an organization's page
// is pages/org/<its route's part>.js and a repository's pages/repo/<its route's part>.js, index.js the root of each,
// and every stylesheet beside the modules styles the Console. The Console's server names these directories' files in
// its page, so a new page, or a module's styles, is a new file, and two changes that each add one touch no file in
// common.

import { h } from "./dom.js";

// assets is where the Console's modules came from: assets/ at its root, under their release (index.html), which every
// file this loads shares.
const assets = new URL("../", import.meta.url);

// orgPages and repoPages are the pages' modules by the part of the route that opens them: "" for index.js; consolePages
// are those of pages/ that draw in the app shell, which a default export says, by the Console path they take; tabPages
// are the tabs of a page whose views are tabs, each a module of the page's own directory (pages/org/settings/), by the
// directory under pages/ ("org/settings") and then by the tab's part, "" for its index.js (lib/tabs.js).
export const orgPages = new Map();
export const repoPages = new Map();
export const consolePages = new Map();
export const tabPages = new Map();

// stylesheet links the stylesheet at a path under assets, and resolves once it loaded.
const stylesheet = (path) => new Promise((resolve, reject) => {
  document.head.append(h("link", { rel: "stylesheet", href: new URL(path, assets).href, onload: resolve, onerror: () => reject(new Error("The Console couldn't load " + path)) }));
});

// tabbed is a directory of a page's tabs, as the Console's page names it: pages/org/<page>/ or pages/repo/<page>/.
const tabbed = /^pages\/(?:org|repo)\/[a-z0-9-]+\/$/;

// load links the Console's stylesheets, fonts.css, tokens.css, console.css and components.css first and then each
// directory's in turn, so the order of their rules is the same on every load, and imports every page, so a session runs
// one release's modules throughout. named holds the files of each directory, by its path under assets, as the
// Console's page names them: the four below, and every directory of a page's tabs, which the page names without a
// list here.
export async function load(named) {
  const dirs = ["lib/", "pages/", "pages/org/", "pages/repo/", ...Object.keys(named).filter((dir) => tabbed.test(dir)).sort()];
  const files = dirs.map((dir) => [...(named[dir] || [])].sort());
  const sheets = ["fonts.css", "tokens.css", "console.css", "components.css", ...dirs.flatMap((dir, i) => files[i].filter((f) => f.endsWith(".css")).map((f) => dir + f))];
  const pages = (dir, i, into) => Promise.all(files[i].filter((f) => f.endsWith(".js")).map(async (f) => [f === "index.js" ? "" : f.slice(0, -3), await import(new URL(dir + f, assets).href)]))
    .then((loaded) => loaded.forEach(([part, module]) => into.set(part, module)));
  const shelled = new Map();
  const tabs = dirs.slice(4).map((dir, i) => {
    const into = new Map();
    tabPages.set(dir.slice("pages/".length, -1), into);
    return pages(dir, 4 + i, into);
  });
  await Promise.all([...sheets.map(stylesheet), pages("pages/", 1, shelled), pages("pages/org/", 2, orgPages), pages("pages/repo/", 3, repoPages), ...tabs]);
  shelled.forEach((module, part) => { if (module.default) consolePages.set(part, module); });
}
