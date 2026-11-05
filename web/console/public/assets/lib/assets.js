// The Console's pages and styles, found by their files' names rather than by a list in its code: an organization's page
// is pages/org/<its route's part>.js and a repository's pages/repo/<its route's part>.js, index.js the root of each,
// and every stylesheet beside the modules styles the Console. The Console's server names these directories' files in
// its page, so a new page, or a module's styles, is a new file, and two changes that each add one touch no file in
// common.

import { h } from "./dom.js";

// assets is where the Console's modules came from: assets/ at its root, under their release (index.html), which every
// file this loads shares.
const assets = new URL("../", import.meta.url);

// orgPages and repoPages are the pages' modules by the part of the route that opens them: "" for index.js.
export const orgPages = new Map();
export const repoPages = new Map();

// stylesheet links the stylesheet at a path under assets, and resolves once it loaded.
const stylesheet = (path) => new Promise((resolve, reject) => {
  document.head.append(h("link", { rel: "stylesheet", href: new URL(path, assets).href, onload: resolve, onerror: () => reject(new Error("The Console couldn't load " + path)) }));
});

// load links the Console's stylesheets, fonts.css, tokens.css and console.css first and then each directory's in
// turn, so the order of their rules is the same on every load, and imports every page, so a session runs one
// release's modules throughout. named holds the files of each directory, by its path under assets, as the Console's
// page names them.
export async function load(named) {
  const dirs = ["lib/", "pages/", "pages/org/", "pages/repo/"];
  const files = dirs.map((dir) => [...named[dir]].sort());
  const sheets = ["fonts.css", "tokens.css", "console.css", ...dirs.flatMap((dir, i) => files[i].filter((f) => f.endsWith(".css")).map((f) => dir + f))];
  const pages = (dir, i, into) => Promise.all(files[i].filter((f) => f.endsWith(".js")).map(async (f) => [f === "index.js" ? "" : f.slice(0, -3), await import(new URL(dir + f, assets).href)]))
    .then((loaded) => loaded.forEach(([part, module]) => into.set(part, module)));
  await Promise.all([...sheets.map(stylesheet), pages("pages/org/", 2, orgPages), pages("pages/repo/", 3, repoPages)]);
}
