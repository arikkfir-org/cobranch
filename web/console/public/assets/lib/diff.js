import { api } from "./api.js";
import { h } from "./dom.js";
import { plural, shortSha } from "./format.js";

// diffPanel reads the diff at url and shows it under title, or why it couldn't; with drafting, a reviewer drafts
// comments on its lines.
export async function diffPanel(url, at, title, drafting) {
  const panel = h("section", { class: "panel" }, h("h3", {}, title));
  let d;
  try {
    d = await api("GET", url);
  } catch (err) {
    panel.append(h("p", {}, err.code === "git.unavailable" ? "This installation runs no Git server, so its changes can't be shown."
      : "The changes couldn't be read: " + err.message));
    return panel;
  }
  const files = d.files.length + d.filesLeftOut;
  panel.append(h("p", { class: "diff-summary" }, files ? plural(files, "file") + " changed" : "No files changed",
    h("span", { class: "added-count" }, "+" + d.additions), h("span", { class: "removed-count" }, "\u2212" + d.deletions),
    h("span", { class: "muted" }, "from ", d.base ? h("a", { href: at.commit(d.base), "data-nav": "" }, h("code", {}, shortSha(d.base))) : "nothing", " to ",
      h("a", { href: at.commit(d.head), "data-nav": "" }, h("code", {}, shortSha(d.head))))));
  if (d.files.length) panel.append(h("div", { class: "diff-files" }, d.files.map((f) => fileDiffView(f, at, d, drafting))));
  if (d.filesLeftOut) panel.append(h("p", { class: "muted" }, plural(d.filesLeftOut, "more file") + " changed past the 3,000 a diff shows."));
  return panel;
}

// fileStatuses are the words for what a diff did to a file.
const fileStatuses = { added: "Added", modified: "Modified", deleted: "Deleted", renamed: "Renamed", type_changed: "Type changed" };

// fileDiffView is one file's change, folded when it is long: its path, what changed, its counts, and its hunks, or
// why it shows none.
function fileDiffView(f, at, d, drafting) {
  const lines = f.hunks.reduce((n, hk) => n + hk.lines.length, 0);
  const file = f.status === "deleted" ? (d.base ? at.blob(d.base, f.path) : null) : at.blob(d.head, f.path);
  const summary = h("summary", {}, h("span", { class: "chip" }, fileStatuses[f.status]),
    f.oldPath ? [h("code", {}, f.oldPath), " \u2192 "] : null, file ? h("a", { href: file, "data-nav": "" }, h("code", {}, f.path)) : h("code", {}, f.path),
    f.binary ? null : h("span", { class: "counts" }, h("span", { class: "added-count" }, "+" + f.additions), h("span", { class: "removed-count" }, "\u2212" + f.deletions)));
  const notes = [];
  if (f.oldMode && f.mode && f.oldMode !== f.mode) notes.push("Mode " + f.oldMode + " \u2192 " + f.mode + ".");
  if (f.binary) notes.push("A binary file: its lines don't show.");
  else if (f.truncated) notes.push("Too large to show here: " + plural(f.additions + f.deletions, "line") + " changed.");
  else if (!f.hunks.length && !notes.length) notes.push(f.status === "renamed" ? "Renamed without changes." : "An empty file.");
  return h("details", { class: "file-diff", open: lines <= 500 ? "" : null }, summary, notes.map((n) => h("p", { class: "note" }, n)),
    f.hunks.length ? h("div", { class: "hunks" }, h("table", { class: "hunk" }, h("tbody", {}, f.hunks.map((hk) => hunkRows(hk, drafting && lineDrafter(f, d, drafting)))))) : null);
}

// hunkRows is a hunk as table rows: its header, then each line with its numbers in the old file and the new; with
// drafter, each line also takes a comment, and shows those drafted on it.
function hunkRows(hk, drafter) {
  const marks = { context: " ", added: "+", removed: "-" };
  const head = h("tr", { class: "hunk-head" }, h("td", { colspan: drafter ? "5" : "4" }, "@@ -" + hk.oldStart + "," + hk.oldLines + " +" + hk.newStart + "," + hk.newLines + " @@" +
    (hk.section ? " " + hk.section : "")));
  return [head, hk.lines.map((l) => [h("tr", { class: l.kind }, drafter ? h("td", { class: "act" }, drafter.button(l)) : null,
    h("td", { class: "ln" }, l.old || ""), h("td", { class: "ln" }, l.new || ""), h("td", { class: "mark" }, marks[l.kind]),
    h("td", { class: "text" }, l.text, l.noNewline ? h("span", { class: "no-newline" }, "  No newline at end of file") : null)), drafter ? drafter.drafted(l) : null])];
}

// lineDrafter drafts comments on the lines of file f of diff d into drafting's: an added or unchanged line is anchored
// at its number in d's head, a removed one at its number in d's base, where the reviewer saw it.
function lineDrafter(f, d, drafting) {
  const anchorOf = (l) => l.kind === "removed" ? { sha: d.base, path: f.oldPath || f.path, startLine: l.old, endLine: l.old }
    : { sha: d.head, path: f.path, startLine: l.new, endLine: l.new };
  const same = (a, b) => a.sha === b.sha && a.path === b.path && a.startLine === b.startLine && a.endLine === b.endLine;
  const drafted = (c) => {
    const remove = () => {
      drafting.comments.splice(drafting.comments.indexOf(c), 1);
      row.remove();
      drafting.changed();
    };
    const row = h("tr", { class: "draft" }, h("td", { colspan: "5" }, h("div", { class: "draft-comment" },
      h("div", { class: "row" }, h("span", { class: "chip" }, "Draft"), h("span", { class: "muted" }, "Sent with your review"), h("span", { class: "spacer" }),
        h("button", { class: "button small", type: "button", onclick: remove }, "Remove")),
      h("p", { class: "text" }, c.body))));
    return row;
  };
  const open = (line, anchor) => {
    const text = h("textarea", { required: true, maxlength: 20000, placeholder: "What about this line? In Markdown.", "aria-label": "Comment" });
    const add = (e) => {
      e.preventDefault();
      const c = { anchor, body: text.value };
      drafting.comments.push(c);
      row.replaceWith(drafted(c));
      drafting.changed();
    };
    const row = h("tr", { class: "draft" }, h("td", { colspan: "5" }, h("form", { class: "draft-comment", onsubmit: add }, text,
      h("div", { class: "row" }, h("span", { class: "spacer" }), h("button", { class: "button small", type: "button", onclick: () => row.remove() }, "Cancel"),
        h("button", { class: "button small primary", type: "submit" }, "Add comment")))));
    line.after(row);
    text.focus();
  };
  return {
    button: (l) => {
      const a = anchorOf(l);
      return h("button", { class: "add-comment", type: "button", title: "Comment on this line", "aria-label": "Comment on line " + a.startLine + " of " + a.path,
        onclick: (e) => open(e.currentTarget.closest("tr"), a) }, "+");
    },
    drafted: (l) => drafting.comments.filter((c) => same(c.anchor, anchorOf(l))).map(drafted),
  };
}
