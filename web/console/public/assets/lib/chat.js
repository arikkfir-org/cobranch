import { api, apiPage } from "./api.js";
import { h } from "./dom.js";
import { shortSha, whenExactly } from "./format.js";
import { base, follows, me } from "./state.js";

// lineLabel names lines of a file a thread is on: its path, then its line or its first and last.
export const lineLabel = (a) => a.path + ":" + a.startLine + (a.endLine > a.startLine ? "–" + a.endLine : "");
// linesAnchor is the anchor of a file's page that marks lines: #L3, or #L3-L5.
export const linesAnchor = (start, end) => "#L" + start + (end > start ? "-L" + end : "");

// The elements a chat message's HTML keeps, of those the API's sanitizer lets through: messageBody copies these
// alone, with the few attributes Markdown writes, into the page's own elements, so a message draws as written and
// runs nothing.
const messageTags = new Set(["P", "BR", "HR", "EM", "STRONG", "DEL", "S", "CODE", "PRE", "BLOCKQUOTE", "UL", "OL", "LI", "H1", "H2", "H3", "H4", "H5",
  "H6", "TABLE", "THEAD", "TBODY", "TR", "TH", "TD", "A", "IMG", "DETAILS", "SUMMARY", "INPUT", "SUP", "SUB", "SPAN", "DIV"]);

// messageBody is a chat message's rendered HTML as the page's elements: links to the Console's pages stay in the
// Console, other links open in a new tab, and only web addresses are followed or shown.
export function messageBody(html) {
  const copy = (node) => {
    if (node.nodeType === Node.TEXT_NODE) return document.createTextNode(node.data);
    if (node.nodeType !== Node.ELEMENT_NODE) return null;
    const children = [...node.childNodes].map(copy);
    if (!messageTags.has(node.tagName)) return children;
    const el = document.createElement(node.tagName);
    const web = (name) => { try { const u = new URL(node.getAttribute(name) || "", location.href); return /^https?:$/.test(u.protocol) ? u : null; } catch { return null; } };
    const tag = node.tagName, u = tag === "A" ? web("href") : tag === "IMG" ? web("src") : null;
    if (tag === "A" && u && u.origin === location.origin && u.pathname.startsWith(base)) {
      el.setAttribute("data-nav", "");
      el.setAttribute("href", u.pathname + u.search + u.hash);
    } else if (tag === "A" && u) Object.assign(el, { href: u.href, target: "_blank", rel: "noopener noreferrer" });
    else if (tag === "IMG") Object.assign(el, { src: u ? u.href : "", alt: node.getAttribute("alt") || "", loading: "lazy" });
    else if (tag === "INPUT" && node.getAttribute("type") !== "checkbox") return null;
    else if (tag === "INPUT") Object.assign(el, { type: "checkbox", checked: node.hasAttribute("checked"), disabled: true });
    else if (tag === "CODE" && /^language-[\w+#.-]+$/.test(node.className)) el.className = node.className;
    else if ((tag === "TD" || tag === "TH") && /^(left|right|center)$/.test(node.getAttribute("align") || "")) el.style.textAlign = node.getAttribute("align");
    el.append(...children.flat(Infinity).filter(Boolean));
    return el;
  };
  return [...new DOMParser().parseFromString(html, "text/html").body.childNodes].map(copy).flat(Infinity).filter(Boolean);
}

// branchChat is a branch's chat, which it follows live: its threads, newest first, each perhaps on lines of a file at
// a commit, with their messages in Markdown, to which a viewer with messages.post replies. A deleted branch's chat
// (b.deletedAt) stays as it was: it shows to be read, and nothing follows it.
export function branchChat(path, at, b, can) {
  const gone = !!b.deletedAt;
  return chatPanel(path + "/branches/" + encodeURIComponent(b.name) + "/chat", {
    title: "Chat", at, gone, post: !gone && can("messages.post"), moderate: !gone && can("messages.moderate"),
    none: gone ? "It had no threads." : "No threads yet.", hint: " Start one here, or on lines of a file from its page on this branch.",
    missing: "This branch has no chat any more.", unanchored: "On the branch", placeholder: "Start a thread on this branch, in Markdown",
    start: "Start thread", frozen: "As it was when the branch was deleted",
  });
}

// issueChat is an issue's comments, its chat, which it follows live: threads of messages in Markdown, newest first,
// which a viewer with issues.write starts and replies to, and one with issues.manage moderates.
export function issueChat(path, can) {
  return chatPanel(path + "/chat", {
    title: "Comments", post: can("issues.write"), moderate: can("issues.manage"), none: "No comments yet.", hint: "",
    missing: "This issue has no comments any more.", placeholder: "Comment in Markdown", start: "Comment",
  });
}

// chatPanel is the chat at path, a branch's or an issue's, as o words it, which it follows live: its threads, newest
// first, with their messages in Markdown, to which a viewer who may post (o.post) replies, and which one who
// moderates (o.moderate) resolves and deletes. It reads the chat's updates as they come, each read waiting up to 15
// seconds for one, until the Console draws another page; a chat that stays as it was (o.gone) shows to be read, and
// nothing follows it.
async function chatPanel(chat, o) {
  const { gone, post, moderate } = o;
  const error = h("p", { class: "alert", hidden: true });
  const fail = (err) => { error.textContent = err.message; error.hidden = false; };
  const live = h("span", { class: "muted" });
  const list = h("div", { class: "threads" });
  const empty = h("p", {}, o.none, post ? o.hint : "");
  const more = h("button", { class: "button small", type: "button", hidden: true }, "Older threads");
  const shown = new Map(); // each thread's view, by its ID: the thread as last read, its element, and its messages'
  let page;
  try {
    page = await apiPage(chat);
  } catch (err) {
    if (err.status !== 404) throw err;
    return h("section", { class: "panel" }, h("h3", {}, o.title), h("p", {}, o.missing));
  }

  // message draws one of a thread's messages, or draws it again as it is now, unless its author is editing it.
  const message = (v, m) => {
    const old = v.messages.get(m.id);
    if (old && old.m.version > m.version) return;
    if (old && old.editing) return void (old.pending = m);
    const el = messageView(v, m);
    if (old) old.el.replaceWith(el);
    else v.list.append(el);
    v.messages.set(m.id, { m, el });
  };
  const messageView = (v, m) => {
    const mine = m.author && m.author.id === me.id;
    const target = chat + "/threads/" + v.t.id + "/messages/" + m.id;
    const content = m.deletedAt ? h("p", { class: "muted" }, "Deleted" + (m.deletedBy ? " by " + m.deletedBy.displayName : "") + ", " + whenExactly(m.deletedAt) + ".")
      : m.html ? h("div", { class: "body" }, messageBody(m.html)) : h("p", { class: "text" }, m.body);
    const edit = () => {
      const entry = v.messages.get(m.id);
      entry.editing = true;
      const text = h("textarea", { required: true, maxlength: 20000, "aria-label": "Message" });
      text.value = m.body;
      const save = h("button", { class: "button small primary", type: "submit" }, "Save");
      const done = (latest) => {
        entry.editing = false;
        entry.pending = null;
        if (latest) message(v, latest);
        else form.replaceWith(content);
      };
      const form = h("form", { onsubmit: (e) => {
        e.preventDefault();
        save.disabled = true;
        api("PATCH", target, { body: text.value }, { "If-Match": `"${m.version}"` }).then(done).catch((err) => { save.disabled = false; fail(err); });
      } }, text, h("div", { class: "row" }, h("span", { class: "spacer" }),
        h("button", { class: "button small", type: "button", onclick: () => done(entry.pending) }, "Cancel"), save));
      content.replaceWith(form);
      text.focus();
    };
    const remove = (e) => {
      if (!confirm(mine ? "Delete your message?" : "Delete " + (m.author ? m.author.displayName + "'s" : "this") + " message?")) return;
      e.target.disabled = true;
      api("DELETE", target, undefined, { "If-Match": `"${m.version}"` }).then(() => api("GET", target)).then((gone) => message(v, gone))
        .catch((err) => { e.target.disabled = false; fail(err); });
    };
    const actions = m.deletedAt ? [] : [mine && post ? h("button", { class: "button small", type: "button", onclick: edit }, "Edit") : null,
      (mine && post) || moderate ? h("button", { class: "button small danger", type: "button", onclick: remove }, "Delete") : null].filter(Boolean);
    return h("div", { class: "message", id: "message-" + m.id },
      h("div", { class: "meta" }, h("strong", {}, m.author ? m.author.displayName : "Someone since gone"),
        m.taskNumber ? h("a", { class: "chip", href: base + chat.split("/")[2] + "/tasks/" + m.taskNumber, "data-nav": "",
          title: "An agent posted it, working this task." }, "agent · task #" + m.taskNumber) : null,
        h("time", { datetime: m.createdAt }, whenExactly(m.createdAt)),
        m.editedAt ? h("span", { title: "Edited " + whenExactly(m.editedAt) }, "edited") : null, actions.length ? h("span", { class: "actions" }, actions) : null),
      content);
  };
  // header is a thread's line: the lines it is on, whether the branch has moved on from them, and whether it is
  // resolved, with a switch for its starter or a moderator. An issue's threads are on no lines.
  const header = (t) => {
    const a = t.anchor;
    const may = moderate || (post && t.startedBy && t.startedBy.id === me.id);
    const toggle = (e) => {
      e.target.disabled = true;
      api("PATCH", chat + "/threads/" + t.id, { resolved: !t.resolvedAt }, { "If-Match": `"${t.version}"` }).then((changed) => place(changed))
        .catch((err) => { e.target.disabled = false; fail(err); });
    };
    return h("div", { class: "row" },
      a ? [h("a", { href: o.at.blob(a.sha, a.path) + linesAnchor(a.startLine, a.endLine), "data-nav": "" }, h("code", {}, lineLabel(a))),
        h("span", { class: "muted" }, "at " + shortSha(a.sha))] : o.unanchored ? h("span", { class: "muted" }, o.unanchored) : null,
      t.reviewId ? h("span", { class: "chip", title: "A comment of a review, which opened this thread." }, "review comment") : null,
      t.outdated ? h("span", { class: "chip", title: "The branch has moved on from the commit these lines are at." }, "outdated") : null,
      t.resolvedAt ? h("span", { class: "chip" }, "Resolved" + (t.resolvedBy ? " by " + t.resolvedBy.displayName : "")) : null,
      h("span", { class: "spacer" }), may ? h("button", { class: "button small", type: "button", onclick: toggle }, t.resolvedAt ? "Reopen" : "Resolve") : null);
  };
  const reply = (v) => {
    const text = h("textarea", { name: "body", required: true, maxlength: 20000, placeholder: "Reply in Markdown", "aria-label": "Reply" });
    const send = h("button", { class: "button small primary", type: "submit" }, "Reply");
    return h("form", { onsubmit: (e) => {
      e.preventDefault();
      send.disabled = true;
      api("POST", chat + "/threads/" + v.t.id + "/messages", { body: text.value }).then((m) => { text.value = ""; message(v, m); })
        .catch(fail).finally(() => { send.disabled = false; });
    } }, text, h("div", { class: "row" }, h("span", { class: "spacer" }), send));
  };
  // place draws a thread, or draws it again as it is now, with the messages it comes with: a new one first, and one
  // of an older page last.
  const place = (t, older) => {
    let v = shown.get(t.id);
    if (!v) {
      v = { t, list: h("div", { class: "messages" }), messages: new Map() };
      v.head = header(t);
      v.el = h("article", { class: "thread", id: "thread-" + t.id }, v.head, v.list, post ? reply(v) : null);
      shown.set(t.id, v);
      if (older) list.append(v.el);
      else list.prepend(v.el);
    } else if (t.version >= v.t.version) {
      v.t = t;
      const head = header(t);
      v.head.replaceWith(head);
      v.head = head;
    }
    v.el.classList.toggle("resolved", !!v.t.resolvedAt);
    (t.messages || []).forEach((m) => message(v, m));
    empty.hidden = shown.size > 0;
  };
  // An update to a thread of a page not read yet shows when that page is.
  const apply = (u) => {
    if (!shown.has(u.thread.id) && u.kind !== "thread.started") return;
    place(u.thread);
    if (u.message) message(shown.get(u.thread.id), u.message);
  };
  const show = (p) => {
    p.data.threads.forEach((t) => place(t, true));
    more.hidden = !p.next;
    more.onclick = () => { more.disabled = true; apiPage(p.next).then(show).catch(fail).finally(() => { more.disabled = false; }); };
  };
  show(page);
  empty.hidden = shown.size > 0;

  let start = null;
  if (post) {
    const text = h("textarea", { name: "body", required: true, maxlength: 20000, placeholder: o.placeholder, "aria-label": "New thread" });
    const button = h("button", { class: "button small primary", type: "submit" }, o.start);
    start = h("form", { onsubmit: (e) => {
      e.preventDefault();
      button.disabled = true;
      api("POST", chat + "/threads", { body: text.value }).then((t) => { text.value = ""; place(t); }).catch(fail).finally(() => { button.disabled = false; });
    } }, text, h("div", { class: "row" }, h("span", { class: "spacer" }), button));
  }
  const panel = h("section", { class: "panel chat" }, h("div", { class: "row" }, h("h3", {}, o.title), live), error, start, empty, list, more);
  if (gone) live.textContent = o.frozen;
  else {
    const stop = new AbortController();
    follows(stop);
    followChat(chat, page.data.after, apply, panel, live, stop.signal);
  }
  // A link to a thread brings it into view once the page is drawn: after the calls that draw it, which take no turn.
  setTimeout(() => {
    const el = location.hash.startsWith("#thread-") && document.getElementById(location.hash.slice(1));
    if (el) el.scrollIntoView({ block: "start" });
  }, 0);
  return panel;
}

// followChat applies a chat's updates after cursor after as they come, until signal stops it, the chat's panel
// leaves the page, or the chat is gone; it says on live whether it follows, and tries again, waiting longer each
// time, when a read fails.
async function followChat(chat, after, apply, panel, live, signal) {
  let delay = 1000;
  while (!signal.aborted) {
    try {
      const d = await api("GET", chat + "/updates?" + new URLSearchParams({ after, wait: 15 }), undefined, {}, signal);
      if (!panel.isConnected) return;
      d.updates.forEach(apply);
      after = d.after;
      delay = 1000;
      live.textContent = "Live";
    } catch (err) {
      if (signal.aborted || !panel.isConnected) return;
      if (err.status === 404) return void (live.textContent = "This chat is gone.");
      live.textContent = "Reconnecting…";
      await new Promise((resolve) => setTimeout(resolve, delay));
      delay = Math.min(delay * 2, 60000);
    }
  }
}
