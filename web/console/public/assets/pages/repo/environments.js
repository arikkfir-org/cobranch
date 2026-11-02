import { api, apiPage } from "../../lib/api.js";
import { h } from "../../lib/dom.js";
import { deliversTo, deliveryStates, environmentHealths, environmentSyncs, environmentsPanel } from "../../lib/environments.js";
import { capitalized, shortSha, whenExactly } from "../../lib/format.js";
import { badgeOf } from "../../lib/icons.js";
import { render } from "../../lib/router.js";

export const nav = { label: "Environments", order: 30 };
export const accepts = (rest) => rest.length <= 1;
export default async ({ path, at, rest, can, fail }) => rest.length ? repositoryEnvironment(path, at, rest[0], can, fail) : [await environmentsPanel(path, at, "")];

// repositoryEnvironment is an environment's page: its target and branch, where it delivers, its sync, health and
// revision, Redeliver, Pause and Resume for a viewer holding delivery.run, and its deliveries.
async function repositoryEnvironment(path, at, id, can, fail) {
  let e;
  try {
    e = await api("GET", path + "/environments/" + encodeURIComponent(id));
  } catch (err) {
    if (err.status !== 404 && err.status !== 400) throw err;
    return [h("section", { class: "panel" }, h("h3", {}, "Environment"), h("p", {}, "This repository has no such environment."))];
  }
  const facts = h("dl", { class: "facts" },
    h("dt", {}, "Branch"), h("dd", {}, h("a", { href: at.branchPage(e.branch), "data-nav": "" }, h("code", {}, e.branch))),
    h("dt", {}, "Delivers to"), h("dd", {}, deliversTo(e)),
    h("dt", {}, "State"), h("dd", {}, badgeOf(environmentSyncs, e.sync), " ", badgeOf(environmentHealths, e.health), e.paused ? [" ", h("span", { class: "chip" }, "Paused")] : null),
    h("dt", {}, "Revision"), h("dd", {}, e.revision ? [h("a", { href: at.commit(e.revision), "data-nav": "" }, h("code", {}, e.revision)), " since ", whenExactly(e.syncedAt)] : "Nothing delivered yet"),
    h("dt", {}, "Created"), h("dd", {}, whenExactly(e.createdAt)));
  const title = h("h2", {}, h("code", {}, e.target.displayName), " of ", h("code", {}, e.branch));
  return [h("section", { class: "panel" }, h("div", { class: "row" }, title, h("div", {}, environmentActions(path, e, can, fail))), facts),
    await deliveriesPanel(path, at, e, can, fail)];
}

// environmentActions are Redeliver and Pause for an environment that isn't paused, and Resume for one that is, for a
// viewer holding delivery.run.
function environmentActions(path, e, can, fail) {
  if (!can("delivery.run")) return null;
  const act = (verb, button) => {
    button.disabled = true;
    api("POST", path + "/environments/" + e.id + "/" + verb).then(render).catch((err) => { button.disabled = false; fail(err); });
  };
  const button = (verb, label, tone, title) => h("button", { class: "button small" + tone, type: "button", title, onclick: (ev) => act(verb, ev.currentTarget) }, label);
  const name = e.target.displayName + " of " + e.branch;
  if (e.paused) return button("resume", "Resume", " primary", "Resume " + name + " and deliver the head of " + e.branch);
  return [button("redeliver", "Redeliver", " primary", "Deliver the head of " + e.branch + " to " + name + " again"), " ",
    button("pause", "Pause", "", "Pause " + name + ": it takes no delivery until resumed")];
}

// deliveriesPanel lists an environment's deliveries, newest first, a page at a time: each with its state and why, its
// commit, what queued it and when, and Start while it waits, for a viewer holding delivery.run.
async function deliveriesPanel(path, at, e, can, fail) {
  const rows = h("tbody");
  let next = null;
  const older = h("button", { class: "button small", type: "button", hidden: true, onclick: () => load(next) }, "Older deliveries");
  const start = (d, button) => {
    button.disabled = true;
    api("POST", path + "/deliveries/" + d.number + "/start").then(render).catch((err) => { button.disabled = false; fail(err); });
  };
  const row = (d) => h("tr", {},
    h("td", { class: "state" }, badgeOf(deliveryStates, d.state)),
    h("td", {}, "#" + d.number + " ", d.kind === "teardown" ? "Teardown"
      : [d.kind === "resync" ? "Resync of " : null, h("a", { href: at.commit(d.sha), "data-nav": "" }, h("code", {}, shortSha(d.sha)))],
      d.reason ? h("p", {}, capitalized(d.reason)) : null, h("p", { class: "muted" }, queuedBy(d), " · ", whenExactly(d.queuedAt))),
    h("td", { class: "actions" }, can("delivery.run") && d.state === "waiting" ? h("button", { class: "button small primary", type: "button",
      title: "Start #" + d.number + ", delivering " + shortSha(d.sha) + " to " + e.target.displayName, onclick: (ev) => start(d, ev.currentTarget) }, "Start") : null));
  async function load(from) {
    older.disabled = true;
    try {
      const page = await apiPage(from);
      rows.append(...page.data.deliveries.map(row));
      next = page.next;
      older.hidden = !next;
    } catch (err) {
      fail(err);
    } finally {
      older.disabled = false;
    }
  }
  await load(path + "/environments/" + e.id + "/deliveries");
  const none = h("p", {}, "No deliveries yet. A push to ", h("code", {}, e.branch), " delivers it.");
  return h("section", { class: "panel" }, h("h3", {}, "Deliveries"), rows.children.length ? h("table", { class: "deliveries" }, rows) : none, h("div", {}, older));
}

// queuedBy says what queued a delivery, and who started it.
const queuedBy = (d) => {
  const by = d.startedBy ? d.startedBy.displayName : "someone since gone";
  if (d.trigger === "redeliver") return "Redelivered by " + by;
  if (d.trigger === "resume") return "Resumed by " + by;
  if (d.trigger === "schedule") return "The controller's schedule";
  return "A push" + (d.startedBy ? ", started by " + by : "");
};
