// A diff, as the Commit and Branch screens draw it: a line counting the files and lines it changes beside the Unified
// and Split switch, then a card of each file's change (Diff card), its hunks' lines in one column, or the old beside
// the new. A branch's lists its files first, and draws its chat's open threads under the lines they are on, and its
// viewer's Viewed marks, which fold the files they hold.

import { api } from "./api.js";
import { lineThreads } from "./diffthreads.js";
import { h } from "./dom.js";
import { banner, checkbox } from "./forms.js";
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
// why it couldn't be read. With drafting, a reviewer drafts comments on its lines. With chat, a branch's conversation
// ({ chat, post, moderate } as its subject has them), a card lists its files first, and the conversation's open
// threads show under the lines they are on. With viewed, the URL of a signed-in viewer's Viewed marks on the branch,
// each file's card has Viewed, which marks it and folds it.
export async function diffView(url, at, drafting, chat, viewed) {
  let d, threads, marks;
  try {
    // Threads or marks that can't be read leave the diff without them, and say so.
    [d, threads, marks] = await Promise.all([api("GET", url), chat ? lineThreads(chat).catch((err) => ({ failed: err })) : null,
      viewed ? viewedMarks(viewed).catch((err) => ({ failed: err })) : null]);
  } catch (err) {
    return [banner("warning", null, err.code === "git.unavailable" ? "This installation runs no Git server, so its changes can't be shown."
      : "The changes couldn't be read: " + err.message)];
  }
  let layout = savedLayout();
  const cards = h("div", { class: "diff-cards" });
  const unread = threads && threads.failed ? banner("warning", null, "The branch's threads on these lines couldn't be read: " + threads.failed.message) : null;
  if (unread) threads = null;
  const unmarked = marks && marks.failed ? banner("warning", null, "Your Viewed marks couldn't be read: " + marks.failed.message) : null;
  if (unmarked) marks = null;
  // Why a mark didn't change, once one didn't.
  const problem = h("div", { hidden: true });
  if (marks) {
    marks.fails((err) => { problem.replaceChildren(banner("warning", null, "The Viewed mark couldn't change: " + err.message)); problem.hidden = false; });
    marks.listen(() => { problem.hidden = true; });
  }
  const draw = () => cards.replaceChildren(...d.files.map((f, i) => diffCard(f, i, at, d, drafting, threads, layout, marks)));
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
  return [stats, unread, unmarked, problem, chat && d.files.length ? filesCard(d, cards, marks) : null, d.files.length ? cards : null,
    d.filesLeftOut ? h("p", { class: "diff-left-out" }, plural(d.filesLeftOut, "more file") + " changed past the 3,000 a diff shows.") : null];
}

// pathOf is how a file's change names it: its path, after the one it had for a renamed file.
const pathOf = (f) => f.oldPath ? f.oldPath + " → " + f.path : f.path;

// viewedMarks reads a viewer's Viewed marks on a branch at url and answers what keeps them: has(f) tells whether the
// diff's file f is marked at the content it shows, so a file changed since its mark shows unviewed again; set(f, on)
// marks it there, or takes its mark off, then tells each of listen's listeners; fail(err) and fails(listener) say why
// a mark didn't change.
async function viewedMarks(url) {
  let files = (await api("GET", url)).files;
  const listeners = [], failures = [];
  return {
    has: (f) => !!files[f.path] && (files[f.path].object || "") === (f.object || ""),
    listen: (fn) => listeners.push(fn),
    fails: (fn) => failures.push(fn),
    fail: (err) => failures.forEach((fn) => fn(err)),
    async set(f, on) {
      // A file the branch deletes has no object: its mark holds none.
      const mark = on ? (f.object ? { object: f.object } : {}) : null;
      files = (await api("PATCH", url, { files: { [f.path]: mark } }, { "Content-Type": "application/merge-patch+json", "If-Match": "*" })).files;
      listeners.forEach((fn) => fn());
    },
  };
}

// filesCard is the card that lists a diff's files (Files), each with what it adds and removes, and a link down to its
// change among cards, the n-th file's the n-th card; with marks, a file marked Viewed shows muted, with a check.
function filesCard(d, cards, marks) {
  const down = (i) => (e) => {
    e.preventDefault();
    const card = cards.children[i];
    if (card) card.scrollIntoView({ block: "start" });
  };
  const rows = d.files.map((f, i) => {
    const link = h("a", { class: "diff-files-path", href: "#file-" + (i + 1), title: pathOf(f), onclick: down(i) }, pathOf(f));
    const check = h("span", { class: "diff-files-viewed", role: "img", "aria-label": "Viewed", title: "Viewed", hidden: true }, icon("check"));
    const paint = () => {
      const on = !!marks && marks.has(f);
      link.classList.toggle("diff-files-path-viewed", on);
      check.hidden = !on;
    };
    paint();
    return { paint, el: h("li", { class: "diff-files-row" }, icon("file-code"), link, h("span", { class: "spacer" }),
      f.binary ? h("span", { class: "diff-files-binary" }, "binary") : [h("span", { class: "diff-added" }, "+" + f.additions), h("span", { class: "diff-removed" }, "−" + f.deletions)],
      check) };
  });
  if (marks) marks.listen(() => rows.forEach((r) => r.paint()));
  return h("section", { class: "card diff-files", "aria-label": "Files" }, h("div", { class: "card-header" }, h("h2", { class: "card-title" }, "Files")),
    h("ul", { class: "diff-files-list" }, rows.map((r) => r.el)));
}

// viewedBox is a file's Viewed checkbox in its card's header, which folds card while the viewer has f marked Viewed in
// marks, and marks it, or takes the mark off, when they check it; a mark that doesn't change unfolds it again.
function viewedBox(f, card, marks) {
  const fold = (on) => card.classList.toggle("diff-card-viewed", on);
  const box = h("input", { type: "checkbox", checked: marks.has(f), "aria-label": "Viewed " + f.path, onchange: () => {
    const on = box.checked;
    box.disabled = true;
    fold(on);
    marks.set(f, on).then(() => { box.disabled = false; }, (err) => {
      box.disabled = false;
      box.checked = !on;
      fold(!on);
      marks.fail(err);
    });
  } });
  fold(box.checked);
  return checkbox(box, "Viewed");
}

// diffCard is d's i-th file's change (Diff card): its path, what it adds and removes, Viewed with marks, and a menu to
// its file, over its hunks, laid out as layout says, or why it shows none; threads draws the threads on its lines under
// them.
function diffCard(f, i, at, d, drafting, threads, layout, marks) {
  const file = f.status === "deleted" ? (d.base ? at.blob(d.base, f.path) : null) : at.blob(d.head, f.path);
  const more = h("button", { class: "icon-btn icon-btn-ghost icon-btn-sm", type: "button", "aria-label": "More for " + f.path, title: "More", "aria-haspopup": "menu",
    onclick: () => fileMenu(more, f, file) }, icon("ellipsis"));
  const header = h("div", { class: "diff-card-header" }, icon("file-code"),
    h("span", { class: "diff-card-path", title: pathOf(f) }, pathOf(f)),
    f.binary ? null : [h("span", { class: "diff-added" }, "+" + f.additions), h("span", { class: "diff-removed" }, "−" + f.deletions)],
    h("span", { class: "spacer" }));
  const notes = [];
  if (f.oldMode && f.mode && f.oldMode !== f.mode) notes.push("Mode " + f.oldMode + " → " + f.mode + ".");
  if (f.binary) notes.push("A binary file: its lines don't show.");
  else if (f.truncated) notes.push("Too large to show here: " + plural(f.additions + f.deletions, "line") + " changed.");
  else if (!f.hunks.length && !notes.length) notes.push(f.status === "renamed" ? "Renamed without changes." : f.status === "added" ? "An empty file, added." : "An empty file.");
  const drafter = drafting ? lineDrafter(f, d, drafting) : null;
  // What shows under a line: the threads on it, then the comments drafted on it.
  const below = (l) => [...(threads ? threads.under(anchorOf(f, d, l), at) : []), ...(drafter ? drafter.drafted(l) : [])];
  const lines = () => layout === "split" ? splitLines(f.hunks, drafter, below) : unifiedLines(f.hunks, drafter, below);
  const count = f.hunks.reduce((n, hk) => n + hk.lines.length, 0);
  let body = null;
  if (f.hunks.length && count <= longFile) body = lines();
  else if (f.hunks.length) {
    const show = h("button", { class: "btn btn-sm", type: "button", onclick: () => wait.replaceWith(lines()) }, "Show " + plural(count, "line"));
    const wait = h("div", { class: "diff-note" }, h("span", {}, "A long change, folded."), show);
    body = wait;
  }
  const card = h("section", { class: "card diff-card", id: "file-" + (i + 1), "aria-label": f.path }, header, notes.map((n) => h("p", { class: "diff-note" }, n)), body);
  if (marks) header.append(viewedBox(f, card, marks));
  header.append(more);
  return card;
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

// unifiedLines are hunks in one column: each line under its numbers in the old file and the new, then its sign, and
// below(line) beneath it; with drafter, a line takes a comment.
function unifiedLines(hunks, drafter, below) {
  const rows = hunks.flatMap((hk) => [h("div", { class: "diff-line diff-hunk" }, h("span", { class: "diff-text" }, hunkHead(hk))),
    hk.lines.flatMap((l) => [h("div", { class: "diff-line diff-" + l.kind }, drafter ? drafter.button(l) : null,
      h("span", { class: "diff-num" }, l.old || ""), h("span", { class: "diff-num" }, l.new || ""), h("span", { class: "diff-sign" }, signs[l.kind]), lineText(l)),
    below(l)])]);
  return h("div", { class: "diff-lines" }, h("div", { class: "diff-rows" }, rows));
}

// splitLines are hunks with the old beside the new: context on both sides, and each run of removed lines beside the
// run of added lines after it, the shorter side padded with filler; each half repeats the hunk's header, and what
// shows below a line spans both. With drafter, a removed line takes a comment on the old side and every other line on
// the new.
function splitLines(hunks, drafter, below) {
  const half = (l, side) => l ? h("div", { class: "diff-half diff-" + l.kind }, drafter && (side === "old") === (l.kind === "removed") ? drafter.button(l) : null,
    h("span", { class: "diff-num" }, (side === "old" ? l.old : l.new) || ""), h("span", { class: "diff-sign" }, signs[l.kind]), lineText(l))
    : h("div", { class: "diff-half diff-filler" });
  const row = (old, now) => [h("div", { class: "diff-split-row" }, half(old, "old"), half(now, "new")),
    old && old.kind === "removed" ? below(old) : [], now ? below(now) : []];
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

// anchorOf is where line l of file f of diff d is: an added or unchanged line at its number in d's head, a removed one
// at its number in d's base, where a reviewer saw it.
const anchorOf = (f, d, l) => l.kind === "removed" ? { sha: d.base, path: f.oldPath || f.path, startLine: l.old, endLine: l.old }
  : { sha: d.head, path: f.path, startLine: l.new, endLine: l.new };

// lineDrafter drafts comments on the lines of file f of diff d into drafting's, each anchored where its line is.
function lineDrafter(f, d, drafting) {
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
      const a = anchorOf(f, d, l);
      return h("button", { class: "diff-comment", type: "button", title: "Comment on this line", "aria-label": "Comment on line " + a.startLine + " of " + a.path,
        onclick: (e) => open(e.currentTarget.closest(".diff-line, .diff-split-row"), a) }, icon("plus"));
    },
    drafted: (l) => drafting.comments.filter((c) => same(c.anchor, anchorOf(f, d, l))).map(drafted),
  };
}
