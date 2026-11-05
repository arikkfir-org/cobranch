import { api, apiPage } from "./api.js";
import { orgPages } from "./assets.js";
import { ask } from "./dialog.js";
import { h } from "./dom.js";
import { ago, shortSha, whenExactly } from "./format.js";
import { avatar, icon } from "./icons.js";
import { popover } from "./menu.js";
import { agents, base, me } from "./state.js";

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

// kinds and formats name an artifact's kind and the format its versions are written in, as its card shows them.
const kinds = { document: "Document", diagram: "Diagram", report: "Report", page: "Page" };
const formats = { "text/markdown": "Markdown", "image/svg+xml": "SVG", "text/html": "HTML" };
// audienceOf says who reads an artifact, beside its format on its card: only its owner, whose conversation's
// artifacts stay theirs until they share them; someone who shared it; or its repository's or organization's readers.
const audienceOf = (a) => a.audience === "organization" ? "everyone in the organization"
  : a.audience === "repository" ? "members of " + (a.repository ? a.repository.slug : "the repository")
    : a.owner && a.owner.id === me.id ? "only you" : "shared with you";

// branchSubject is a branch's conversation, as the pane shows it on the branch's pages and those of its commits,
// files and runs (lib/pane.js): its chat, shared with everyone who reads the repository, agents included, to which a
// viewer with messages.post posts, and which one with messages.moderate moderates. A branch that isn't live any more
// keeps its chat as it was, to be read; the pane asks whether it is only when it opens.
export function branchSubject(org, repo, path, at, name, can) {
  const chat = path + "/branches/" + encodeURIComponent(name) + "/chat";
  return { key: "branch:" + org.slug + "/" + repo.slug + "/" + name, title: name, code: true, scope: "Shared with everyone on the branch", chat, at,
    post: can("messages.post"), moderate: can("messages.moderate"), placeholder: "Message the branch",
    none: "Start a thread here, or on lines of a file from its page on this branch.",
    gone: () => api("GET", path + "/branches/" + encodeURIComponent(name)).then(() => false, (err) => err.status === 404 ? true : Promise.reject(err)) };
}

// issueSubject is an issue's conversation, its comments: shared with everyone who may see the issue, agents
// included, to which a viewer with issues.write posts, and which one with issues.manage moderates.
export const issueSubject = (org, number, identifier, can) => ({
  key: "issue:" + org.slug + "/" + number, title: identifier, code: true, scope: "Shared with everyone who can see the issue",
  chat: "/orgs/" + org.slug + "/issues/" + number + "/chat", post: can("issues.write"), moderate: can("issues.manage"),
  placeholder: "Comment on the issue", none: "Comments on the issue show here, to everyone who can see it.",
});

// privateSubject is the viewer's own conversation about an organization or one of its repositories, which every page of
// it that names no other has: private to them, where a viewer who may ask the organization's agents (can's
// agents.assign) brings one in by its tag.
export const privateSubject = (key, title, code, chat, can) => ({
  key, title, code, chat, post: true, moderate: true, ask: can("agents.assign"),
  scope: can("agents.assign") ? "Private to you · tag an agent to bring it in" : "Private to you", placeholder: "Message " + title,
  none: can("agents.assign") ? "Ask a question, or @mention an agent to give it work here." : "Write a note or a question; only you read it.",
});

// agentSubject is the viewer's conversation with one of the organization's agents, on the agent's page: private to
// them, where every thread they start asks the agent, which takes agents.assign; the agent's task there sees its tasks
// and its queue. A viewer who may not ask it reads the conversation and replies in its threads.
export const agentSubject = (org, agent, can) => ({
  key: "agent:" + org.slug + "/" + agent.id, title: agent.name, scope: "Private to you · every message asks " + agent.name,
  chat: "/orgs/" + org.slug + "/agents/" + agent.id + "/conversation", post: true, moderate: true, ask: can("agents.assign"), asks: agent,
  placeholder: "Message " + agent.name,
  none: can("agents.assign") ? "Ask " + agent.name + " for work, or about its tasks and queue." : "Asking " + agent.name + " takes the Assign tasks permission.",
});

// conversation draws a subject's conversation for the pane (lib/pane.js), which it follows live until signal stops it:
// its threads, oldest at the top, each perhaps on lines of a file at a commit, with their messages in Markdown, to
// which a viewer who may post (s.post) replies, and which one who moderates (s.moderate) resolves and deletes; below
// them the composer, which starts a thread. It reads the conversation's updates as they come, each read waiting up to
// 15 seconds for one; a conversation that stays as it was (s.gone) shows to be read, and nothing follows it. A viewer
// who may ask the organization's agents (s.ask) picks one with Ask, in the composer and in a reply, and the message
// then gives the agent a task in its thread; in an agent's conversation (s.asks) every thread asks the agent.
export async function conversation(s, signal) {
  const chat = s.chat;
  const [page, gone] = await Promise.all([apiPage(chat).catch((err) => err.status === 404 ? null : Promise.reject(err)),
    typeof s.gone === "function" ? s.gone() : !!s.gone]);
  if (!page) return h("p", { class: "app-pane-none" }, "This conversation is gone.");
  const post = s.post && !gone, moderate = s.moderate && !gone;
  const error = h("p", { class: "alert", hidden: true });
  const fail = (err) => { error.textContent = err.message; error.hidden = false; };
  const live = h("div", { class: "banner banner-warning app-pane-live", role: "status", hidden: true }, icon("triangle-alert"), h("div", { class: "banner-text" }, h("p", {})));
  const list = h("div", { class: "app-pane-threads" });
  const empty = gone ? goneState(s) : emptyState(s);
  const more = h("button", { class: "btn btn-ghost btn-sm", type: "button", hidden: true }, "Older threads");
  const messages = h("div", { class: "app-pane-messages" }, more, empty, list);
  const shown = new Map(); // each thread's view, by its ID: the thread as last read, its element, and its messages'
  const org = chat.split("/")[2];
  // atBottom says whether the viewer reads the newest messages, which the list then keeps in view as more come.
  const atBottom = () => messages.scrollHeight - messages.scrollTop - messages.clientHeight < 24;
  const keepDown = (was) => { if (was) messages.scrollTop = messages.scrollHeight; };
  let roster = null; // the organization's agents, read once Ask first opens them
  const agentsToAsk = () => roster || (roster = api("GET", "/orgs/" + org + "/agents").then((d) => d.agents, (err) => { roster = null; throw err; }));

  const artifacts = new Map(); // the artifacts the thread's cards name, by ID, read once each

  // artifactCard is the card of an artifact a message names (Figma "Pane · Artifact card"), under the message across
  // its row: its title, then its format and who reads it, which the artifact itself answers, and Open to its page. Anyone who reads the thread
  // sees the card, but only those its grants let in read the artifact, so for the others it shows the kind alone and
  // opens nothing.
  const artifactCard = (a) => {
    const meta = h("span", { class: "artifact-card-meta" }, kinds[a.kind] || "Artifact");
    const actions = h("div", { class: "artifact-card-actions" });
    const card = h("div", { class: "artifact-card" }, icon("file"),
      h("div", { class: "artifact-card-text" }, h("span", { class: "artifact-card-name", title: a.title }, a.title), meta), actions);
    if (!artifacts.has(a.id)) artifacts.set(a.id, api("GET", "/orgs/" + org + "/artifacts/" + a.id).catch(() => null));
    artifacts.get(a.id).then((full) => {
      if (!full) return;
      meta.textContent = [formats[full.mediaType], audienceOf(full)].filter(Boolean).join(" · ");
      if (orgPages.has("artifacts")) actions.append(h("a", { class: "btn btn-sm", href: base + org + "/artifacts/" + a.id, "data-nav": "" }, "Open"));
    });
    return card;
  };

  // asker picks the agent a message asks, beside its text: Ask opens the organization's agents in a menu above form,
  // as typing @ at the start of a word does, which then writes the agent's name after it. The agent picked shows as
  // its tag at the start of form, in Ask's place, whose × asks nobody again, and the text's placeholder asks it.
  const asker = (form, text) => {
    const placeholder = text.placeholder;
    let picked = null;
    const ask = h("button", { class: "app-pane-ask", type: "button", "aria-haspopup": "menu", title: "Ask one of the organization's agents" }, icon("sparkles"), h("span", {}, "Ask"));
    const tag = h("span", { class: "tag tone-ai app-pane-asked", hidden: true });
    const pick = (a) => {
      picked = a;
      ask.hidden = !!a;
      tag.hidden = !a;
      text.placeholder = a ? "Ask " + a.name : placeholder;
      tag.replaceChildren(...(a ? [icon("sparkles"), h("span", {}, a.name), h("button", { type: "button", "aria-label": "Ask nobody", title: "Ask nobody",
        onclick: () => { pick(null); text.focus(); } }, icon("x"))] : []));
    };
    const menu = (list, mention) => (close) => [h("div", { class: "app-ask-menu-heading", "aria-hidden": "true" }, "Ask an agent"),
      list.length ? list.map((a) => h("button", { class: "menu-item app-ask-menu-item", type: "button", role: "menuitem", onclick: () => {
        close();
        if (mention) text.setRangeText(a.name + " ", text.selectionStart, text.selectionEnd, "end");
        pick(a);
        text.focus();
      } }, avatar(a.name, true, 24), h("span", { class: "app-ask-menu-text" }, h("strong", {}, a.name), a.description ? h("span", {}, a.description) : null)))
        : h("p", { class: "app-ask-menu-none" }, "The organization has no agents yet.")];
    const open = (mention) => agentsToAsk().then((list) => {
      const el = popover(form, "above", menu(list, mention), "app-ask-menu");
      if (el) el.style.width = form.offsetWidth + "px";
    }).catch(fail);
    ask.addEventListener("click", () => open(false));
    text.addEventListener("input", (e) => {
      if (e.inputType === "insertText" && e.data === "@" && /(^|\s)@$/.test(text.value.slice(0, text.selectionStart))) open(true);
    });
    return { tag, ask, agent: () => picked, clear: () => pick(null) };
  };
  // asking is what a message's input adds for the agent it asks, if any.
  const asking = (picker) => (picker && picker.agent() ? { ask: picker.agent().id } : {});

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
    const agent = !!m.taskNumber || (m.author && agents.has(m.author.id));
    const target = chat + "/threads/" + v.t.id + "/messages/" + m.id;
    const content = m.deletedAt ? h("p", { class: "muted" }, "Deleted" + (m.deletedBy ? " by " + m.deletedBy.displayName : "") + ", " + whenExactly(m.deletedAt) + ".")
      : m.html ? h("div", { class: "chat-message-body" }, messageBody(m.html)) : h("p", { class: "chat-message-body chat-message-text" }, m.body);
    const edit = () => {
      const entry = v.messages.get(m.id);
      entry.editing = true;
      const text = h("textarea", { class: "textarea", required: true, maxlength: 20000, "aria-label": "Message" });
      text.value = m.body;
      const save = h("button", { class: "btn btn-primary btn-sm", type: "submit" }, "Save");
      const done = (latest) => {
        entry.editing = false;
        entry.pending = null;
        if (latest) message(v, latest);
        else form.replaceWith(content);
      };
      const form = h("form", { class: "app-pane-form", onsubmit: (e) => {
        e.preventDefault();
        save.disabled = true;
        api("PATCH", target, { body: text.value }, { "If-Match": `"${m.version}"` }).then(done).catch((err) => { save.disabled = false; fail(err); });
      } }, text, h("div", { class: "app-pane-form-actions" }, h("button", { class: "btn btn-ghost btn-sm", type: "button", onclick: () => done(entry.pending) }, "Cancel"), save));
      content.replaceWith(form);
      text.focus();
    };
    const remove = () => ask(mine ? "Delete your message?" : "Delete " + (m.author ? m.author.displayName + "'s" : "this") + " message?",
      "It shows as deleted to everyone in the conversation. This can't be undone.", "Delete",
      () => api("DELETE", target, undefined, { "If-Match": `"${m.version}"` }).then(() => api("GET", target)).then((gone) => message(v, gone)));
    const act = (glyph, label, onclick) => h("button", { class: "icon-btn icon-btn-ghost icon-btn-sm", type: "button", "aria-label": label, title: label, onclick }, icon(glyph));
    const actions = m.deletedAt ? [] : [mine && post ? act("pencil", "Edit", edit) : null, (mine && post) || moderate ? act("x", "Delete", remove) : null].filter(Boolean);
    const name = m.author ? m.author.displayName : "Someone since gone";
    const tag = !agent ? null : m.taskNumber ? h("a", { class: "tag tone-ai", href: base + org + "/tasks/" + m.taskNumber, "data-nav": "",
      title: "An agent posted it, working task #" + m.taskNumber + "." }, icon("sparkles"), "Agent") : h("span", { class: "tag tone-ai" }, icon("sparkles"), "Agent");
    // A message that asked an agent names it, and links the task it asked for.
    const asked = m.askedTaskNumber ? h("p", { class: "chat-message-asked" }, icon("sparkles"), h("span", {}, "Asked " + (m.askedAgent ? m.askedAgent.displayName : "an agent since gone")),
      "·", h("a", { href: base + org + "/tasks/" + m.askedTaskNumber, "data-nav": "" }, "#" + m.askedTaskNumber)) : null;
    return h("div", { class: "chat-message", id: "message-" + m.id }, avatar(name, agent, 32),
      h("div", { class: "chat-message-text-column" },
        h("div", { class: "chat-message-header" }, h("strong", {}, name), tag,
          h("time", { datetime: m.createdAt, title: whenExactly(m.createdAt) }, ago(m.createdAt)),
          m.editedAt ? h("span", { title: "Edited " + whenExactly(m.editedAt) }, "edited") : null,
          actions.length ? h("span", { class: "chat-message-actions" }, actions) : null),
        content, asked),
      m.artifact && !m.deletedAt ? artifactCard(m.artifact) : null);
  };
  // head is a thread's anchor, when it has one: the lines it is on, whether a review opened it, and whether it is
  // resolved or the branch has moved on from them, which a line under it explains.
  const head = (t) => {
    const a = t.anchor;
    if (!a && !t.reviewId && !t.outdated && !t.resolvedAt) return null;
    const state = t.resolvedAt ? h("span", { class: "badge-quiet tone-success" }, "Resolved") : t.outdated ? h("span", { class: "badge-quiet tone-warning" }, "Outdated") : null;
    return h("div", { class: "app-thread-head" },
      h("div", { class: "app-thread-anchor" },
        a ? h("a", { class: "app-thread-lines", href: s.at.blob(a.sha, a.path) + linesAnchor(a.startLine, a.endLine), "data-nav": "", title: "At " + shortSha(a.sha) },
          icon("file"), h("code", {}, lineLabel(a))) : h("span", { class: "app-thread-lines" }, icon("message-square"), h("span", {}, "Thread")),
        t.reviewId ? h("span", { class: "tag tone-neutral", title: "A comment of a review, which opened this thread." }, "review") : null, state),
      t.outdated && a && !t.resolvedAt ? h("p", { class: "app-thread-note" }, "The lines changed after " + shortSha(a.sha) + "; this thread is about that version.") : null);
  };
  // resolvedRow stands for a resolved thread's messages, which Show opens: who resolved it, when, and how many it has.
  const resolvedRow = (v) => {
    const t = v.t;
    const show = h("button", { class: "btn btn-ghost btn-sm", type: "button", "aria-expanded": String(!!v.open), onclick: () => { v.open = !v.open; fold(v); } }, v.open ? "Hide" : "Show");
    return h("div", { class: "app-thread-resolved" }, icon("circle-check"),
      h("span", {}, ["Resolved" + (t.resolvedBy ? " by " + t.resolvedBy.displayName : ""), ago(t.resolvedAt), v.messages.size === 1 ? "1 message" : v.messages.size + " messages"].join(" · ")), show);
  };
  // fold shows a resolved thread as its row until the viewer opens it, and an open thread whole.
  const fold = (v) => {
    const folded = !!v.t.resolvedAt && !v.open;
    const row = v.t.resolvedAt ? resolvedRow(v) : h("span", { hidden: true });
    v.row.replaceWith(row);
    v.row = row;
    v.list.hidden = folded;
    v.foot.hidden = folded;
  };
  // foot is a thread's actions: replying, for a viewer who may post, and resolving or reopening it, for its starter
  // or a moderator.
  const foot = (v) => {
    const t = v.t;
    const may = moderate || (post && t.startedBy && t.startedBy.id === me.id);
    const toggle = (e) => {
      e.target.disabled = true;
      api("PATCH", chat + "/threads/" + t.id, { resolved: !t.resolvedAt }, { "If-Match": `"${t.version}"` }).then((changed) => place(changed))
        .catch((err) => { e.target.disabled = false; fail(err); });
    };
    if (!post && !may) return null;
    return h("div", { class: "app-thread-foot" }, post ? h("button", { class: "btn btn-ghost btn-sm", type: "button", onclick: () => replying(v) }, "Reply") : null,
      may ? h("button", { class: "btn btn-ghost btn-sm", type: "button", onclick: toggle }, t.resolvedAt ? "Reopen" : "Resolve") : null);
  };
  // replying opens a thread's reply under its actions: a box like the composer, where Enter sends and Escape closes it,
  // with Ask for a viewer who may ask an agent, or a Reply button.
  const replying = (v) => {
    if (v.reply) return v.reply.querySelector("textarea").focus();
    const close = () => { v.reply.remove(); v.reply = null; };
    v.reply = composerBox("Reply in Markdown", "Reply", { ask: s.ask, button: "Reply", cancel: close }, (text, picker, done) => {
      const was = atBottom();
      return api("POST", chat + "/threads/" + v.t.id + "/messages", { body: text.value, ...asking(picker) }).then((m) => { close(); message(v, m); keepDown(was); }).finally(done);
    });
    v.reply.classList.add("app-pane-reply");
    v.el.append(v.reply);
    v.reply.querySelector("textarea").focus();
  };
  // composerBox is a form that sends one message: its text, which grows with what is written, Enter sending it and
  // Shift+Enter starting a new line, Escape calling o.cancel when given; at its end Ask, when o.ask, or else the
  // button o.button names, if any. submit(text, picker, done) sends it, calling done once it is through.
  const composerBox = (placeholder, label, o, submit) => {
    const { ask, button, cancel } = o;
    const text = h("textarea", { name: "body", required: true, maxlength: 20000, rows: 1, placeholder, "aria-label": label });
    const grow = () => { text.style.height = "auto"; text.style.height = Math.min(text.scrollHeight, 200) + "px"; };
    const form = h("form", { class: "app-pane-composer" });
    const picker = ask ? asker(form, text) : null;
    const send = picker || !button ? null : h("button", { class: "btn btn-primary btn-sm", type: "submit", title: button + " (Enter), in Markdown; Shift+Enter starts a new line" }, button);
    let busy = false;
    text.addEventListener("input", grow);
    text.addEventListener("keydown", (e) => {
      if (e.key === "Enter" && !e.shiftKey && !e.isComposing) { e.preventDefault(); form.requestSubmit(); }
      if (e.key === "Escape" && cancel) cancel();
    });
    form.addEventListener("submit", (e) => {
      e.preventDefault();
      if (busy || !text.value.trim()) return;
      busy = true;
      if (send) send.disabled = true;
      submit(text, picker, () => { busy = false; if (send) send.disabled = false; }).then(() => { text.value = ""; grow(); if (picker) picker.clear(); }, fail);
    });
    form.append(...[picker && picker.tag, text, picker ? picker.ask : send].filter(Boolean));
    return form;
  };
  // place draws a thread, or draws it again as it is now, with the messages it comes with: one of an older page at the
  // top, and a new one at the bottom, by the composer.
  const place = (t, older) => {
    let v = shown.get(t.id);
    if (!v) {
      v = { t, list: h("div", { class: "app-thread-messages" }), messages: new Map(), reply: null, open: false, row: h("span", { hidden: true }) };
      v.head = head(t) || h("span", { hidden: true });
      v.foot = foot(v) || h("span", { hidden: true });
      v.el = h("article", { class: "app-thread", id: "thread-" + t.id }, v.head, v.row, v.list, v.foot);
      shown.set(t.id, v);
      if (older) list.prepend(v.el);
      else list.append(v.el);
    } else if (t.version >= v.t.version) {
      v.t = t;
      const again = head(t) || h("span", { hidden: true });
      v.head.replaceWith(again);
      v.head = again;
      const after = foot(v) || h("span", { hidden: true });
      v.foot.replaceWith(after);
      v.foot = after;
    }
    (t.messages || []).forEach((m) => message(v, m));
    if (location.hash === "#thread-" + t.id) v.open = true;
    fold(v);
    empty.hidden = shown.size > 0;
  };
  // An update to a thread of a page not read yet shows when that page is.
  const apply = (u) => {
    if (!shown.has(u.thread.id) && u.kind !== "thread.started") return;
    const was = atBottom();
    place(u.thread);
    if (u.message) message(shown.get(u.thread.id), u.message);
    fold(shown.get(u.thread.id));
    keepDown(was);
  };
  const show = (p) => {
    const height = messages.scrollHeight;
    p.data.threads.forEach((t) => place(t, true));
    messages.scrollTop += messages.scrollHeight - height;
    more.hidden = !p.next;
    more.onclick = () => { more.disabled = true; apiPage(p.next).then(show).catch(fail).finally(() => { more.disabled = false; }); };
  };
  show(page);
  empty.hidden = shown.size > 0;

  let composer = gone ? h("div", { class: "app-pane-composer", "aria-disabled": "true" },
    h("textarea", { rows: 1, disabled: true, placeholder: "Read-only: the branch is gone", "aria-label": "New thread" })) : null;
  // The composer starts a thread; in an agent's conversation every thread asks the agent, which a viewer who may not
  // ask it can't start.
  if (post && (!s.asks || s.ask)) {
    const placeholder = (s.placeholder || "Message, in Markdown") + (s.ask && !s.asks ? ", or @mention an agent" : "");
    composer = composerBox(placeholder, "New thread", { ask: s.ask && !s.asks, button: s.asks ? null : "Send" }, (text, picker, done) =>
      api("POST", chat + "/threads", { body: text.value, ...asking(picker) }).then((t) => { place(t); keepDown(true); }).finally(done));
  }
  const el = h("div", { class: "app-pane-conversation" }, error, gone && shown.size ? goneBanner(s) : null, gone ? null : live, messages, composer);
  if (!gone) followChat(chat, page.data.after, apply, live, signal);
  // The newest messages show first, or the thread a link names.
  setTimeout(() => {
    const linked = location.hash.startsWith("#thread-") && document.getElementById(location.hash.slice(1));
    if (linked && el.contains(linked)) linked.scrollIntoView({ block: "start" });
    else messages.scrollTop = messages.scrollHeight;
  }, 0);
  return el;
}

// emptyState is what the pane shows while a subject has no conversation, or its conversation no threads yet: B's Empty
// state, naming the subject, with what would start one.
export const emptyState = (s) => h("div", { class: "empty app-pane-empty" }, h("div", { class: "empty-icon" }, icon("sparkles")),
  h("h2", { class: "empty-title" }, s.chat && s.title ? ["There's no conversation " + (s.asks ? "with " : "about "), h(s.code ? "code" : "span", {}, s.title), " yet"]
    : "There's no conversation here yet"),
  h("p", { class: "empty-description" }, s.chat ? s.none : "The Console's own pages have none; every page of an organization has one."));

// goneState is what the pane shows of a deleted branch's conversation that had no threads; goneBanner tops one that
// had: the branch is gone, and its conversation stays, to be read.
const goneState = (s) => h("div", { class: "empty app-pane-empty" }, h("div", { class: "empty-icon" }, icon("circle-x")),
  h("h2", { class: "empty-title" }, "This branch was deleted"), h("p", { class: "empty-description" }, h("code", {}, s.title), " was deleted. Its conversation stays here, read-only."));
const goneBanner = (s) => h("div", { class: "banner app-pane-live", role: "status" }, icon("info"),
  h("div", { class: "banner-text" }, h("p", {}, h("code", {}, s.title), " was deleted. Its conversation stays here, read-only.")));

// followChat applies a chat's updates after cursor after as they come, until signal stops it (the pane stops it when it
// closes, shows another conversation or a card replaces the shell) or the chat is gone; its banner, live, says when it doesn't follow, and it tries again,
// waiting longer each time, when a read fails.
async function followChat(chat, after, apply, live, signal) {
  let delay = 1000;
  while (!signal.aborted) {
    try {
      const d = await api("GET", chat + "/updates?" + new URLSearchParams({ after, wait: 15 }), undefined, {}, signal);
      d.updates.forEach(apply);
      after = d.after;
      delay = 1000;
      live.hidden = true;
    } catch (err) {
      if (signal.aborted) return;
      live.hidden = false;
      if (err.status === 404) return void (live.querySelector("p").textContent = "This conversation is gone.");
      live.querySelector("p").textContent = "Reconnecting…";
      await new Promise((resolve) => setTimeout(resolve, delay));
      delay = Math.min(delay * 2, 60000);
    }
  }
}
