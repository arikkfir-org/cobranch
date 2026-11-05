import { api, apiPage } from "../../lib/api.js";
import { h } from "../../lib/dom.js";
import { capitalized, whenExactly } from "../../lib/format.js";
import { icon } from "../../lib/icons.js";
import { repoLinks } from "../../lib/repository.js";
import { go } from "../../lib/router.js";
import { remember } from "../../lib/sidebar.js";
import { base, me } from "../../lib/state.js";
import { taskBadge, taskStates } from "../../lib/tasks.js";
import { agentItem } from "./agents.js";

export const nav = { label: "Tasks", order: 60, under: "agents" };
export const crumbs = ({ root, sub }) => [{ label: "Agents", href: root + "/agents" }, { label: "Tasks", href: root + "/tasks" }, ...(sub ? [{ label: "#" + sub }] : [])];
export default ({ org, main, can, sub }) => sub ? showTask(org, main, can, sub) : showTasks(org, main, can);

const activeTask = (t) => ["queued", "running", "waiting"].includes(t.state);
// taskByline says whose a task is, where it works, who assigned it and when.
function taskByline(org, t) {
  const where = !t.repositorySlug ? h("span", { class: "muted" }, "Anywhere") : [h("a", { href: base + org.slug + "/" + t.repositorySlug, "data-nav": "" }, t.repositorySlug),
    t.branch ? [" · ", h("a", { href: repoLinks(org, { slug: t.repositorySlug }).branchPage(t.branch), "data-nav": "" }, icon("git-branch"), t.branch)] : null];
  return h("p", { class: "muted" }, t.agent.displayName, " · ", where, " · ", t.assignedBy ? "assigned by " + t.assignedBy.displayName : "its assigner is gone",
    " · ", whenExactly(t.createdAt));
}
// The transcript's entry kinds, as the task's page names them.
const entryKinds = { input: "Input", turn: "Model", result: "Tool result", state: "State" };
// transcriptEntry draws an entry of a task's transcript: its text, the tools a turn calls, whether a tool failed or
// its output was cut, a state it moved the task to, and the rest of what the harness recorded, folded away.
function transcriptEntry(e) {
  const { text, ...others } = e.body;
  const { state, summary, reason, ...rest } = others;
  const extra = e.kind === "state" ? rest : others;
  const calls = e.kind === "turn" && Array.isArray(e.body.calls) ? e.body.calls : [];
  const failed = e.kind === "result" && e.body.error === true;
  const kind = (failed ? "Tool failed" : entryKinds[e.kind] || e.kind) + (e.body.by === "harness" ? ", from the harness" : "");
  return h("li", { class: e.kind + (failed ? " failed" : ""), id: "entry-" + e.seq },
    h("p", { class: "muted" }, "#" + e.seq, " · ", kind, typeof e.body.name === "string" ? [" · ", h("code", {}, e.body.name)] : null, " · ", whenExactly(e.createdAt)),
    e.kind === "state" && taskStates[state] ? h("p", {}, taskBadge(state), summary ? [" ", summary] : null, reason ? [" ", capitalized(reason)] : null) : null,
    typeof text === "string" && text ? h("p", { class: "text" }, text) : null,
    calls.length ? h("ul", { class: "calls" }, calls.map(callLine)) : null,
    e.body.cut === true ? h("p", { class: "muted" }, "Cut to its first 64 KiB, of " + Number(e.body.size).toLocaleString() + " bytes.") : null,
    Object.keys(extra).length ? h("details", {}, h("summary", { class: "muted" }, "Details"), h("pre", {}, JSON.stringify(extra, null, 2))) : null);
}

// callLine draws a tool's call that a turn makes: the tool's name and its first argument that is text, shortened.
function callLine(c) {
  const args = c && c.arguments && typeof c.arguments === "object" ? c.arguments : {};
  const first = Object.values(args).find((v) => typeof v === "string");
  const shown = first && first.length > 160 ? first.slice(0, 160) + "…" : first;
  return h("li", {}, h("code", {}, String((c && c.name) || "?")), shown ? [" ", h("span", { class: "muted" }, shown)] : null);
}

// showTask renders one of the organization's tasks and its transcript, following both until the task ends. The
// transcript is its assigner's, org.principals.manage's and its repository's readers'; others see the task alone.
async function showTask(org, main, can, number) {
  const path = "/orgs/" + org.slug + "/tasks/" + encodeURIComponent(number);
  let t;
  try {
    t = await api("GET", path);
  } catch (err) {
    if (err.status === 404 || err.status === 400) return main.replaceChildren(h("h1", {}, "Task #" + number), h("p", {}, "There is no such task, or you may not see it."));
    throw err;
  }
  const error = h("p", { class: "alert", hidden: true });
  const head = h("section", { class: "panel" });
  const entries = h("ol", { class: "transcript" });
  const live = h("span", { class: "muted" });
  const note = h("p", { class: "muted", hidden: true });
  const cancel = (button) => {
    button.disabled = true;
    api("POST", path + "/cancel", undefined, { "If-Match": `"${t.version}"` }).then((got) => { t = got; draw(); })
      .catch((err) => { button.disabled = false; error.textContent = err.message; error.hidden = false; });
  };
  // replaceChildren would write a null as text, so the parts left out are dropped first.
  const draw = () => head.replaceChildren(...[
    h("div", { class: "row" }, taskBadge(t.state), h("span", { class: "spacer" }),
      activeTask(t) && ((t.assignedBy && t.assignedBy.id === me.id) || can("org.principals.manage"))
        ? h("button", { class: "button small danger", type: "button", onclick: (e) => confirm("Cancel #" + t.number + ", " + t.title + "?") && cancel(e.currentTarget) }, "Cancel") : null),
    taskByline(org, t),
    t.instructions ? h("p", { class: "instructions" }, t.instructions) : null,
    t.reason ? h("p", {}, capitalized(t.reason)) : null,
    t.summary ? h("p", {}, h("strong", {}, "Summary: "), t.summary) : null].filter(Boolean));
  draw();
  main.replaceChildren(h("h1", {}, t.title, " ", h("span", { class: "muted" }, "#" + t.number)), error, head,
    h("section", { class: "panel" }, h("div", { class: "row" }, h("h2", {}, "Transcript"), h("span", { class: "spacer" }), live), note, entries));
  let after = 0;
  // read appends the entries after the last one drawn, a page at a time, and answers false once the viewer may not.
  const read = async () => {
    for (;;) {
      let page;
      try {
        page = await api("GET", path + "/transcript?" + new URLSearchParams({ after, limit: 500 }));
      } catch (err) {
        if (err.status !== 403) throw err;
        note.textContent = "Its assigner, the organization's admins and whoever reads its repository read its transcript.";
        note.hidden = false;
        return false;
      }
      entries.append(...page.entries.map(transcriptEntry));
      if (page.entries.length) after = page.entries[page.entries.length - 1].seq;
      if (page.entries.length < 500) break;
    }
    note.textContent = "Nothing yet: the agent records each step here as it works.";
    note.hidden = entries.children.length > 0;
    return true;
  };
  if (!(await read())) return;
  // While the task hasn't ended, its page follows it every few seconds, until the viewer leaves the page.
  let delay = 3000;
  while (activeTask(t) && head.isConnected) {
    live.textContent = "Live";
    await new Promise((resolve) => setTimeout(resolve, delay));
    if (!head.isConnected) return;
    try {
      const got = await api("GET", path);
      await read();
      if (got.version !== t.version) { t = got; draw(); }
      delay = 3000;
    } catch {
      live.textContent = "Reconnecting…";
      delay = Math.min(delay * 2, 60000);
    }
  }
  live.textContent = "";
}

// showTasks renders a page at a time of the organization's tasks, newest first, of the agent the URL's query names,
// and in the states the viewer picks: each with its state, its agent, where it works, its assigner, and Cancel for
// its assigner and for a viewer holding org.principals.manage.
async function showTasks(org, main, can) {
  const error = h("p", { class: "alert", hidden: true });
  const fail = (err) => { error.textContent = err.message; error.hidden = false; };
  const query = new URLSearchParams(location.search);
  const filter = h("select", { onchange: () => { query.set("state", filter.value); if (!filter.value) query.delete("state"); go("/" + org.slug + "/tasks?" + query, true); } },
    [["", "Every task"], ["active", "Active"], ["succeeded", "Succeeded"], ["failed", "Failed"], ["cancelled", "Cancelled"]]
      .map(([value, label]) => h("option", { value, selected: (query.get("state") || "") === value }, label)));
  const params = new URLSearchParams();
  if (query.get("agent")) {
    params.set("agent", query.get("agent"));
    api("GET", "/orgs/" + org.slug + "/agents/" + encodeURIComponent(query.get("agent"))).then((a) => remember(org, "agents", agentItem(org, a)),
      () => { /* no such agent: its tasks say so, and the sidebar keeps what it showed */ });
  }
  for (const s of query.get("state") === "active" ? ["queued", "running", "waiting"] : query.get("state") ? [query.get("state")] : []) params.append("state", s);
  const rows = h("tbody");
  let next = null;
  const older = h("button", { class: "button small", type: "button", hidden: true, onclick: () => load(next) }, "Older tasks");
  const cancel = (t, button) => {
    button.disabled = true;
    api("POST", "/orgs/" + org.slug + "/tasks/" + t.number + "/cancel", undefined, { "If-Match": `"${t.version}"` })
      .then(() => showTasks(org, main, can)).catch((err) => { button.disabled = false; fail(err); });
  };
  const row = (t) => h("tr", {},
    h("td", { class: "state" }, taskBadge(t.state)),
    h("td", {}, h("a", { href: base + org.slug + "/tasks/" + t.number, "data-nav": "" }, t.title), " ", h("span", { class: "muted" }, "#" + t.number),
      t.reason ? h("p", {}, capitalized(t.reason)) : null, t.summary ? h("p", {}, t.summary) : null, taskByline(org, t)),
    h("td", { class: "actions" }, activeTask(t) && ((t.assignedBy && t.assignedBy.id === me.id) || can("org.principals.manage"))
      ? h("button", { class: "button small danger", type: "button", title: "Cancel #" + t.number, onclick: (e) => confirm("Cancel #" + t.number + ", " + t.title + "?") && cancel(t, e.currentTarget) }, "Cancel") : null));
  async function load(from) {
    older.disabled = true;
    try {
      const page = await apiPage(from);
      rows.append(...page.data.tasks.map(row));
      next = page.next;
      older.hidden = !next;
    } catch (err) {
      fail(err);
    } finally {
      older.disabled = false;
    }
  }
  await load("/orgs/" + org.slug + "/tasks" + (params.toString() ? "?" + params : ""));
  main.replaceChildren(error, h("section", { class: "panel" },
    h("div", { class: "row" }, filter, h("span", { class: "spacer" }), can("agents.assign") ? h("a", { class: "button small primary", href: base + org.slug + "/agents", "data-nav": "" }, "Assign a task") : null),
    rows.children.length ? h("table", { class: "runs" }, rows) : h("p", {}, "No tasks here yet. Members who hold ", h("code", {}, "agents.assign"), " assign them to the organization's agents."),
    h("div", {}, older)));
}
