import { api } from "../../../lib/api.js";
import { h } from "../../../lib/dom.js";
import { size } from "../../../lib/format.js";
import { base } from "../../../lib/state.js";

export const tab = { label: "Usage", order: 70 };
export const view = "organization.usage";
export default ({ org, main }) => showUsage(org, main);

// bytes is a size in binary units, as limits are written: 1 GiB, 2.5 MiB.
const bytes = (n) => size(n).replace(".0 ", " ");

// gauge is one limit's usage: what is used against the limit, on a meter that warns at 90% and turns to danger at
// the limit; a limit of 0 caps nothing, and shows no meter.
function gauge(label, usage, format) {
  const head = h("div", { class: "gauge-head" }, h("span", { class: "gauge-label" }, label),
    h("span", { class: "gauge-value" }, usage.limit ? format(usage.used) + " of " + format(usage.limit) : format(usage.used) + " · no limit"));
  if (!usage.limit) return h("div", { class: "gauge" }, head);
  const share = usage.used / usage.limit;
  const tone = share >= 1 ? "danger" : share >= 0.9 ? "warning" : "";
  const meter = h("div", { class: "meter", role: "meter", "aria-label": label, "aria-valuemin": "0", "aria-valuemax": String(usage.limit),
    "aria-valuenow": String(usage.used), "data-tone": tone }, h("span", { style: "width: " + Math.min(100, share * 100).toFixed(1) + "%" }));
  const note = tone === "danger" ? "At its limit: what would add more is refused. An administrator can raise it."
    : tone === "warning" ? "Near its limit." : null;
  return h("div", { class: "gauge" }, head, meter, note ? h("span", { class: "gauge-note" }, note) : null);
}

// showUsage is what the organization takes of what its limits cap, for anyone who reads it, with each repository the
// viewer reads and its Git data, largest first.
async function showUsage(org, main) {
  const usage = await api("GET", "/orgs/" + org.slug + "/usage");
  const list = [...usage.byRepository].sort((a, b) => b.gitData - a.gitData || a.slug.localeCompare(b.slug));
  const unread = usage.repositories.used - list.length;
  const row = (r) => h("tr", {}, h("td", {}, h("a", { class: "cell-mono", href: base + org.slug + "/" + r.slug, "data-nav": "" }, r.slug)), h("td", { class: "cell-number" }, bytes(r.gitData)));
  main.replaceChildren(
    h("section", { class: "card" }, h("header", { class: "card-header" }, h("h2", { class: "card-title" }, "Limits")),
      h("div", { class: "card-body" }, gauge("Repositories", usage.repositories, String), gauge("Git data", usage.gitData, bytes))),
    list.length ? h("table", { class: "table" }, h("thead", {}, h("tr", {}, h("th", {}, "Repository"), h("th", { class: "cell-number" }, "Git data"))), h("tbody", {}, list.map(row))) : "",
    unread > 0 ? h("p", { class: "usage-unread" }, (unread === 1 ? "1 repository you can't read counts" : unread + " repositories you can't read count") + " toward these too.") : "");
}
