import { api } from "./api.js";
import { lineLabel, linesAnchor } from "./chat.js";
import { h } from "./dom.js";
import { shortSha, size, subject, whenExactly, who } from "./format.js";
import { rendered } from "./markdown.js";
import { go } from "./router.js";
import { base, config } from "./state.js";

// repositoryCode is a repository's Code page: how to clone it, and push to it for a viewer who may, over HTTPS with
// a personal token as the password, or over SSH with one of their SSH keys. An installation that names no Git
// server has nothing to clone yet.
function repositoryCode(org, repo, push) {
  const facts = h("p", {}, "It opens on branch ", h("code", {}, "main"), ", and its history names objects by ", h("code", {}, repo.objectFormat), ".");
  if (!config.gitUrl && !config.gitSshUrl) return h("section", { class: "panel" }, h("h3", {}, "Code"),
    h("p", {}, "This installation's Git server has no address yet, so its repositories can't be cloned. Its operator gives it one."), facts);
  const path = "/" + org.slug + "/" + repo.slug + ".git";
  const settings = (text) => h("a", { href: base + "settings", "data-nav": "" }, text);
  const ways = [];
  if (config.gitUrl) ways.push(cloneURL("HTTPS", config.gitUrl + path),
    h("p", {}, "Over HTTPS, Git asks for a password: give it a ", settings("personal token"), ", with any user name. ",
      push ? "Pushing takes a token that may push, as tokens do unless you narrow them." : ""));
  if (config.gitSshUrl) ways.push(cloneURL("SSH", config.gitSshUrl + path),
    h("p", {}, "Over SSH, Git signs in with one of ", settings("your SSH keys"), ", which acts as you, unnarrowed."));
  if (!push) ways.push(h("p", {}, "You may read this repository, but not push to it."));
  return h("section", { class: "panel" }, h("h3", {}, "Clone"), ...ways, facts);
}

// repositoryFiles is the Code tab's directory or file: under a picker of the repository's branches and tags and the
// path's breadcrumb, a directory lists its directories first and then its files, and a file shows its text, its
// image, or its size, with links to its bytes. Its URL's parts after tree/ or blob/ are a ref, then a path: the
// longest branch or tag name they start with, or else their first part, a commit's full name. A repository nothing
// was pushed to shows how to push its first commit.
export async function repositoryFiles(org, repo, path, at, isFile, rest, can) {
  const push = can("repo.push");
  const [branches, tags] = await Promise.all([api("GET", path + "/branches").then((d) => d.branches), api("GET", path + "/tags").then((d) => d.tags)]);
  if (!branches.length) return [repositoryCode(org, repo, push), h("section", { class: "panel" }, h("h3", {}, "Files"),
    h("p", {}, "Nothing has been pushed yet: push ", h("code", {}, "main"), " to see its files."))];
  const [ref, file] = splitRef(rest, [...branches.map((b) => b.name), ...tags.map((t) => t.name)]);
  const toolbar = h("div", { class: "row" }, refPicker(branches, tags, ref, (r) => go((isFile ? at.blob(r, file) : at.tree(r, file)).slice(base.length - 1))),
    breadcrumb(repo, at, ref, file), h("span", { class: "spacer" }), clone(org, repo, push));
  const onBranch = branches.some((b) => b.name === ref) ? ref : null;
  const shown = isFile ? await repositoryFile(path, at, ref, file, onBranch, can) : await repositoryTree(path, at, ref, file, branches);
  return [toolbar, ...shown];
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

// refPicker picks a branch or a tag, and shows a commit's full name, which neither is, as its own.
export function refPicker(branches, tags, ref, pick) {
  const known = branches.some((b) => b.name === ref) || tags.some((t) => t.name === ref);
  return h("select", { "aria-label": "Branch or tag", class: "ref", onchange: (e) => pick(e.target.value) },
    known ? null : h("option", { value: ref, selected: true }, /^[0-9a-f]{40,64}$/.test(ref) ? shortSha(ref) : ref),
    h("optgroup", { label: "Branches" }, branches.map((b) => h("option", { value: b.name, selected: b.name === ref }, b.name))),
    tags.length ? h("optgroup", { label: "Tags" }, tags.map((t) => h("option", { value: t.name, selected: t.name === ref }, t.name))) : null);
}

// breadcrumb links the repository's root and each directory on the way to path.
function breadcrumb(repo, at, ref, path) {
  const names = path ? path.split("/") : [];
  const crumbs = [h("a", { href: at.tree(ref, ""), "data-nav": "" }, repo.slug)];
  names.forEach((name, i) => crumbs.push(" / ", i === names.length - 1 ? h("strong", {}, name)
    : h("a", { href: at.tree(ref, names.slice(0, i + 1).join("/")), "data-nav": "" }, name)));
  return h("code", { class: "crumbs" }, crumbs);
}

// clone is the Clone button, which opens the repository's clone URLs.
function clone(org, repo, push) {
  return h("details", { class: "clone" }, h("summary", { class: "button small" }, "Clone"), repositoryCode(org, repo, push));
}

// unreadable is what a page shows when the API couldn't read a directory or a file: what a ref or a path found
// nothing at, an installation without a Git server, or any other failure.
function unreadable(err, ref, path) {
  const what = { "ref.not_found": ["No branch, tag or commit is named ", h("code", {}, ref), "."],
    "path.not_found": ["There is nothing at ", h("code", {}, path), " in ", h("code", {}, ref), "."],
    "path.submodule": [h("code", {}, path), " is a submodule, whose files are another repository's."],
    "git.unavailable": ["This installation runs no Git server, so its files can't be shown."] }[err.code];
  return [h("section", { class: "panel" + (what ? "" : " notice") }, h("p", {}, what || ["The files couldn't be read: " + err.message]))];
}

// repositoryTree lists the directory at path of ref, under the commit it is at, with a link to a branch's
// history, and its README beneath. A file's path goes to its file's page.
async function repositoryTree(path, at, ref, dir, branches) {
  let tree;
  try {
    tree = await api("GET", path + "/tree?" + new URLSearchParams({ ref, path: dir }));
  } catch (err) {
    if (err.code === "path.file") return go(at.blob(ref, dir).slice(base.length - 1), true), [];
    return unreadable(err, ref, dir);
  }
  const latest = await api("GET", path + "/commits/" + tree.commit).catch(() => null);
  const head = h("div", { class: "row" }, latest ? h("div", {}, h("a", { class: "subject", href: at.commit(latest.sha), "data-nav": "" }, subject(latest.message)),
    h("p", {}, who(latest.author), " · ", whenExactly(latest.author.at))) : h("p", {}, "At ", h("code", {}, shortSha(tree.commit))),
    h("div", { class: "row" }, h("a", { href: at.commit(tree.commit), "data-nav": "" }, h("code", {}, shortSha(tree.commit))),
      h("a", { class: "button small", href: at.history(ref), "data-nav": "" }, "History")));
  const row = (e) => {
    const name = e.kind === "tree" ? h("a", { href: at.tree(ref, e.path), "data-nav": "" }, e.name + "/")
      : e.kind === "submodule" ? [e.name, " ", h("span", { class: "muted" }, "@ " + shortSha(e.sha))]
        : h("a", { href: at.blob(ref, e.path), "data-nav": "" }, e.name);
    const kind = { tree: "Directory", submodule: "Submodule", symlink: "Symlink" }[e.kind] || (e.mode === "100755" ? "Executable" : "");
    return h("tr", {}, h("td", { class: "name" }, name), h("td", { class: "muted" }, kind), h("td", { class: "size muted" }, e.size === undefined ? "" : size(e.size)));
  };
  const up = dir ? h("tr", {}, h("td", { colspan: 3 }, h("a", { href: at.tree(ref, dir.split("/").slice(0, -1).join("/")), "data-nav": "" }, ".."))) : null;
  return [h("section", { class: "panel" }, head, h("table", { class: "files" }, h("tbody", {}, up, tree.entries.map(row))),
    tree.entries.length ? null : h("p", {}, "This directory is empty."), tree.readme ? readme(at, ref, tree.readme) : null)];
}

// readme is a directory's README beneath its listing: rendered when it is Markdown, else its text.
function readme(at, ref, f) {
  return h("div", { class: "readme" }, h("h3", {}, h("a", { href: at.blob(ref, f.path), "data-nav": "" }, f.name)),
    f.html !== undefined ? rendered(f.html) : h("pre", {}, f.text),
    f.truncated ? h("p", { class: "notice" }, "This shows the README's first 1 MiB.") : null);
}

// imageFile reports whether a file's name is an image's, which its page shows from its bytes.
const imageFile = (name) => /\.(png|jpe?g|gif|webp|avif|bmp|ico|svg)$/i.test(name);

// repositoryFile shows the file at path of ref: a Markdown file rendered, its text with line numbers, its image, or
// its size, with links to its bytes at the commit ref is at. A symlink shows its target; a directory's path goes to its directory's page.
// On a branch, a text file's lines start threads of the branch's chat, and the chat's threads on the file are listed.
async function repositoryFile(path, at, ref, file, branch, can) {
  let f;
  try {
    f = await api("GET", path + "/blob?" + new URLSearchParams({ ref, path: file }));
  } catch (err) {
    if (err.code === "path.directory") return go(at.tree(ref, file).slice(base.length - 1), true), [];
    return unreadable(err, ref, file);
  }
  const links = h("div", { class: "row" }, h("a", { class: "button small", href: at.raw(f.commit, f.path), target: "_blank", rel: "noopener" }, "Raw"),
    h("a", { class: "button small", href: at.raw(f.commit, f.path, true) }, "Download"));
  const facts = [size(f.size), f.mode === "100755" ? "executable" : null, f.kind === "symlink" ? "symlink" : null].filter(Boolean).join(" · ");
  const head = h("div", { class: "row" }, h("div", {}, h("h2", {}, f.name), h("p", {}, facts)), links);
  let body, lines = null;
  if (f.kind === "symlink") {
    const target = f.path.split("/").slice(0, -1).concat(f.text.split("/")).reduce((p, n) => (n === ".." ? p.slice(0, -1) : n === "." || !n ? p : [...p, n]), []).join("/");
    body = h("p", {}, "A symlink to ", h("a", { href: at.blob(ref, target), "data-nav": "" }, h("code", {}, f.text)), ".");
  } else if (imageFile(f.name)) body = h("div", { class: "image" }, h("img", { src: at.raw(f.commit, f.path), alt: f.name }));
  else if (f.binary) body = h("p", {}, "A binary file of " + size(f.size) + ": download it to open it.");
  else {
    lines = fileLines(path, at, branch, f, can);
    body = f.html !== undefined ? markdownFile(f, lines) : source(f.text, lines);
  }
  const panel = h("section", { class: "panel" }, head, f.truncated ? h("p", { class: "notice" }, "This shows the file's first 1 MiB. The rest is in its raw bytes.") : null, body);
  return [panel, lines ? await lines.panel() : null].filter(Boolean);
}

// markdownFile shows a Markdown file rendered, with a switch to its source, which it shows first when the page's
// anchor names lines.
function markdownFile(f, lines) {
  const preview = rendered(f.html), code = source(f.text, lines);
  const atLines = lines && lines.picked() !== null;
  (atLines ? preview : code).hidden = true;
  const tab = (label, shown) => h("button", { class: "button small", type: "button", "aria-pressed": String(shown), onclick: (e) => {
    preview.hidden = label !== "Preview";
    code.hidden = label === "Preview";
    e.target.parentElement.querySelectorAll("button").forEach((b) => b.setAttribute("aria-pressed", String(b === e.target)));
  } }, label);
  return h("div", { class: "markdown-file" }, h("div", { class: "tabs", role: "group", "aria-label": "View" }, tab("Preview", !atLines), tab("Source", atLines)), preview, code);
}

// source is a text with its lines numbered: with lines, a click on a line's number picks that line and a shift-click
// the lines from the one picked before, which lines marks and takes.
function source(text, lines) {
  const all = text.replace(/\n$/, "").split("\n");
  const numbers = all.map((_, i) => h("span", { "data-line": i + 1 }, String(i + 1)));
  const gutter = h("pre", { class: "ln", "aria-hidden": "true" }, numbers);
  if (lines) lines.attach(numbers, gutter);
  return h("div", { class: "source" + (lines ? " pickable" : "") }, gutter, h("pre", { class: "text" }, all.join("\n")));
}

// cloneURL is one way to clone a repository: its name and URL, with a button that copies the URL.
function cloneURL(name, url) {
  const code = h("code", {}, url);
  const copy = h("button", { class: "button small", type: "button", onclick: () => {
    navigator.clipboard.writeText(url).then(() => { copy.textContent = "Copied"; }).catch(() => { getSelection().selectAllChildren(code); });
  } }, "Copy");
  return h("div", { class: "row" }, h("span", { class: "chip" }, name), code, copy);
}

// fileLines marks and picks lines of a text file, as source draws them: those the page's anchor names (#L3 or
// #L3-L5) when it opens, then those its viewer picks, which the anchor follows so that the page's URL points at
// them. On a branch, panel is the section that starts a thread of the branch's chat on the lines picked, at the
// commit the page shows, and lists the chat's threads on the file; elsewhere there is none.
function fileLines(path, at, branch, f, can) {
  const named = /^#L(\d+)(?:-L(\d+))?$/.exec(location.hash);
  let range = named ? [Math.min(+named[1], +(named[2] || named[1])), Math.max(+named[1], +(named[2] || named[1]))] : null;
  let numbers = [], from = range ? range[0] : 0;
  const start = h("input", { type: "number", name: "startLine", min: 1, required: true, value: range ? range[0] : "" });
  const end = h("input", { type: "number", name: "endLine", min: 1, required: true, value: range ? range[1] : "" });
  const mark = () => numbers.forEach((n, i) => n.classList.toggle("picked", !!range && i + 1 >= range[0] && i + 1 <= range[1]));
  const pick = (a, b) => {
    range = [Math.min(a, b), Math.max(a, b)];
    [start.value, end.value] = range;
    mark();
    history.replaceState(null, "", linesAnchor(...range));
  };
  // The marked lines come into view once the page is drawn: after the calls that draw it, which take no turn.
  const reveal = () => setTimeout(() => { if (range && numbers[range[0] - 1]) numbers[range[0] - 1].scrollIntoView({ block: "center" }); }, 0);
  return {
    picked: () => range,
    attach(spans, gutter) {
      numbers = spans;
      mark();
      gutter.addEventListener("click", (e) => {
        const n = +((e.target.dataset && e.target.dataset.line) || 0);
        if (!n) return;
        if (!e.shiftKey || !from) from = n;
        pick(from, n);
      });
    },
    async panel() {
      reveal();
      if (!branch) return null;
      const chat = path + "/branches/" + encodeURIComponent(branch) + "/chat";
      const error = h("p", { class: "alert", hidden: true });
      const fail = (err) => { error.textContent = err.message; error.hidden = false; };
      let threads = [];
      try {
        threads = (await api("GET", chat + "?limit=100")).threads.filter((t) => t.anchor && t.anchor.path === f.path);
      } catch (err) {
        fail(err);
      }
      const firstWords = (t) => { const first = t.messages && t.messages[0]; return first && first.body ? first.body.split("\n")[0].slice(0, 80) : ""; };
      const row = (t) => h("li", {}, h("a", { href: at.branchPage(branch) + "#thread-" + t.id, "data-nav": "" }, h("code", {}, lineLabel(t.anchor))), " ",
        t.anchor.sha !== f.commit ? h("span", { class: "chip" }, "at " + shortSha(t.anchor.sha)) : null, t.resolvedAt ? h("span", { class: "chip" }, "resolved") : null,
        " ", h("span", { class: "muted" }, firstWords(t)));
      let form = null;
      if (can("messages.post")) {
        const body = h("textarea", { name: "body", required: true, maxlength: 20000, placeholder: "What about these lines? In Markdown." });
        const button = h("button", { class: "button primary", type: "submit" }, "Start thread");
        const startThread = (e) => {
          e.preventDefault();
          button.disabled = true;
          api("POST", chat + "/threads", { body: body.value, anchor: { sha: f.commit, path: f.path, startLine: +start.value, endLine: +end.value } })
            .then((t) => go(at.branchPage(branch).slice(base.length - 1) + "#thread-" + t.id))
            .catch((err) => { button.disabled = false; fail(err); });
        };
        form = h("form", { class: "lines", onsubmit: startThread }, h("label", {}, "From line", start), h("label", {}, "to line", end), body,
          h("div", { class: "row" }, h("span", { class: "muted" }, "Pick lines by their numbers: click one, then shift-click another."), button));
      }
      return h("section", { class: "panel" }, h("h3", {}, "Threads on this file"), error,
        threads.length ? h("ul", { class: "file-threads" }, threads.map(row)) : h("p", {}, "None yet on ", h("code", {}, branch), "'s chat."), form);
    },
  };
}
