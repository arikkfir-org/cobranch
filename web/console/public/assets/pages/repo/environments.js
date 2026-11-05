import { api } from "../../lib/api.js";
import { branchSubject } from "../../lib/chat.js";
import { h } from "../../lib/dom.js";
import { emptyCentred, environmentCard, environmentsCard, targetsCard } from "../../lib/environments.js";
import { icon } from "../../lib/icons.js";

export const nav = { label: "Delivery", order: 30 };
export const accepts = (rest) => rest.length <= 1;
export const heading = () => true;
export const crumbs = ({ root, rest }) => [{ label: "Delivery", href: root + "/environments" }, ...(rest.length ? [{ label: "Environment" }] : [])];
// subject is an environment's branch's conversation, on the environment's page.
export const subject = ({ org, repo, path, at, rest, can }) => rest.length ? api("GET", path + "/environments/" + encodeURIComponent(rest[0]))
  .then((e) => (e.branch ? branchSubject(org, repo, path, at, e.branch, can) : null)) : null;
export default (ctx) => ctx.rest.length ? environmentPage(ctx, ctx.rest[0]) : deliveryPage(ctx);

// deliveryPage is the repository's Delivery tab: its environments and the targets main's /cobranch.yaml declares, or
// how to declare them when it declares none.
async function deliveryPage({ path, at }) {
  const [envs, targets] = await Promise.all([api("GET", path + "/environments").then((d) => d.environments), api("GET", path + "/targets").then((d) => d.targets)]);
  const none = emptyCentred("globe", "No delivery targets", "Declare targets under delivery in /cobranch.yaml on main: each names the branches it delivers, " +
    "a cluster and a namespace. Every live branch a target matches then gets an environment.");
  if (!targets.length) return envs.length ? [environmentsCard(at, envs), none] : [none];
  // Environments in the order main declares their targets, as the Targets card lists them; an environment whose target
  // main no longer declares, until it is torn down, last.
  const order = new Map(targets.map((t, i) => [t.name, i]));
  const rank = (e) => (order.has(e.target.displayName) ? order.get(e.target.displayName) : targets.length);
  envs.sort((a, b) => rank(a) - rank(b));
  return [environmentsCard(at, envs), targetsCard(at, targets)];
}

// environmentPage is an environment's page: its card, the full width, under a button back to Delivery.
async function environmentPage(ctx, id) {
  const back = h("a", { class: "btn btn-ghost", href: ctx.root + "/environments", "data-nav": "" }, icon("arrow-left"), "Delivery");
  let e;
  try {
    e = await api("GET", ctx.path + "/environments/" + encodeURIComponent(id));
  } catch (err) {
    if (err.status !== 404 && err.status !== 400) throw err;
    return [h("div", { class: "environment-page" }, back, emptyCentred("globe", "No such environment", "This repository has no such environment, or it was torn down."))];
  }
  // Whether its branch still is, which says why a teardown deletes it.
  const live = await api("GET", ctx.path + "/branches/" + encodeURIComponent(e.branch)).then(() => true, (err) => {
    if (err.status === 404) return false;
    throw err;
  });
  return [h("div", { class: "environment-page" }, back, await environmentCard(ctx, e, { page: true, live }))];
}
