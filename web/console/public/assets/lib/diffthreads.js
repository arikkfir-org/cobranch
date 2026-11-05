// A branch's chat's threads on the lines of its diff, as Branch · Files changed draws them: each open thread at the
// branch's head, or on a removed line at its base, shows under its line (Comment), its messages oldest first, then
// Reply, Resolve and Open in conversation. The conversation pane shows every thread, those that are resolved or
// outdated included.

import { api, apiPage } from "./api.js";
import { messageBody } from "./chat.js";
import { h } from "./dom.js";
import { ago, whenExactly } from "./format.js";
import { avatar, icon } from "./icons.js";
import { openThread } from "./pane.js";
import { agents, me } from "./state.js";

// pages bounds how many pages of a conversation's threads a diff reads: 1,000 threads.
const pages = 10;

// lineKey names the line an anchor ends on, which its thread shows under.
const lineKey = (a) => a.sha + "\n" + a.path + "\n" + a.endLine;

// lineThreads reads the open threads on lines of conversation c ({ chat, post, moderate }, as the branch's subject has
// them) and answers what draws them: under(anchor) is the views of those on the line anchor names, each drawn once and
// kept, so that a view moves with its line when the diff's layout changes.
export async function lineThreads(c) {
  const byLine = new Map();
  let page = await apiPage(c.chat + "?limit=100");
  for (let n = 1; ; n++) {
    for (const t of page.data.threads || []) {
      if (!t.anchor || t.resolvedAt || t.outdated) continue;
      const key = lineKey(t.anchor);
      byLine.set(key, [...(byLine.get(key) || []), t]);
    }
    if (!page.next || n === pages) break;
    page = await apiPage(page.next);
  }
  const views = new Map();
  const view = (t) => {
    if (!views.has(t.id)) views.set(t.id, threadView(t, c));
    return views.get(t.id);
  };
  return { under: (anchor) => (byLine.get(lineKey(anchor)) || []).map(view) };
}

// threadView is thread t of conversation c under its line: its messages and actions in a Comment, or, once resolved
// here, a row that says so, with Reopen.
function threadView(t, c) {
  const el = h("div", { class: "diff-thread", "data-thread": t.id });
  const target = c.chat + "/threads/" + t.id;
  const error = h("p", { class: "alert", hidden: true });
  const fail = (err) => { error.textContent = err.message; error.hidden = false; };
  let reply = null;
  // Its starter and a moderator resolve it and reopen it.
  const may = () => c.moderate || (c.post && t.startedBy && me && t.startedBy.id === me.id);
  const changed = (next) => {
    t = { ...next, messages: next.messages || t.messages };
    draw();
  };
  const toggle = (e) => {
    e.currentTarget.disabled = true;
    api("PATCH", target, { resolved: !t.resolvedAt }, { "If-Match": `"${t.version}"` }).then(changed, (err) => { draw(); fail(err); });
  };
  const replying = () => {
    if (reply) return reply.querySelector("textarea").focus();
    const first = (t.messages || [])[0];
    const text = h("textarea", { class: "textarea", required: true, maxlength: 20000, "aria-label": "Reply",
      placeholder: first && first.author ? "Reply to " + first.author.displayName + "…" : "Reply…" });
    const send = h("button", { class: "btn btn-sm btn-primary", type: "submit" }, "Reply");
    const close = () => { reply = null; draw(); };
    const submit = (e) => {
      e.preventDefault();
      if (!text.value.trim()) return;
      send.disabled = true;
      api("POST", target + "/messages", { body: text.value }).then((m) => {
        reply = null;
        t = { ...t, messages: [...(t.messages || []), m] };
        draw();
        // A message counts as a change of its thread, which is read again for the version Resolve names; a read that
        // fails leaves the reply shown, posted, and Resolve then asks the viewer to try again.
        api("GET", target).then(changed, () => {});
      }, (err) => { send.disabled = false; fail(err); });
    };
    text.addEventListener("keydown", (e) => { if (e.key === "Escape") close(); });
    reply = h("form", { class: "diff-thread-reply", onsubmit: submit }, text,
      h("div", { class: "draft-comment-actions" }, h("button", { class: "btn btn-sm btn-ghost", type: "button", onclick: close }, "Cancel"), send));
    draw();
    text.focus();
  };
  const draw = () => {
    error.hidden = true;
    if (t.resolvedAt) {
      el.replaceChildren(h("div", { class: "diff-thread-comment diff-thread-resolved" }, icon("circle-check"),
        h("span", {}, "Resolved" + (t.resolvedBy ? " by " + t.resolvedBy.displayName : "")), h("span", { class: "spacer" }),
        may() ? h("button", { class: "btn btn-sm btn-ghost", type: "button", onclick: toggle }, "Reopen") : null), error);
      return;
    }
    const actions = [c.post ? h("button", { class: "btn btn-sm btn-ghost", type: "button", onclick: replying }, "Reply") : null,
      may() ? h("button", { class: "btn btn-sm btn-ghost", type: "button", onclick: toggle }, icon("circle-check"), "Resolve") : null,
      h("button", { class: "btn btn-sm btn-ghost", type: "button", onclick: () => openThread(t.id) }, "Open in conversation")];
    el.replaceChildren(h("div", { class: "diff-thread-comment" }, (t.messages || []).map(messageView),
      reply || h("div", { class: "diff-thread-actions" }, actions)), error);
  };
  draw();
  return el;
}

// messageView is one of a thread's messages (Chat message): its author's avatar, their name, whether an agent wrote
// it and when, over its Markdown.
function messageView(m) {
  const agent = !!m.taskNumber || (!!m.author && agents.has(m.author.id));
  const name = m.author ? m.author.displayName : "Someone since gone";
  const body = m.deletedAt ? h("p", { class: "chat-message-body muted" }, "Deleted" + (m.deletedBy ? " by " + m.deletedBy.displayName : "") + ".")
    : m.html ? h("div", { class: "chat-message-body" }, messageBody(m.html)) : h("p", { class: "chat-message-body chat-message-text" }, m.body);
  return h("div", { class: "diff-thread-message" }, avatar(name, agent, 24),
    h("div", { class: "diff-thread-text" }, h("div", { class: "chat-message-header" }, h("strong", {}, name),
      agent ? h("span", { class: "tag tone-ai" }, icon("sparkles"), "Agent") : null,
      h("time", { datetime: m.createdAt, title: whenExactly(m.createdAt) }, ago(m.createdAt))), body));
}
