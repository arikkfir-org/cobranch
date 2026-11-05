// A diff, as the Commit and Branch screens draw it: a line counting the files and lines it changes beside the Unified
// and Split switch, then a card of each file's change (Diff card), its hunks' lines in one column, or the old beside
// the new.

import { api } from "./api.js";
import { h } from "./dom.js";
import { banner } from "./forms.js";
import { plural } from "./format.js";
import { icon } from "./icons.js";
import { popover } from "./menu.js";

// layoutKey is where this browser keeps how its viewer last read a diff, split or unified, on every page.
const layoutKey = "cobranch.diff.layout";
const savedLayout = () => {
  try {
    return localStorage.getItem(layoutKey) === "split" ? "split" : "unified";
  } catch {
    return "unified";
  }
};

// longFile is how many lines of a file's change show at once: a longer one waits for its viewer to ask.
const longFile = 500;

// diffView reads the diff at url and answers what shows it: its counts, the layout switch and its files' cards, or
// why it couldn't be read. With drafting, a reviewer drafts comments on its lines.
export async function diffView(url, at, drafting) {
  let d;
  try {
    d = await api("GET", url);
  } catch (err) {
    return [banner("warning", null, err.code === "git.unavailable" ? "This installation runs no Git server, so its changes can't be shown."
      : "The changes couldn't be read: " + err.message)];
  }
  let layout = savedLayout();
  const cards = h("div", { class: "diff-cards" });
  const draw = () => cards.replaceChildren(...d.files.map((f) => diffCard(f, at, d, drafting, layout)));
  const choose = (to) => () => {
    layout = to;
    try {
      localStorage.setItem(layoutKey, to);
    } catch {}
    for (const b of switcher.children) b.setAttribute("aria-pressed", String(b.value === to));
    draw();
  };
  const switcher = h("div", { class: "segmented diff-layout", role: "group", "aria-label": "Layout" },
    [["unified", "Unified"], ["split", "Split"]].map(([value, label]) => h("button", { type: "button", value, "aria-pressed": String(layout === value), onclick: choose(value) }, label)));
  const files = d.files.length + d.filesLeftOut;
  const stats = h("div", { class: "diff-stats" }, h("span", { class: "diff-stats-files" }, files ? plural(files, "file") + " changed" : "No files changed"),
    files ? [h("span", { class: "diff-added" }, "+" + d.additions), h("span", { class: "diff-removed" }, "−" + d.deletions)] : null,
    h("span", { class: "spacer" }), d.files.length ? switcher : null);
  draw();
  return [stats, d.files.length ? cards : null,
    d.filesLeftOut ? h("p", { class: "diff-left-out" }, plural(d.filesLeftOut, "more file") + " changed past the 3,000 a diff shows.") : null];
}

// diffCard is one file's change (Diff card): its path, what it adds and removes and a menu to its file, over its
// hunks, laid out as layout says, or why it shows none.
function diffCard(f, at, d, drafting, layout) {
  const file = f.status === "deleted" ? (d.base ? at.blob(d.base, f.path) : null) : at.blob(d.head, f.path);
  const more = h("button", { class: "icon-btn icon-btn-ghost icon-btn-sm", type: "button", "aria-label": "More for " + f.path, title: "More", "aria-haspopup": "menu",
    onclick: () => fileMenu(more, f, file) }, icon("ellipsis"));
  const header = h("div", { class: "diff-card-header" }, icon("file-code"),
    h("span", { class: "diff-card-path", title: f.oldPath ? f.oldPath + " → " + f.path : f.path }, f.oldPath ? f.oldPath + " → " + f.path : f.path),
    f.binary ? null : [h("span", { class: "diff-added" }, "+" + f.additions), h("span", { class: "diff-removed" }, "−" + f.deletions)],
    h("span", { class: "spacer" }), more);
  const notes = [];
  if (f.oldMode && f.mode && f.oldMode !== f.mode) notes.push("Mode " + f.oldMode + " → " + f.mode + ".");
  if (f.binary) notes.push("A binary file: its lines don't show.");
  else if (f.truncated) notes.push("Too large to show here: " + plural(f.additions + f.deletions, "line") + " changed.");
  else if (!f.hunks.length && !notes.length) notes.push(f.status === "renamed" ? "Renamed without changes." : f.status === "added" ? "An empty file, added." : "An empty file.");
  const drafter = drafting ? lineDrafter(f, d, drafting) : null;
  const lines = () => layout === "split" ? splitLines(f.hunks, drafter) : unifiedLines(f.hunks, drafter);
  const count = f.hunks.reduce((n, hk) => n + hk.lines.length, 0);
  let body = null;
  if (f.hunks.length && count <= longFile) body = lines();
  else if (f.hunks.length) {
    const show = h("button", { class: "btn btn-sm", type: "button", onclick: () => wait.replaceWith(lines()) }, "Show " + plural(count, "line"));
    const wait = h("div", { class: "diff-note" }, h("span", {}, "A long change, folded."), show);
    body = wait;
  }
  return h("section", { class: "card diff-card", "aria-label": f.path }, header, notes.map((n) => h("p", { class: "diff-note" }, n)), body);
}

// fileMenu opens a file's menu under anchor: View file, at the commit that holds it, and Copy path.
function fileMenu(anchor, f, file) {
  popover(anchor, "end", (close) => [
    file ? h("a", { class: "menu-item", href: file, "data-nav": "", role: "menuitem", onclick: close }, icon("file"), h("span", {}, "View file")) : null,
    h("button", { class: "menu-item", type: "button", role: "menuitem", onclick: () => { close(); navigator.clipboard.writeText(f.path).catch(() => {}); } },
      icon("copy"), h("span", {}, "Copy path"))]);
}

// signs are what a line's sign column shows for each kind of line.
const signs = { context: " ", added: "+", removed: "-" };

// hunkHead is a hunk's header line: its ranges in the old file and the new, and the section it falls in.
const hunkHead = (hk) => "@@ -" + hk.oldStart + "," + hk.oldLines + " +" + hk.newStart + "," + hk.newLines + " @@" + (hk.section ? " " + hk.section : "");

// lineText is a line's text, and the mark of a last line without a newline.
const lineText = (l) => h("span", { class: "diff-text" }, l.text, l.noNewline ? h("span", { class: "diff-no-newline" }, "  No newline at end of file") : null);

// unifiedLines are hunks in one column: each line under its numbers in the old file and the new, then its sign; with
// drafter, a line takes a comment and shows those drafted on it beneath.
function unifiedLines(hunks, drafter) {
  const rows = hunks.flatMap((hk) => [h("div", { class: "diff-line diff-hunk" }, h("span", { class: "diff-text" }, hunkHead(hk))),
    hk.lines.flatMap((l) => [h("div", { class: "diff-line diff-" + l.kind }, drafter ? drafter.button(l) : null,
      h("span", { class: "diff-num" }, l.old || ""), h("span", { class: "diff-num" }, l.new || ""), h("span", { class: "diff-sign" }, signs[l.kind]), lineText(l)),
    drafter ? drafter.drafted(l) : []])]);
  return h("div", { class: "diff-lines" }, h("div", { class: "diff-rows" }, rows));
}

// splitLines are hunks with the old beside the new: context on both sides, and each run of removed lines beside the
// run of added lines after it, the shorter side padded with filler; each half repeats the hunk's header. With drafter,
// a removed line takes a comment on the old side and every other line on the new, and drafts span both.
function splitLines(hunks, drafter) {
  const half = (l, side) => l ? h("div", { class: "diff-half diff-" + l.kind }, drafter && (side === "old") === (l.kind === "removed") ? drafter.button(l) : null,
    h("span", { class: "diff-num" }, (side === "old" ? l.old : l.new) || ""), h("span", { class: "diff-sign" }, signs[l.kind]), lineText(l))
    : h("div", { class: "diff-half diff-filler" });
  const row = (old, now) => [h("div", { class: "diff-split-row" }, half(old, "old"), half(now, "new")),
    drafter ? [old && old.kind === "removed" ? drafter.drafted(old) : [], now ? drafter.drafted(now) : []] : []];
  const rows = hunks.flatMap((hk) => {
    const out = [h("div", { class: "diff-split-row" }, [0, 1].map(() => h("div", { class: "diff-half diff-hunk" }, h("span", { class: "diff-text" }, hunkHead(hk)))))];
    for (let i = 0; i < hk.lines.length;) {
      if (hk.lines[i].kind === "context") {
        out.push(row(hk.lines[i], hk.lines[i]));
        i++;
        continue;
      }
      const removed = [], added = [];
      while (i < hk.lines.length && hk.lines[i].kind === "removed") removed.push(hk.lines[i++]);
      while (i < hk.lines.length && hk.lines[i].kind === "added") added.push(hk.lines[i++]);
      for (let j = 0; j < Math.max(removed.length, added.length); j++) out.push(row(removed[j], added[j]));
    }
    return out;
  });
  return h("div", { class: "diff-lines diff-split" }, rows);
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
    const row = h("div", { class: "diff-draft" }, h("div", { class: "draft-comment" },
      h("div", { class: "draft-comment-header" }, h("span", { class: "tag tone-info" }, "Draft"), h("span", { class: "muted" }, "Sent with your review"),
        h("span", { class: "spacer" }), h("button", { class: "btn btn-sm btn-ghost", type: "button", onclick: remove }, "Remove")),
      h("p", { class: "draft-comment-text" }, c.body)));
    return row;
  };
  const open = (line, anchor) => {
    const text = h("textarea", { class: "textarea", required: true, maxlength: 20000, placeholder: "What about this line? In Markdown.", "aria-label": "Comment" });
    const add = (e) => {
      e.preventDefault();
      const c = { anchor, body: text.value };
      drafting.comments.push(c);
      row.replaceWith(drafted(c));
      drafting.changed();
    };
    const row = h("div", { class: "diff-draft" }, h("form", { class: "draft-comment", onsubmit: add }, text,
      h("div", { class: "draft-comment-actions" }, h("button", { class: "btn btn-sm", type: "button", onclick: () => row.remove() }, "Cancel"),
        h("button", { class: "btn btn-sm btn-primary", type: "submit" }, "Add comment"))));
    line.after(row);
    text.focus();
  };
  return {
    button: (l) => {
      const a = anchorOf(l);
      return h("button", { class: "diff-comment", type: "button", title: "Comment on this line", "aria-label": "Comment on line " + a.startLine + " of " + a.path,
        onclick: (e) => open(e.currentTarget.closest(".diff-line, .diff-split-row"), a) }, icon("plus"));
    },
    drafted: (l) => drafting.comments.filter((c) => same(c.anchor, anchorOf(l))).map(drafted),
  };
}
