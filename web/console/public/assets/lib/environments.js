import { api, apiPage } from "./api.js";
import { h } from "./dom.js";
import { ago, fromNow, plural, shortSha, whenExactly } from "./format.js";
import { icon } from "./icons.js";
import { popover } from "./menu.js";
import { render } from "./router.js";

// An environment as the Figma file's Branch · Environment and Repository · Delivery screens draw it: its health and
// how it stands as Badge/Quiet, its facts as Fact rows, its objects in a table, and its deliveries, resyncs left out.

// quiet is a Badge/Quiet of word in tone.
const quiet = (tone, word) => h("span", { class: "badge-quiet tone-" + tone }, word);

// healths are an environment's or an object's healths, as Badge/Quiet draws them.
const healths = {
  healthy: ["success", "Healthy"],
  progressing: ["info", "Progressing"],
  unknown: ["neutral", "Unknown"],
  missing: ["warning", "Missing"],
  degraded: ["danger", "Degraded"],
};
const healthBadge = (health) => quiet(...(healths[health] || healths.unknown));

// syncBadge is how e stands against its branch: Paused, Synced, or Out of sync, in danger once its newest delivery
// failed.
const syncBadge = (e, newest) => e.paused ? quiet("warning", "Paused") : e.sync === "synced" ? quiet("neutral", "Synced")
  : quiet(newest && newest.state === "failed" ? "danger" : "warning", "Out of sync");

// glyphs are each delivery state's icon and tone in a Delivery row.
const glyphs = {
  waiting: ["clock", null, "Waiting"],
  queued: ["circle-dashed", null, "Queued"],
  rendering: ["loader-circle", "info", "Rendering"],
  applying: ["loader-circle", "info", "Applying"],
  deleting: ["loader-circle", "info", "Deleting"],
  succeeded: ["circle-check", "success", "Succeeded"],
  failed: ["circle-x", "danger", "Failed"],
  cancelled: ["ban", null, "Cancelled"],
};
const ended = (d) => ["succeeded", "failed", "cancelled"].includes(d.state);
const running = (d) => ["rendering", "applying", "deleting"].includes(d.state);

// took is how long from one moment to another, or to now, as a delivery's line says it: 48 s, 3 min, 2 h.
function took(from, to) {
  const s = Math.max(0, Math.round(((to ? new Date(to) : new Date()) - new Date(from)) / 1000));
  return s < 60 ? s + " s" : s < 3600 ? Math.floor(s / 60) + " min" : Math.floor(s / 3600) + " h";
}

// sha links a commit, in Code/Small.
const sha = (at, commit) => h("a", { class: "environment-sha", href: at.commit(commit), "data-nav": "" }, shortSha(commit));

// standing says how a delivery stands, as an environment's status line says it, and whether it says a failure.
function standing(d) {
  const n = "#" + d.number;
  const reason = d.reason ? ": " + d.reason : "";
  if (d.state === "failed") return [n + " failed " + fromNow(d.endedAt || d.queuedAt) + reason, true];
  if (d.state === "cancelled") return [n + " cancelled " + fromNow(d.endedAt || d.queuedAt) + reason, false];
  if (d.state === "waiting") return [n + " " + (d.reason || "waits to start"), false];
  if (d.state === "queued") return [n + " queued" + (d.sha ? " for " + shortSha(d.sha) : ""), false];
  if (running(d)) return [n + " " + d.state + (d.sha ? " " + shortSha(d.sha) : "") + " · " + took(d.startedAt || d.queuedAt), false];
  return [n + (d.kind === "resync" ? " resynced " : " delivered ") + fromNow(d.endedAt), false];
}

// statusText is what e's status line says after its revision: its newest delivery, or what it waits for, then the
// last that delivered it, with how long that took; and for a paused environment behind its branch, that the push
// waits. head is its branch's head, when known.
function statusText(e, list, head) {
  const newest = list[0];
  const pending = list.find((d) => !ended(d));
  const last = list.find((d) => d.state === "succeeded" && d.kind === "deliver");
  let text, danger = false;
  if (newest && (newest.state === "failed" || newest.state === "cancelled")) [text, danger] = standing(newest);
  else if (pending) {
    [text] = standing(pending);
    const busy = pending.state === "queued" && list.find((d) => d !== pending && running(d));
    if (busy) text += ", waiting for #" + busy.number + " to finish " + busy.state;
  } else if (last) text = "#" + last.number + " delivered " + fromNow(last.endedAt) + " in " + took(last.startedAt || last.queuedAt, last.endedAt);
  else text = "Nothing delivered yet";
  if (e.paused && e.sync === "out_of_sync" && !pending) {
    text += " · " + (head && head !== e.revision ? shortSha(head) + " was pushed since and waits" : "a push since waits") + " until the environment resumes";
  }
  return [text, danger];
}

// statusLines are e's status: its health and how it stands, the commit it holds and what its deliveries say, then its
// URL; while a teardown runs, that alone. Its objects are those of the last apply until the teardown ends, so the line
// counts none as remaining.
function statusLines(at, e, list, head) {
  const teardown = list[0] && list[0].kind === "teardown" && !ended(list[0]) ? list[0] : null;
  const url = e.url ? h("p", { class: "environment-line" }, icon("globe"), h("a", { href: e.url, target: "_blank", rel: "noopener" }, e.url)) : null;
  if (teardown) {
    return [h("p", { class: "environment-line" }, quiet("info", "Deleting"), h("span", { class: "environment-text" }, "Teardown #" + teardown.number +
      " deletes what the environment holds · " + (teardown.startedAt ? "started " + fromNow(teardown.startedAt) : "queued " + fromNow(teardown.queuedAt))))];
  }
  const [text, danger] = statusText(e, list, head);
  return [h("p", { class: "environment-line" }, healthBadge(e.health), syncBadge(e, list[0]), e.revision ? ["at", sha(at, e.revision)] : null,
    h("span", { class: "environment-text" + (danger ? " tone-danger" : "") }, (e.revision ? "· " : "") + text)), url];
}

// clusterName says which cluster a target delivers to: its secret's name, for a viewer who may see it, else that a
// secret holds it, or this installation's own.
const clusterName = (t, own) => t.clusterName ? t.clusterName + " (secret)" : t.cluster ? h("span", { class: "environment-quiet", title: t.cluster }, "A secret")
  : h("span", { class: own ? null : "environment-quiet" }, own || "This installation");

// why is why an environment is torn down: its branch is gone, or, while the branch lives, main's targets no longer give
// it one.
const why = (live) => (live ? "main stopped matching the branch" : "the branch was deleted");

// tornDown says when e is torn down: once its branch is gone, or main's targets no longer give it one, and while a
// teardown runs, why it does. live is whether its branch still is.
function tornDown(e, list, live) {
  const teardown = list[0] && list[0].kind === "teardown" && !ended(list[0]) ? list[0] : null;
  if (teardown) return "Now: " + why(live) + " " + fromNow(teardown.queuedAt);
  return e.branch === "main" ? "When main stops matching it" : "When the branch is deleted, or when main stops matching it";
}

// fact is a Fact row of label and value; code sets the value in Code/Small.
const fact = (label, value, code) => h("div", { class: "fact-row" }, h("span", { class: "fact-label" }, label),
  h("span", { class: "fact-value" + (code ? " environment-code" : "") }, value));

// facts are e's cluster, namespace, what its branch's head renders, and when it is torn down.
const facts = (e, list, live) => h("div", { class: "facts environment-facts" },
  fact("Cluster", clusterName(e, "This installation’s cluster")),
  fact("Namespace", e.namespace, true),
  fact("Renders", e.renders.length ? e.renders.map((r) => r.kind + " " + r.path).join(" · ") : h("span", { class: "environment-quiet" }, "Nothing: no root renders for it"), e.renders.length > 0),
  fact("Torn down", tornDown(e, list, live)));

// section is a card's Section line: a title and, at its end, what it holds.
const section = (title, meta, cls) => h("div", { class: "environment-section" + (cls ? " " + cls : "") }, h("h3", {}, title),
  meta ? h("span", { class: "card-meta" }, meta) : null);

// objectsTable is what e holds, as its last apply recorded it, each with its kind, health, how much of it is ready and
// its age.
function objectsTable(objects) {
  if (!objects.length) return [section("Objects"), h("p", { class: "card-body card-note environment-note" }, "Nothing applied yet.")];
  const by = Math.max(...objects.map((o) => o.delivery || 0));
  const row = (o) => h("tr", {},
    h("td", { class: "cell-mono", title: (o.namespace ? o.namespace + "/" : "") + o.name }, o.name),
    h("td", { class: "cell-muted" }, o.kind),
    h("td", {}, healthBadge(o.health),
      o.message ? h("p", { class: "environment-message" + (o.health === "degraded" ? " tone-danger" : "") }, o.message) : null),
    h("td", {}, o.ready || ""),
    h("td", { class: "cell-muted", title: o.createdAt ? whenExactly(o.createdAt) : null }, o.createdAt ? ago(o.createdAt) : ""));
  return [section("Objects", objects.length + " applied" + (by ? " by #" + by : "")),
    h("table", { class: "table environment-objects" }, h("thead", {}, h("tr", {}, ["Object", "Kind", "Health", "Ready", "Age"].map((c) => h("th", {}, c)))),
      h("tbody", {}, objects.map(row)))];
}

// deliveryAside is the end of a Delivery row: when it ended and how long it took, why it failed, or what it waits on.
function deliveryAside(d) {
  if (d.state === "succeeded") return [fromNow(d.endedAt) + " · " + took(d.startedAt || d.queuedAt, d.endedAt), false];
  if (d.state === "failed") return [fromNow(d.endedAt || d.queuedAt) + " · failed" + (d.reason ? ": " + d.reason : ""), true];
  if (d.state === "cancelled") return [fromNow(d.endedAt || d.queuedAt) + " · cancelled" + (d.reason ? ": " + d.reason : ""), false];
  if (d.state === "waiting") return ["pushed " + fromNow(d.queuedAt) + " · waiting for a member", false];
  if (d.state === "queued") return ["queued " + fromNow(d.queuedAt), false];
  return ["started " + fromNow(d.startedAt || d.queuedAt) + " · " + d.state, false];
}

// deliveryRow is one of e's deliveries: its state's icon, its number, its commit and the commit's subject, or what
// tears it down, and how it went; Start while it waits, for a viewer holding delivery.run.
function deliveryRow({ path, at, can, fail }, e, d, live) {
  const [glyph, tone, word] = d.kind === "teardown" && d.state !== "failed" ? ["trash-2", null, "Teardown"] : glyphs[d.state];
  const mark = icon(glyph, word + (d.reason ? ": " + d.reason : ""));
  if (tone) mark.classList.add("tone-" + tone);
  const what = d.kind === "teardown" ? h("span", { class: "delivery-subject" }, "Teardown: " + why(live))
    : [sha(at, d.sha), h("span", { class: "delivery-subject", title: d.subject }, d.subject)];
  const [aside, danger] = deliveryAside(d);
  const start = d.state === "waiting" && can("delivery.run") ? h("button", { class: "btn btn-primary btn-sm", type: "button",
    title: "Start #" + d.number + ", delivering " + shortSha(d.sha) + " to " + e.target.displayName, onclick: () => {
      start.disabled = true;
      api("POST", path + "/deliveries/" + d.number + "/start").then(render).catch((err) => { start.disabled = false; fail(err); });
    } }, icon("play"), "Start") : null;
  return h("li", { class: "delivery-row" }, mark, h("span", { class: "delivery-number" }, "#" + d.number), what,
    h("span", { class: "delivery-aside" + (danger ? " tone-danger" : ""), title: whenExactly(d.endedAt || d.startedAt || d.queuedAt) }, aside), start);
}

// deliveriesList is e's deliveries of commits and teardowns, newest first, with a button that lists older ones.
function deliveriesList(ctx, e, first, live) {
  const list = h("ul", { class: "environment-deliveries" }, first.data.deliveries.map((d) => deliveryRow(ctx, e, d, live)));
  let next = first.next;
  const older = h("button", { class: "btn btn-ghost btn-sm environment-older", type: "button", hidden: !next, onclick: async () => {
    older.disabled = true;
    try {
      const page = await apiPage(next);
      list.append(...page.data.deliveries.map((d) => deliveryRow(ctx, e, d, live)));
      next = page.next;
      older.hidden = !next;
    } catch (err) {
      ctx.fail(err);
    } finally {
      older.disabled = false;
    }
  } }, "Older deliveries");
  const none = h("p", { class: "card-body card-note environment-note" }, h("span", {}, "No deliveries yet. A push to ", h("code", {}, e.branch), " delivers it."));
  return [section("Deliveries", "Commits and teardowns; resyncs are not listed", "environment-section-ruled"),
    first.data.deliveries.length ? list : none, older];
}

// act asks the API to redeliver, pause or resume e, then draws the page again.
const act = ({ path, fail }, e, verb, button) => {
  if (button) button.disabled = true;
  api("POST", path + "/environments/" + e.id + "/" + verb).then(render).catch((err) => { if (button) button.disabled = false; fail(err); });
};

// actions are e's header buttons: Open where it answers, until a teardown deletes it; Redeliver, or Resume while it is paused, for a viewer
// holding delivery.run; and its menu, which pauses or resumes it and, off its own page, opens it.
function actions(ctx, e, list, page) {
  const { at, can } = ctx;
  const may = can("delivery.run");
  const tearing = list[0] && list[0].kind === "teardown" && !ended(list[0]);
  const name = e.target.displayName + " of " + e.branch;
  const open = e.url && !tearing ? h("a", { class: "btn btn-sm", href: e.url, target: "_blank", rel: "noopener", title: "Open " + e.url }, icon("globe"), "Open") : null;
  const main = !may ? null : e.paused ? h("button", { class: "btn btn-primary btn-sm", type: "button", disabled: tearing, title: "Resume " + name + " and deliver the head of " + e.branch,
    onclick: (ev) => act(ctx, e, "resume", ev.currentTarget) }, icon("play"), "Resume")
    : h("button", { class: "btn btn-sm", type: "button", disabled: tearing, title: "Deliver the head of " + e.branch + " to " + name + " again",
      onclick: (ev) => act(ctx, e, "redeliver", ev.currentTarget) }, icon("refresh-cw"), "Redeliver");
  // On its own page, the menu holds only Pause or Resume, which a teardown takes away.
  if (page && (!may || tearing)) return [open, main];
  const more = h("button", { class: "icon-btn icon-btn-ghost icon-btn-sm", type: "button", "aria-haspopup": "menu", "aria-label": "More", title: "More" }, icon("ellipsis"));
  more.onclick = () => popover(more, "end", (close) => [
    page ? null : h("a", { class: "menu-item", role: "menuitem", href: at.environment(e.id), "data-nav": "", onclick: close }, icon("globe"), h("span", {}, "Open its page")),
    may && !tearing ? h("button", { class: "menu-item", type: "button", role: "menuitem", onclick: () => { close(); act(ctx, e, e.paused ? "resume" : "pause"); } },
      icon(e.paused ? "play" : "circle-pause"), h("span", {}, e.paused ? "Resume" : "Pause: take no delivery until resumed")) : null]);
  return [open, main, more];
}

// environmentCard is e's card: its target, and its branch on its own page; Open, Redeliver or Resume, and its menu;
// its status; its facts; its objects; and its deliveries. head is its branch's head, when known, and live whether the
// branch still is.
export async function environmentCard(ctx, e, { page = false, head = "", live = true } = {}) {
  const { path, at } = ctx;
  const base = path + "/environments/" + e.id;
  const [objects, first] = await Promise.all([api("GET", base + "/objects").then((d) => d.objects),
    apiPage(base + "/deliveries?" + new URLSearchParams([["kind", "deliver"], ["kind", "teardown"]]))]);
  const list = first.data.deliveries;
  const title = h("h2", { class: "card-title" }, e.target.displayName, page ? [icon("git-branch"),
    h("a", { class: "environment-branch", href: at.branchPage(e.branch), "data-nav": "" }, e.branch)] : null);
  return h("section", { class: "card environment-card", "aria-label": e.target.displayName + " of " + e.branch },
    h("div", { class: "card-header" }, title, actions(ctx, e, list, page)),
    h("div", { class: "environment-status" }, statusLines(at, e, list, head)),
    facts(e, list, live), objectsTable(objects), deliveriesList(ctx, e, first, live));
}

// shortStanding is how e stands on the Delivery page's row, from its newest delivery of any kind.
function shortStanding(e) {
  const d = e.latest;
  let text = d ? standing(d)[0] : "nothing delivered yet";
  if (d && d.state === "succeeded") text = text.replace(/ in .*$/, "");
  if (e.paused && e.sync === "out_of_sync" && (!d || ended(d))) text += " · a push since waits until resumed";
  return text;
}

// environmentRow is e's row in the Delivery page's Environments card: its target, linking to its page, its health,
// how it stands and its branch; where it answers, the commit it holds and its newest delivery; and Open, until a
// teardown deletes it.
const environmentRow = (at, e) => h("li", { class: "card-row environment-row" }, icon("globe"),
  h("div", { class: "card-row-text" },
    h("div", { class: "card-row-title" }, h("a", { class: "environment-name", href: at.environment(e.id), "data-nav": "" }, e.target.displayName),
      healthBadge(e.health), syncBadge(e, e.latest), icon("git-branch"), h("a", { class: "environment-branch", href: at.branchPage(e.branch, "environment"), "data-nav": "" }, e.branch)),
    h("div", { class: "card-row-meta environment-meta" }, e.url ? [h("span", {}, e.url), "·"] : null, e.revision ? sha(at, e.revision) : null,
      h("span", { class: e.latest && e.latest.state === "failed" ? "tone-danger" : null }, shortStanding(e)))),
  h("div", { class: "card-row-actions" }, e.url && !(e.latest && e.latest.kind === "teardown" && !ended(e.latest))
    ? h("a", { class: "btn btn-sm", href: e.url, target: "_blank", rel: "noopener", title: "Open " + e.url }, icon("globe"), "Open") : null));

// environmentsCard lists the repository's environments, by target and then branch.
export function environmentsCard(at, envs) {
  const targets = new Set(envs.map((e) => e.target.id)).size;
  return h("section", { class: "card" }, h("div", { class: "card-header" }, h("h2", { class: "card-title" }, "Environments"),
    h("span", { class: "card-meta" }, envs.length + " across " + plural(targets, "target"))),
  envs.length ? h("ul", { class: "list" }, envs.map((e) => environmentRow(at, e)))
    : h("p", { class: "card-body card-note" }, "No environments yet. Each target makes one for every branch it matches, on that branch's first push."));
}

// targetsCard lists the targets main's /cobranch.yaml declares, their variables unexpanded.
export function targetsCard(at, targets) {
  const row = (t) => h("tr", {}, h("td", { class: "environment-target" }, t.name), h("td", { class: "cell-mono" }, t.branches.join(", ")),
    h("td", {}, clusterName(t)), h("td", { class: "cell-mono" }, t.namespace), h("td", { class: "cell-mono", title: t.url || null }, t.url || ""));
  return h("section", { class: "card targets-card" }, h("div", { class: "card-header" }, h("h2", { class: "card-title" }, "Targets")),
    h("table", { class: "table targets-table" }, h("thead", {}, h("tr", {}, ["Target", "Branches", "Cluster", "Namespace", "URL"].map((c) => h("th", {}, c)))),
      h("tbody", {}, targets.map(row))),
    h("p", { class: "card-body card-note" }, h("span", {}, "Declared under ", h("code", {}, "delivery"), " in ", h("a", { href: at.blob("main", "cobranch.yaml"), "data-nav": "" },
      h("code", {}, "/cobranch.yaml")), " on main. Change the file on main to add, rename or drop a target; a branch’s own targets count for nothing.")));
}

// emptyCentred is an Empty state, centred on the page: an icon in a sunken circle, a title and what would fill it.
export const emptyCentred = (glyph, title, text) => h("section", { class: "empty-centred" }, h("div", { class: "empty" },
  h("div", { class: "empty-icon" }, icon(glyph)), h("h2", { class: "empty-title" }, title), h("p", { class: "empty-description" }, text)));

// environmentsOf draws a branch's environments, as its Environment tab does: a card each, or why it has none.
export async function environmentsOf(ctx, b, envs) {
  if (!envs.length) {
    return [emptyCentred("globe", "No environment", "No target in /cobranch.yaml on main matches " + b.name +
      ". Add its pattern to a target’s branches on main to deliver this branch.")];
  }
  return Promise.all(envs.map((e) => environmentCard(ctx, e, { head: b.head })));
}
