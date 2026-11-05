import { api } from "./api.js";
import { linesAnchor } from "./chat.js";
import { h } from "./dom.js";
import { failure, field, input, textarea } from "./forms.js";
import { fromNow, plural, shortSha, size, subject, whenExactly } from "./format.js";
import { authorAvatar, icon, principalAvatar } from "./icons.js";
import { rendered } from "./markdown.js";
import { popover } from "./menu.js";
import { go } from "./router.js";
import { base, config } from "./state.js";

// repositoryFiles is the Files tab's directory or file, as the Files and File screens draw them: under the ref picker
// and the path, a directory's latest commit, its entries beside the commits that last changed them, and its README;
// or a file's card, with its lines, its rendered Markdown, its image, or why it isn't shown. Its URL's parts after
// tree/ or blob/ are a ref, then a path: the longest branch or tag name they start with, or else their first part, a
// commit's full name. A repository nothing was pushed to says so, with the command that clones it.
export async function repositoryFiles(org, repo, path, at, isFile, rest, can) {
  const { branches, tags } = await refs(path);
  if (!branches.length) return [noFiles(org, repo)];
  const [ref, file] = splitRef(rest, [...branches.map((b) => b.name), ...tags.map((t) => t.name)]);
  const toolbar = h("div", { class: "files-toolbar" }, refPicker(branches, tags, ref, (r) => (isFile ? at.blob(r, file) : at.tree(r, file))),
    filePath(repo, at, ref, file));
  const onBranch = branches.some((b) => b.name === ref) ? ref : null;
  const shown = isFile ? await repositoryFile(repo, path, at, ref, file, onBranch, can) : await repositoryTree(repo, path, at, ref, file);
  return [toolbar, ...shown];
}

// refs reads the repository's branches and tags.
const refs = (path) => Promise.all([api("GET", path + "/branches"), api("GET", path + "/tags")])
  .then(([b, t]) => ({ branches: b.branches, tags: t.tags }));

// filesBranch is the branch whose files the parts of a directory's or a file's URL show, or null for a tag's or a
// commit's: the conversation pane shows its chat (lib/pane.js).
export async function filesBranch(path, parts) {
  const { branches, tags } = await refs(path);
  const [ref] = splitRef(parts, [...branches.map((b) => b.name), ...tags.map((t) => t.name)]);
  return branches.some((b) => b.name === ref) ? ref : null;
}

// splitRef splits the parts of a directory's or a file's URL into its ref and its path: by the longest branch or
// tag name of names that its first parts make, or else its first part, main when there is none.
function splitRef(parts, names) {
  for (let n = parts.length; n > 0; n--) {
    const ref = parts.slice(0, n).join("/");
    if (names.includes(ref)) return [ref, parts.slice(n).join("/")];
  }
  return [parts[0] || "main", parts.slice(1).join("/")];
}

// refPicker is the Ref picker: a button that names the branch, tag or commit a page shows, and opens a menu of the
// repository's branches and then its tags, each a link to where link(name) says, the one shown checked.
export function refPicker(branches, tags, ref, link) {
  const isTag = !branches.some((b) => b.name === ref) && tags.some((t) => t.name === ref);
  const sha = !isTag && !branches.some((b) => b.name === ref) && /^[0-9a-f]{40,64}$/.test(ref);
  const button = h("button", { class: "btn ref-picker", type: "button", "aria-haspopup": "menu", "aria-expanded": "false", title: ref },
    icon(isTag ? "tag" : sha ? "git-commit-horizontal" : "git-branch"), h("span", { class: "ref-picker-name" }, sha ? shortSha(ref) : ref), icon("chevron-down"));
  button.addEventListener("click", () => popover(button, "start", (close) => {
    const item = (glyph, name) => h("a", { class: "menu-item", role: "menuitemradio", "aria-checked": String(name === ref), href: link(name), "data-nav": "",
      onclick: close }, icon(glyph), h("span", { class: "ref-menu-name" }, name));
    return [branches.map((b) => item("git-branch", b.name)), tags.length ? [h("hr", { class: "menu-divider" }), tags.map((t) => item("tag", t.name))] : null];
  })?.classList.add("ref-menu"));
  return button;
}

// filePath is the path of a directory or a file from the repository's root, which its slug names: a link to each
// directory on the way, then the last part itself.
function filePath(repo, at, ref, path) {
  const names = [repo.slug, ...(path ? path.split("/") : [])];
  return h("nav", { class: "file-path", "aria-label": "Path" }, names.map((name, i) => [i ? h("span", { class: "file-path-separator", "aria-hidden": "true" }, "/") : null,
    i === names.length - 1 ? h("span", { "aria-current": "page" }, name) : h("a", { href: at.tree(ref, names.slice(1, i + 1).join("/")), "data-nav": "" }, name)]));
}

// cloneMenu opens the Clone popover under anchor, the heading's Clone button: the repository's URLs over HTTPS and
// over SSH, each with a button that copies it, and how each signs in. An installation that names no Git server says
// it has nothing to clone yet.
export function cloneMenu(anchor, org, repo, push) {
  const path = "/" + org.slug + "/" + repo.slug + ".git";
  const settings = (text) => h("a", { href: base + "settings", "data-nav": "" }, text);
  const way = (label, url) => h("div", { class: "field" }, h("span", { class: "field-label" }, label),
    h("div", { class: "clone-url" }, h("code", { title: url }, url), copyButton(url)));
  const ways = [config.gitUrl ? way("HTTPS", config.gitUrl + path) : null, config.gitSshUrl ? way("SSH", config.gitSshUrl + path) : null].filter(Boolean);
  popover(anchor, "end", () => [h("div", { class: "clone-menu" }, h("h2", {}, "Clone " + repo.slug),
    ways.length ? ways : h("p", {}, "This installation's Git server has no address yet, so its repositories can't be cloned. Its operator gives it one."),
    ways.length ? h("p", { class: "field-help" }, config.gitUrl ? ["Over HTTPS, sign in with a ", settings("personal token"), " as the password. "] : null,
      config.gitSshUrl ? ["SSH uses the ", settings("keys on your account"), ". "] : null, push ? null : "You may read this repository, but not push to it.") : null)])
    ?.setAttribute("role", "dialog");
}

// noFiles is the Files screen of a repository nothing was pushed to: what pushing does, and a Copy field with the
// command that clones it over HTTPS, when the installation's Git server has an address.
function noFiles(org, repo) {
  const command = config.gitUrl ? "git clone " + config.gitUrl + "/" + org.slug + "/" + repo.slug + ".git" : null;
  return h("div", { class: "empty files-empty" }, h("span", { class: "empty-icon" }, icon("folder-git-2")), h("h2", { class: "empty-title" }, "No files yet"),
    h("p", { class: "empty-description" }, command ? "Push a branch and its files show here. The clone path is below; main becomes the default branch with the first push."
      : "Push a branch and its files show here; main becomes the default branch with the first push."),
    command ? h("div", { class: "field files-clone" }, h("span", { class: "field-label" }, "Clone"),
      h("div", { class: "clone-url" }, h("code", { title: command }, command), copyIcon(command, "Copy the clone command"))) : null);
}

// copyButton copies text, and says Copied for two seconds once it has.
export function copyButton(text, label = "Copy") {
  const button = h("button", { class: "btn btn-ghost btn-sm", type: "button", title: label }, "Copy");
  button.onclick = () => navigator.clipboard.writeText(text).then(() => {
    button.textContent = "Copied";
    setTimeout(() => { button.textContent = "Copy"; }, 2000);
  }, () => {});
  return button;
}

// copyIcon copies text from a ghost icon button, as a Commit row's, and shows a check for two seconds once it has.
export function copyIcon(text, label) {
  const button = h("button", { class: "icon-btn icon-btn-ghost icon-btn-sm", type: "button", "aria-label": label, title: label }, icon("copy"));
  button.onclick = () => navigator.clipboard.writeText(text).then(() => {
    button.replaceChildren(icon("check"));
    setTimeout(() => button.replaceChildren(icon("copy")), 2000);
  }, () => {});
  return button;
}

// empty is B's Empty state: an icon, a title, what it means, and an action, if any.
const empty = (glyph, title, description, action) => h("div", { class: "empty" }, h("span", { class: "empty-icon" }, icon(glyph)),
  h("h2", { class: "empty-title" }, title), h("p", { class: "empty-description" }, description), action);

// unreadable is the Empty state a page shows when the API couldn't read a directory or a file: what a ref or a path
// found nothing at, an installation without a Git server, or, for any other failure, the Unreadable screens' "Can't
// read", which tries again; repo names the repository its text shows the path in.
function unreadable(err, repo, ref, path, isFile) {
  const what = { "ref.not_found": ["git-branch", "No such branch, tag or commit", ["This repository has nothing named ", h("code", {}, ref), "."]],
    "path.not_found": ["file", "Nothing here", ["There is nothing at ", h("code", {}, path), " in ", h("code", {}, ref), "."]],
    "path.submodule": ["folder-git-2", "A submodule", [h("code", {}, path), " is a submodule, whose files are another repository's."]],
    "git.unavailable": ["server", "No Git server", "This installation runs no Git server, so its files can't be shown."] }[err.code];
  if (what) return empty(...what);
  const again = h("button", { class: "btn btn-primary", type: "button", onclick: () => go(location.pathname.slice(base.length - 1) + location.search + location.hash, true) },
    icon("refresh-cw"), "Try again");
  return isFile ? empty("circle-x", "Can't read this file", path.split("/").pop() + " couldn't be read from the repository just now. Try again, or clone the repository and open it there.", again)
    : empty("circle-x", "Can't read this directory", [repo.slug, path].filter(Boolean).join("/") + " couldn't be read from the repository just now. Try again, or clone the repository and read it there.", again);
}

// by is who made a commit, as a sentence names them: the user behind its author, or the name it gives.
const by = (person) => (person.principal ? person.principal.displayName : person.name);

// repositoryTree lists the directory at dir of ref: the latest commit that changed it, with how many did, which
// links to their history; then its entries, directories first, each beside the commit that last changed it; then
// its README. A file's path goes to its file's page; a directory that can't be read says why in the listing's card.
async function repositoryTree(repo, path, at, ref, dir) {
  const query = new URLSearchParams({ ref, path: dir });
  const reading = api("GET", path + "/tree/commits?" + query).catch(() => null);
  let tree;
  try {
    tree = await api("GET", path + "/tree?" + query);
  } catch (err) {
    if (err.code === "path.file") return go(at.blob(ref, dir).slice(base.length - 1), true), [];
    return [h("section", { class: "card" }, unreadable(err, repo, ref, dir, false))];
  }
  const last = await reading;
  // Entries come directories first, each group by name as people sort them, whatever their case.
  const group = (e) => (e.kind === "tree" || e.kind === "submodule" ? 0 : 1);
  const entries = tree.entries.slice().sort((a, b) => group(a) - group(b) || a.name.localeCompare(b.name, undefined, { sensitivity: "base" }));
  const commits = new Map((last ? last.commits : []).map((c) => [c.sha, c]));
  const changed = (name) => (last && last.entries[name] ? commits.get(last.entries[name]) : undefined);
  const up = dir ? h("li", { class: "file-entry" }, icon("folder"),
    h("a", { class: "file-entry-name file-entry-dir", href: at.tree(ref, dir.split("/").slice(0, -1).join("/")), "data-nav": "", "aria-label": "Up a directory" }, "..")) : null;
  return [latestCommit(at, ref, dir, tree.commit, last, commits),
    h("section", { class: "card" }, entries.length || up ? h("ul", { class: "file-entries" }, up, entries.map((e) => entryRow(at, ref, e, changed(e.name))))
      : h("div", { class: "card-body" }, h("p", { class: "muted" }, "This directory is empty."))),
    tree.readme ? readme(at, ref, tree.readme) : null].filter(Boolean);
}

// latestCommit is the Files screen's commit card: the latest commit that changed a directory, by whom and when, its
// short name, and how many commits changed the directory, which links to their history. Until a push records it, or
// when the walk couldn't say, it names the commit the directory is at.
function latestCommit(at, ref, dir, commit, last, commits) {
  const c = last && last.latest ? commits.get(last.latest) : undefined;
  const historyLink = h("a", { class: "last-commit-history", href: at.history(ref, false, dir), "data-nav": "" }, icon("clock"),
    last ? plural(last.count, "commit") : "History");
  const text = c ? h("div", { class: "last-commit-text" }, h("a", { class: "last-commit-title", href: at.commit(c.sha), "data-nav": "" }, subject(c.message)),
    h("span", { class: "last-commit-meta", title: whenExactly(c.author.at) }, by(c.author) + " committed " + fromNow(c.author.at)))
    : h("div", { class: "last-commit-text" }, h("span", { class: "last-commit-meta" }, "At commit ", h("a", { href: at.commit(commit), "data-nav": "" }, shortSha(commit))));
  return h("section", { class: "card last-commit" }, h("div", { class: "last-commit-row" }, c ? authorAvatar(c.author, 24) : null, text,
    c ? h("a", { class: "last-commit-sha", href: at.commit(c.sha), "data-nav": "" }, shortSha(c.sha)) : null, historyLink));
}

// entryRow is one of a directory's entries: a directory, which links to its listing, a file or a symlink, which
// links to its page, or a submodule, which pins a commit of another repository; beside it the commit that last
// changed it, and when.
function entryRow(at, ref, e, c) {
  const dir = e.kind === "tree";
  const glyph = icon(dir ? "folder" : e.kind === "submodule" ? "folder-git-2" : "file");
  if (dir) glyph.classList.add("file-entry-folder");
  const name = dir ? h("a", { class: "file-entry-name file-entry-dir", href: at.tree(ref, e.path), "data-nav": "" }, e.name + "/")
    : e.kind === "submodule" ? h("span", { class: "file-entry-name", title: "A submodule at " + e.sha }, e.name + " @ " + shortSha(e.sha))
      : h("a", { class: "file-entry-name", href: at.blob(ref, e.path), "data-nav": "" }, e.name);
  return h("li", { class: "file-entry" }, glyph, name,
    c ? h("a", { class: "file-entry-message", href: at.commit(c.sha), "data-nav": "", title: subject(c.message) }, subject(c.message)) : h("span", { class: "file-entry-message" }),
    h("span", { class: "file-entry-when", title: c ? whenExactly(c.author.at) : null }, c ? fromNow(c.author.at) : ""));
}

// readme is a directory's README beneath its listing, in a card titled by its name: rendered when it is Markdown,
// else its text.
function readme(at, ref, f) {
  return h("section", { class: "card readme" }, h("div", { class: "card-header" },
    h("h2", { class: "card-title" }, h("a", { href: at.blob(ref, f.path), "data-nav": "" }, f.name)), f.truncated ? h("span", { class: "card-meta" }, "Its first 1 MiB") : null),
  h("div", { class: "readme-body" }, f.html !== undefined ? rendered(f.html) : h("pre", { class: "readme-text" }, f.text)));
}

// imageFile reports whether a file's name is an image's, which its page shows from its bytes.
const imageFile = (name) => /\.(png|jpe?g|gif|webp|avif|bmp|ico|svg)$/i.test(name);

// languages names a file's language by its whole name, else by its extension, for the facts beside its name.
const languages = new Map(Object.entries({ go: "Go", js: "JavaScript", mjs: "JavaScript", cjs: "JavaScript", jsx: "JavaScript", ts: "TypeScript",
  tsx: "TypeScript", py: "Python", rb: "Ruby", rs: "Rust", java: "Java", kt: "Kotlin", swift: "Swift", c: "C", h: "C", cc: "C++", cpp: "C++",
  hpp: "C++", cs: "C#", php: "PHP", sh: "Shell", bash: "Shell", zsh: "Shell", sql: "SQL", html: "HTML", css: "CSS", scss: "SCSS", json: "JSON",
  yaml: "YAML", yml: "YAML", toml: "TOML", xml: "XML", md: "Markdown", markdown: "Markdown", proto: "Protocol Buffers", tf: "Terraform",
  lua: "Lua", graphql: "GraphQL", svg: "SVG", txt: "Text", dockerfile: "Dockerfile", makefile: "Makefile", "go.mod": "Go module", "go.sum": "Go checksums" }));
const language = (name) => {
  const lower = name.toLowerCase();
  return languages.get(lower) || (lower.includes(".") ? languages.get(lower.split(".").pop()) : undefined);
};

// repositoryFile shows the file at path of ref in a card, as the File screen draws it: its name and facts over its
// lines, which a Markdown file shows rendered first; its image; or, for a binary file or one past 1 MiB, why it isn't
// shown, with a link to its bytes. A symlink shows its target; a directory's path goes to its directory's page, and a
// file that can't be read says why in its card. On a branch, a text file's lines start threads of the branch's chat,
// and the chat's threads on the file are listed.
async function repositoryFile(repo, path, at, ref, file, branch, can) {
  const name = file.split("/").pop();
  const historyLink = h("a", { class: "btn btn-ghost btn-sm", href: at.history(ref, false, file), "data-nav": "" }, icon("clock"), "History");
  const facts = h("span", { class: "file-facts" });
  const card = (glyph, said, actions, ...body) => {
    facts.textContent = said.filter(Boolean).join(" · ");
    return h("section", { class: "card file" }, h("div", { class: "card-header file-header" }, icon(glyph), h("h2", { class: "file-name" }, name), facts, actions), body);
  };
  let f;
  try {
    f = await api("GET", path + "/blob?" + new URLSearchParams({ ref, path: file }));
  } catch (err) {
    if (err.code === "path.directory") return go(at.tree(ref, file).slice(base.length - 1), true), [];
    const raw = h("a", { class: "btn btn-ghost btn-sm", href: at.raw(ref, file), target: "_blank", rel: "noopener" }, "Raw");
    return [card("file-code", [language(name)], [raw, historyLink], unreadable(err, repo, ref, file, true))];
  }
  const download = (cls, label) => h("a", { class: cls, href: at.raw(f.commit, f.path, true) }, label);
  const type = f.name.includes(".") ? f.name.split(".").pop().toUpperCase() : null;
  if (f.kind === "symlink") {
    const target = f.path.split("/").slice(0, -1).concat(f.text.split("/")).reduce((p, n) => (n === ".." ? p.slice(0, -1) : n === "." || !n ? p : [...p, n]), []).join("/");
    return [card("link", ["symlink", plural(f.size, "byte")], [historyLink, copyIcon(f.text, "Copy the link's target")],
      h("div", { class: "file-symlink" }, icon("arrow-right"), h("a", { class: "file-symlink-target", href: at.blob(ref, target), "data-nav": "" }, f.text),
        h("span", { class: "file-symlink-hint" }, "Links to this file in the same commit")))];
  }
  if (imageFile(f.name)) {
    const img = h("img", { src: at.raw(f.commit, f.path), alt: f.name });
    img.addEventListener("load", () => {
      if (img.naturalWidth) facts.textContent = [img.naturalWidth + " × " + img.naturalHeight, size(f.size), type].join(" · ");
    });
    return [card("file-code", [size(f.size), type], [download("btn btn-sm", "Download"), historyLink], h("div", { class: "file-image" }, img))];
  }
  if (f.binary || f.truncated) {
    return [card("file-code", [size(f.size), f.binary ? "binary" : language(f.name)], [download("btn btn-sm", "Download"), historyLink],
      empty("file", "Not shown", "Files over 1 MiB, and binary files of any size, aren't shown. Download it, or clone the repository.",
        download("btn btn-primary", "Download " + size(f.size))))];
  }
  const lines = linePicker(path, at, branch, f, can);
  const count = f.text === "" ? 0 : f.text.replace(/\n$/, "").split("\n").length;
  const code = count ? fileLines(f.text, lines) : h("div", { class: "card-body" }, h("p", { class: "muted" }, "This file is empty."));
  const said = [plural(count, "line"), size(f.size), language(f.name), f.mode === "100755" ? "executable" : null];
  const actions = [h("a", { class: "btn btn-ghost btn-sm", href: at.raw(f.commit, f.path), target: "_blank", rel: "noopener" }, "Raw"), historyLink,
    copyIcon(f.text, "Copy the file's text")];
  const shown = f.html !== undefined ? markdownFile(f, code, lines) : { views: null, body: code };
  return [card("file-code", said, [shown.views, actions], shown.body), await lines.panel()].filter(Boolean);
}

// markdownFile is a Markdown file's views: rendered, or its lines, which it shows first when the page's anchor names
// lines, and the segmented control that picks between them.
function markdownFile(f, code, lines) {
  const preview = h("div", { class: "file-markdown" }, rendered(f.html));
  const atLines = lines.picked() !== null;
  (atLines ? preview : code).hidden = true;
  const view = (label, shown) => h("button", { type: "button", "aria-pressed": String(shown), onclick: (e) => {
    preview.hidden = label !== "Preview";
    code.hidden = label === "Preview";
    e.currentTarget.parentElement.querySelectorAll("button").forEach((b) => b.setAttribute("aria-pressed", String(b === e.currentTarget)));
  } }, label);
  return { views: h("div", { class: "segmented file-views", role: "group", "aria-label": "View" }, view("Preview", !atLines), view("Source", atLines)),
    body: [preview, code] };
}

// fileLines is a text's lines, each numbered: a click on a line's number picks that line and a shift-click the lines
// from the one picked before, which lines marks and takes.
function fileLines(text, lines) {
  const rows = text.replace(/\n$/, "").split("\n").map((line, i) => h("div", { class: "file-line" },
    h("span", { class: "file-line-number", "data-line": i + 1 }, String(i + 1)), h("span", { class: "file-line-text" }, line)));
  const all = h("div", { class: "file-lines-rows" }, rows);
  lines.attach(rows, all);
  return h("div", { class: "file-lines" }, all);
}

// linePicker marks and picks lines of a text file, as fileLines draws them: those the page's anchor names (#L3 or
// #L3-L5) when it opens, then those its viewer picks, which the anchor follows so that the page's URL points at
// them; the first line picked ends in a button that copies that URL. On a branch, panel is the card that starts a
// thread of the branch's chat on the lines picked, at the commit the page shows, and lists the chat's threads on
// the file; elsewhere there is none.
function linePicker(path, at, branch, f, can) {
  const named = /^#L(\d+)(?:-L(\d+))?$/.exec(location.hash);
  let range = named ? [Math.min(+named[1], +(named[2] || named[1])), Math.max(+named[1], +(named[2] || named[1]))] : null;
  let rows = [], from = range ? range[0] : 0;
  const start = input({ type: "number", name: "startLine", min: 1, required: true, value: range ? range[0] : "" });
  const end = input({ type: "number", name: "endLine", min: 1, required: true, value: range ? range[1] : "" });
  const link = h("button", { class: "file-line-link", type: "button", "aria-label": "Copy a link to these lines", title: "Copy a link to these lines" }, icon("link"));
  link.onclick = () => navigator.clipboard.writeText(location.href).then(() => {
    link.replaceChildren(icon("check"));
    setTimeout(() => link.replaceChildren(icon("link")), 2000);
  }, () => {});
  const mark = () => {
    rows.forEach((r, i) => r.classList.toggle("picked", !!range && i + 1 >= range[0] && i + 1 <= range[1]));
    if (range && rows[range[0] - 1]) rows[range[0] - 1].append(link);
    else link.remove();
  };
  const pick = (a, b) => {
    range = [Math.min(a, b), Math.max(a, b)];
    [start.value, end.value] = range;
    mark();
    history.replaceState(null, "", linesAnchor(...range));
  };
  // The marked lines come into view once the page shows them, which it does only after the calls that draw it: until
  // then, each frame looks again, for up to two seconds.
  const reveal = (frames = 120) => requestAnimationFrame(() => {
    const row = range && rows[range[0] - 1];
    if (row && row.isConnected) row.scrollIntoView({ block: "center" });
    else if (row && frames) reveal(frames - 1);
  });
  return {
    picked: () => range,
    attach(lines, all) {
      rows = lines;
      mark();
      all.addEventListener("click", (e) => {
        const n = +((e.target.dataset && e.target.dataset.line) || 0);
        if (!n) return;
        if (!e.shiftKey || !from) from = n;
        pick(from, n);
      });
    },
    panel: () => {
      reveal();
      return branch ? threadsCard(path, at, branch, f, can, start, end) : null;
    },
  };
}

// threadsCard is the card of the branch's chat's threads on a file, as File · Threads draws it: each by the lines it
// is on, its first words, who started it and when; and, for a viewer who may post, the form that starts one on the
// lines picked (start and end), at the commit the page shows.
async function threadsCard(path, at, branch, f, can, start, end) {
  const chat = path + "/branches/" + encodeURIComponent(branch) + "/chat";
  const { el: error, fail } = failure();
  let threads = [];
  try {
    threads = (await api("GET", chat + "?limit=100")).threads.filter((t) => t.anchor && t.anchor.path === f.path);
  } catch (err) {
    fail(err);
  }
  const firstWords = (t) => { const first = t.messages && t.messages[0]; return first && first.body ? first.body.split("\n")[0] : ""; };
  const lines = (a) => "L" + a.startLine + (a.endLine > a.startLine ? "-" + a.endLine : "");
  const row = (t) => h("li", { class: "file-thread" },
    h("a", { class: "file-thread-lines", href: at.branchPage(branch) + "#thread-" + t.id, "data-nav": "" }, lines(t.anchor)),
    h("span", { class: "file-thread-title" }, firstWords(t)),
    t.anchor.sha !== f.commit ? h("span", { class: "badge-quiet tone-neutral" }, "at " + shortSha(t.anchor.sha)) : null,
    t.resolvedAt ? h("span", { class: "badge-quiet tone-success" }, "Resolved") : null,
    t.startedBy ? h("span", { class: "file-thread-by" }, principalAvatar(t.startedBy), t.startedBy.displayName) : null,
    h("span", { class: "file-thread-when", title: whenExactly(t.createdAt) }, fromNow(t.createdAt)));
  let form = null;
  if (can("messages.post")) {
    const body = textarea({ name: "body", required: true, maxlength: 20000, rows: 4 });
    const button = h("button", { class: "btn btn-primary", type: "submit" }, "Start thread");
    const startThread = (e) => {
      e.preventDefault();
      button.disabled = true;
      api("POST", chat + "/threads", { body: body.value, anchor: { sha: f.commit, path: f.path, startLine: +start.value, endLine: +end.value } })
        .then((t) => go(at.branchPage(branch).slice(base.length - 1) + "#thread-" + t.id))
        .catch((err) => { button.disabled = false; fail(err); });
    };
    form = h("form", { class: "file-thread-form", onsubmit: startThread }, field("From line", start), field("To line", end),
      field("Message", body, "Everyone who can see the branch reads it; @mention an agent to bring it in."), h("div", { class: "file-thread-form-footer" }, button));
  }
  return h("section", { class: "card" }, h("div", { class: "card-header" }, h("h2", { class: "card-title" }, "Threads on this file"),
    h("span", { class: "card-meta" }, threads.length ? threads.length + " on this branch" : "Shift-click two lines to pick a range")), error,
  threads.length ? h("ul", { class: "list file-threads" }, threads.map(row)) : h("div", { class: "card-body" }, h("p", { class: "muted" }, "None yet.")), form);
}
