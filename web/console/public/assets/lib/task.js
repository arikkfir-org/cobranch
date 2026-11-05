// A task's own page (Agent · Task), at its agent's /<organization>/agents/<agent ID>/tasks/<number>
// (pages/org/agents.js): what it was asked and where it stands, what its agent did step by step, its facts, and what it
// spent against its agent's cap. While the task hasn't ended the page follows it, and Cancel task stops it for its
// assigner and for a viewer holding org.principals.manage.

import { api } from "./api.js";
import { activeTask, agentPage, card, dollars, emptyState, fact, micros, quiet, since, stateBadge, whoAvatar } from "./agent.js";
import { ask } from "./dialog.js";
import { h } from "./dom.js";
import { capitalized, plural, whenExactly } from "./format.js";
import { banner } from "./forms.js";
import { avatar, icon } from "./icons.js";
import { repoLinks } from "./repository.js";
import { go } from "./router.js";
import { base, me } from "./state.js";

// mayCancel is whether the viewer may cancel t: its assigner may, and so may whoever manages the organization's
// principals, while it hasn't ended.
const mayCancel = (t, can) => activeTask(t) && ((t.assignedBy && t.assignedBy.id === me.id) || can("org.principals.manage"));
// kindOf is what t is for, as its facts name it.
const kindOf = (t) => t.private ? "Private conversation" : { assigned: "Assigned", chat: "Chat", issue_chat: "Issue chat" }[t.kind] || capitalized(t.kind);
// origins are where a task the agent was asked for in a conversation came from, as the page's header says it.
const origins = { chat: "from a chat", issue_chat: "from an issue's chat" };
// ends are what the header says a task that ended did.
const ends = { succeeded: "finished", failed: "failed", cancelled: "cancelled" };
// whenOf is when t started, or was queued, and by whom, then when it ended.
const whenOf = (t) => [(t.startedAt ? "started " + since(t.startedAt) : "queued " + since(t.createdAt)) + (t.assignedBy ? " by " + t.assignedBy.displayName : ""),
  t.endedAt ? (ends[t.state] || "ended") + " " + since(t.endedAt) : ""];
// badge is t's state (Badge/Quiet); a task's page says a task that succeeded finished.
const badge = (t) => t.state === "succeeded" ? quiet("Finished", "success") : stateBadge(t.state);
// turnsOf is how many turns t's model took, as the page says it: the turn it is on while t runs, how many once it ended.
const turnsOf = (t, turns) => turns === null ? "" : activeTask(t) ? "turn " + turns : plural(turns, "turn");

// showTask draws task number of the agent whose ID is agentID: its header, an outcome once it ended, its transcript,
// its facts and its spend, and follows it until it ends or the viewer leaves the page. Another agent's task number sends
// the viewer to that agent's page of it; a task the viewer may not see, another's private one among them, is none.
export async function showTask(org, main, can, agentID, number) {
  const path = "/orgs/" + org.slug + "/tasks/" + encodeURIComponent(number);
  let t;
  try {
    t = await api("GET", path);
  } catch (err) {
    if (err.status !== 404 && err.status !== 400) throw err;
    api("GET", "/orgs/" + org.slug + "/agents/" + encodeURIComponent(agentID)).then((a) => named(org, a.id, a.name), () => {});
    return main.replaceChildren(emptyState("sparkles", "No such task", "There is no task #" + number + " in " + org.name + ", or you may not see it."));
  }
  if (t.agent.id !== agentID) return go("/" + org.slug + "/agents/" + t.agent.id + "/tasks/" + t.number, true);
  named(org, t.agent.id, t.agent.displayName, "#" + t.number + " " + t.title);
  const transcript = transcriptCard(t);
  const head = h("div", { class: "task-head" });
  const side = h("div", { class: "agent-column" });
  const draw = () => {
    head.replaceChildren(taskHeader(org, t, can, transcript.turns(), cancel), outcome(t));
    side.replaceChildren(factsCard(org, t), spendCard(t, transcript.turns(), transcript.calls()));
    transcript.redraw(t);
  };
  const cancel = () => ask("Cancel #" + t.number + "?", t.agent.displayName + " stops where it is. What it pushed stays on its branch.", "Cancel task", async () => {
    t = await api("POST", path + "/cancel", undefined, { "If-Match": `"${t.version}"` });
    draw();
  });
  main.replaceChildren(head, h("div", { class: "agent-columns" }, h("div", { class: "agent-column" }, transcript.el), side));
  draw();
  await transcript.read(path);
  draw();
  await follow(path, () => t, (got) => { t = got; }, transcript, head, draw);
}

// follow reads task t and its transcript every few seconds while it hasn't ended and its page shows, and draws the page
// again when either changed; it waits longer each time a read fails, up to a minute.
async function follow(path, get, set, transcript, shown, draw) {
  let delay = 3000;
  while (activeTask(get()) && shown.isConnected) {
    await new Promise((resolve) => setTimeout(resolve, delay));
    if (!shown.isConnected) return;
    try {
      const got = await api("GET", path);
      const more = await transcript.read(path);
      if (got.version !== get().version || more) { set(got); draw(); }
      transcript.live("");
      delay = 3000;
    } catch {
      transcript.live("Reconnecting…");
      delay = Math.min(delay * 2, 60000);
    }
  }
}

// named shows the agent in the breadcrumbs by its name, linked to its page, and, once the task is read, its number and
// title in the window's title.
function named(org, id, name, title) {
  const crumb = document.querySelector(".app-crumbs li:nth-last-child(2) a");
  if (crumb) { crumb.textContent = name; crumb.href = agentPage(org, id); }
  if (title) document.title = title + " · Cobranch";
}

// taskHeader is the top of t's page: its number, title and state, whose it is, where it came from, where it works,
// when and by whom, and, once it started, how many turns its model took; then Open branch and, for a viewer who may,
// Cancel task.
function taskHeader(org, t, can, turns, cancel) {
  const where = t.repositorySlug && "on " + [t.repositorySlug, t.branch].filter(Boolean).join(" ");
  const said = [t.agent.displayName, t.private ? "from a private conversation" : origins[t.kind], where, ...whenOf(t), t.startedAt ? turnsOf(t, turns) : ""]
    .filter(Boolean).join(" · ");
  const branch = t.repositorySlug && t.branch
    ? h("a", { class: "btn", href: repoLinks(org, { slug: t.repositorySlug }).branchPage(t.branch), "data-nav": "" }, icon("git-branch"), "Open branch") : "";
  return h("header", { class: "page-header agent-header" },
    h("div", { class: "page-header-text" },
      h("div", { class: "app-page-heading-title" }, h("span", { class: "task-number" }, "#" + t.number),
        h("h1", { class: "page-header-title" }, t.title), badge(t)),
      h("p", { class: "page-header-description task-meta" }, said)),
    h("div", { class: "page-header-actions" }, branch,
      mayCancel(t, can) ? h("button", { class: "btn btn-danger", type: "button", onclick: cancel }, icon("x"), "Cancel task") : ""));
}

// outcome is how t ended, or that it waits for an answer, as a Banner in its tone; nothing while it is queued or runs.
function outcome(t) {
  if (t.state === "succeeded") return banner("success", "Finished", t.summary);
  if (t.state === "failed") return banner("danger", "Failed", t.reason && capitalized(t.reason));
  // The Console's Cancel task gives no reason of its own, which the API records as "Cancelled".
  const why = t.reason && t.reason.toLowerCase() !== "cancelled" ? capitalized(t.reason) : "";
  if (t.state === "cancelled") return banner("info", "Cancelled" + (t.cancelledBy ? " by " + t.cancelledBy.displayName : ""), why);
  if (t.state === "waiting") return banner("warning", "Waiting for an answer", "It asked in its thread and carries on once someone answers.");
  return "";
}

// factsCard is what t is (Task): its agent and kind, the branch it works on, or its repository when it works on none,
// who assigned it, and when it started, or was queued.
function factsCard(org, t) {
  const moment = (at) => h("span", { title: whenExactly(at) }, since(at));
  const who = (p, agent) => h("span", { class: "task-fact-who" }, agent ? avatar(p.displayName, true) : whoAvatar(p), p.displayName);
  const links = t.repositorySlug ? repoLinks(org, { slug: t.repositorySlug }) : null;
  const rows = [fact("Agent", h("a", { href: agentPage(org, t.agent.id), "data-nav": "" }, who(t.agent, true))), fact("Kind", kindOf(t))];
  if (links && t.branch) rows.push(fact("Branch", h("a", { href: links.branchPage(t.branch), "data-nav": "", title: t.repositorySlug + " " + t.branch }, t.branch), true));
  else if (links) rows.push(fact("Repository", h("a", { href: base + org.slug + "/" + t.repositorySlug, "data-nav": "" }, t.repositorySlug), true));
  rows.push(fact("Assigned by", t.assignedBy ? who(t.assignedBy) : "Someone since gone"));
  rows.push(t.startedAt ? fact("Started", moment(t.startedAt)) : fact("Queued", moment(t.createdAt)));
  return card("Task", "", h("div", { class: "agent-facts" }, rows));
}

// spendCard is what t spent (Spend): against its agent's cap on a Meter, warning from 80% of it and danger once
// reached, which fails the task; then the tokens its model read and wrote, and the tools it called and the turns it took,
// which only its transcript's readers know.
function spendCard(t, turns, calls) {
  const spent = micros(t.spent);
  const cap = t.cap ? micros(t.cap) : 0;
  const share = cap ? spent / cap : 0;
  const tone = !cap ? "" : share >= 1 ? " meter-danger" : share >= 0.8 ? " meter-warning" : "";
  const row = (key, value) => h("div", { class: "task-spend-row" }, h("dt", {}, key), h("dd", {}, value));
  const count = (n) => n === null ? "–" : n.toLocaleString("en-US");
  return card("Spend", "",
    h("div", { class: "task-spend meter" + tone }, h("div", { class: "meter-head" }, h("span", { class: "meter-label" }, "Spend"),
      h("span", { class: "meter-value" }, cap ? dollars(t.spent) + " of " + dollars(t.cap) : dollars(t.spent) + " · no cap")),
    cap ? h("span", { class: "meter-track", title: Math.round(share * 100) + "% of its cap" }, h("span", { class: "meter-bar", style: "width: " + Math.min(100, share * 100).toFixed(1) + "%" })) : ""),
    h("dl", { class: "task-spend-facts" }, row("Input tokens", count(t.inputTokens)), row("Output tokens", count(t.outputTokens)),
      row("Tool calls", count(calls)), row("Turns", count(turns))));
}

// transcriptCard is t's transcript (Transcript): its steps, read a page at a time from the API, how many turns it took
// and what it spent, and Follow while t hasn't ended, which keeps the newest step in view. Its assigner,
// org.principals.manage and its repository's readers read it; anyone else sees a note that says so.
function transcriptCard(t) {
  const entries = [];
  const opened = new Set();
  let task = t;
  let readable = null;
  let following = false;
  const list = h("ol", { class: "task-steps" });
  const meta = h("span", { class: "card-meta" });
  const live = h("span", { class: "card-meta" });
  const toggle = h("button", { class: "btn btn-ghost btn-sm", type: "button", "aria-pressed": "false", onclick: () => {
    following = !following;
    toggle.setAttribute("aria-pressed", String(following));
    toggle.className = "btn btn-sm" + (following ? "" : " btn-ghost");
    toggle.replaceChildren(...(following ? [icon("check"), "Following"] : ["Follow"]));
    if (following) newest();
  } }, "Follow");
  const body = h("div", { class: "task-transcript" }, list);
  const el = h("section", { class: "card" }, h("header", { class: "card-header" }, h("h2", { class: "card-title" }, "Transcript"), live, meta, toggle), body);
  const newest = () => { if (list.lastElementChild) list.lastElementChild.scrollIntoView({ block: "end" }); };
  const turns = () => readable ? entries.filter((e) => e.kind === "turn").length : null;
  const calls = () => readable ? entries.reduce((n, e) => n + (e.kind === "turn" && Array.isArray(e.body.calls) ? e.body.calls.length : 0), 0) : null;
  const redraw = (now) => {
    task = now;
    const spend = task.cap ? dollars(task.spent) + " of " + dollars(task.cap) : dollars(task.spent);
    meta.textContent = readable ? [turnsOf(task, turns()), spend].join(" · ") : "";
    toggle.hidden = !activeTask(task) || !readable;
    if (readable === false) return body.replaceChildren(emptyState("eye", "Not yours to read", "Its assigner, the organization's admins and whoever reads its repository read its transcript."));
    if (readable && !entries.length) {
      return body.replaceChildren(activeTask(task) ? emptyState("clock", "Nothing yet", "The agent records each step here as it works.")
        : emptyState("clock", "Nothing recorded", "It ended before " + task.agent.displayName + " recorded a step."));
    }
    list.replaceChildren(...steps(entries, task).map((s) => stepItem(s, task, opened)));
    body.replaceChildren(list);
    if (following) newest();
  };
  // read appends the entries after the last one read, a page at a time, and answers whether there were any; once the
  // API refuses the viewer them, it reads no more.
  const read = async (path) => {
    if (readable === false) return false;
    let more = false;
    for (;;) {
      let page;
      try {
        page = await api("GET", path + "/transcript?" + new URLSearchParams({ after: entries.length ? entries[entries.length - 1].seq : 0, limit: 500 }));
      } catch (err) {
        if (err.status !== 403) throw err;
        readable = false;
        return false;
      }
      readable = true;
      entries.push(...page.entries);
      more = more || page.entries.length > 0;
      if (page.entries.length < 500) return more;
    }
  };
  return { el, read, redraw, turns, calls, live: (text) => { live.textContent = text; } };
}

// steps are a transcript's entries as its page shows them: what the task was given, each move of its state, what the
// model said, and each tool it called, with what the call answered once it did.
function steps(entries, t) {
  const out = [];
  const calls = new Map();
  let inputs = 0;
  for (const e of entries) {
    const b = e.body || {};
    if (e.kind === "result" && calls.has(b.callId)) {
      calls.get(b.callId).result = b;
    } else if (e.kind === "turn") {
      if (typeof b.text === "string" && b.text.trim()) out.push({ at: e.createdAt, kind: "say", text: b.text });
      for (const c of Array.isArray(b.calls) ? b.calls : []) {
        const s = { at: e.createdAt, kind: "call", call: c, seq: e.seq };
        calls.set(c.id, s);
        out.push(s);
      }
    } else {
      out.push({ at: e.createdAt, kind: e.kind, body: b, first: e.kind === "input" && b.by !== "harness" && inputs++ === 0, seq: e.seq });
    }
  }
  return out.map((s) => ({ ...s, offset: offset(s.at, t.startedAt || t.createdAt) }));
}

// offset is how long after its task started a step came, in minutes and seconds: 0:02, 12:40, 75:10.
function offset(at, from) {
  const s = Math.max(0, Math.floor((new Date(at) - new Date(from)) / 1000));
  return Math.floor(s / 60) + ":" + String(s % 60).padStart(2, "0");
}

// stepItem draws a step of the transcript (Turn): when it came, its icon, who or what it was with a note, and its text;
// a tool's call opens to what it answered, and stays open, by its call's ID, while the page draws again.
function stepItem(s, t, opened) {
  const [glyph, tone, name, note, text, code] = stepParts(s, t);
  const row = (tag, attrs) => h(tag, { class: "task-step", ...attrs },
    h("span", { class: "task-step-time", title: whenExactly(s.at) }, s.offset),
    h("span", { class: "task-step-icon tone-" + tone }, icon(glyph)),
    h("span", { class: "task-step-text" },
      h("span", { class: "task-step-head" }, h("span", { class: "task-step-name" }, name), note ? h("span", { class: "task-step-note" + (s.result && s.result.error ? " tone-danger" : "") }, note) : ""),
      text ? h("span", { class: code ? "task-step-code" : "task-step-body" }, text) : ""));
  if (s.kind !== "call" || !s.result) return h("li", {}, row("div", {}));
  const id = s.call.id || String(s.seq);
  const output = h("div", { class: "task-step-more" }, h("pre", { class: "task-step-output" }, s.result.text || "No output."),
    s.result.cut ? h("span", { class: "task-step-cut" }, "Cut to its first 64 KiB, of " + Number(s.result.size).toLocaleString("en-US") + " bytes.") : "");
  return h("li", {}, h("details", { open: opened.has(id) || null, ontoggle: (e) => { if (e.currentTarget.open) opened.add(id); else opened.delete(id); } },
    row("summary", {}), output));
}

// stateWords are what a state entry says its task did: started, or went on after an answer, waited, finished or failed.
const stateWords = { running: ["clock", "neutral", "Started"], waiting: ["circle-pause", "warning", "Waiting for an answer"],
  succeeded: ["circle-check", "success", "Finished"], failed: ["circle-x", "danger", "Failed"] };

// stepParts are what step s shows: its icon and the icon's tone, its name and note, and its text, in code type when code.
function stepParts(s, t) {
  const b = s.body;
  if (s.kind === "say") return ["sparkles", "ai", t.agent.displayName, "", s.text];
  if (s.kind === "call") return ["command", "neutral", String(s.call.name || "?"), callNote(s, t), firstText(s.call.arguments), true];
  if (s.kind === "input" && b.by === "harness") return ["info", "neutral", "Cobranch", "told it", b.text];
  if (s.kind === "input") return s.first ? ["user", "neutral", t.assignedBy ? t.assignedBy.displayName : "Its assigner", "assigned", b.text] : ["message-square", "neutral", "Its thread", "answered", b.text];
  if (s.kind === "state") {
    const [glyph, tone, word] = stateWords[b.state] || ["info", "neutral", capitalized(String(b.state))];
    const where = b.state === "running" && t.repositorySlug ? " on " + [t.repositorySlug, t.branch].filter(Boolean).join(" · ") : "";
    const said = b.summary || (b.reason && capitalized(b.reason));
    return [glyph, tone, "Task", "", word + where + (said ? ": " + said : "")];
  }
  return ["info", "neutral", capitalized(s.kind), "", typeof b.text === "string" ? b.text : JSON.stringify(b)];
}

// callNote says how a tool's call went: running while its task waits on it, failed, how many lines it answered, or
// that its output was cut.
function callNote(s, t) {
  const r = s.result;
  if (!r) return activeTask(t) ? "running" : "no answer";
  if (r.error) return "failed";
  const lines = r.text ? r.text.split("\n").length : 0;
  return (lines ? plural(lines, "line") : "no output") + (r.cut ? " · cut" : "");
}

// firstText is a call's first argument that is text, shortened to 160 characters, as its step's line shows it.
function firstText(args) {
  const first = args && typeof args === "object" ? Object.values(args).find((v) => typeof v === "string") : null;
  return first && first.length > 160 ? first.slice(0, 160) + "…" : first || "";
}
