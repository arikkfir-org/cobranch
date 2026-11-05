import { api } from "../../../lib/api.js";
import { h } from "../../../lib/dom.js";
import { size } from "../../../lib/format.js";
import { icon } from "../../../lib/icons.js";
import { base } from "../../../lib/state.js";

export const tab = { label: "Usage", order: 70 };
export const view = "organization.usage";
export default ({ org, main }) => showUsage(org, main);

// bytes is a size in binary units, as limits are written: 1 GiB, 2.5 MiB.
const bytes = (n) => size(n).replace(".0 ", " ");

// sectionHeader is a section's title over the line on what it holds.
const sectionHeader = (title, description) => h("header", { class: "section-header" },
  h("div", { class: "section-header-text" }, h("h2", { class: "section-header-title" }, title), h("p", { class: "section-header-description" }, description)));

// meter is a share of a whole as a bar, which warns at 90% and turns to danger at the whole, when tones is set.
function meter(label, share, tones) {
  const tone = !tones ? "" : share >= 1 ? "danger" : share >= 0.9 ? "warning" : "";
  return h("div", { class: "meter", role: "meter", "aria-label": label, "aria-valuemin": "0", "aria-valuemax": "100", "aria-valuenow": String(Math.round(share * 100)), "data-tone": tone },
    h("span", { style: "width: " + Math.min(100, share * 100).toFixed(1) + "%" }));
}

// gauge is one limit's row: what is used against the limit, on a meter; a limit of 0 caps nothing, and shows no meter.
function gauge(label, usage, format) {
  const head = h("div", { class: "gauge-head" }, h("span", { class: "gauge-label" }, label),
    h("span", { class: "gauge-value" }, usage.limit ? format(usage.used) + " of " + format(usage.limit) : format(usage.used) + " · no limit"));
  const share = usage.limit ? usage.used / usage.limit : 0;
  const note = !usage.limit ? null : share >= 1 ? "At its limit: what would add more is refused. An administrator can raise it." : share >= 0.9 ? "Near its limit." : null;
  return h("div", { class: "card-row" }, h("div", { class: "gauge" }, head, usage.limit ? meter(label, share, true) : null, note ? h("span", { class: "gauge-note" }, note) : null));
}

// showUsage is what the organization takes of what its limits cap, for anyone who reads it, and the Git data of each
// repository the viewer reads, largest first, with its share of the organization's.
async function showUsage(org, main) {
  const usage = await api("GET", "/orgs/" + org.slug + "/usage");
  const list = [...usage.byRepository].sort((a, b) => b.gitData - a.gitData || a.slug.localeCompare(b.slug));
  const unread = usage.repositories.used - list.length;
  const whole = usage.gitData.used;
  const row = (r) => {
    const share = whole ? r.gitData / whole : 0;
    return h("tr", {},
      h("td", {}, h("a", { class: "cell-person cell-mono", href: base + org.slug + "/" + r.slug, "data-nav": "" }, icon("folder-git-2"), r.slug)),
      h("td", { class: "cell-number" }, bytes(r.gitData)),
      h("td", {}, h("div", { class: "share" }, meter("Share of " + r.slug, share, false), h("span", { class: "share-value" }, Math.round(share * 100) + "%"))));
  };
  main.replaceChildren(
    sectionHeader("Limits", "What the organization uses against the limits this installation sets."),
    h("section", { class: "card" }, gauge("Repositories", usage.repositories, String), gauge("Git data", usage.gitData, bytes)),
    sectionHeader("Git data per repository", "Each repository's packs as the object store keeps them, and its share of the organization's."),
    list.length ? h("table", { class: "table" }, h("thead", {}, h("tr", {}, h("th", {}, "Repository"), h("th", { class: "cell-number" }, "Total"), h("th", {}, "Share"))), h("tbody", {}, list.map(row)))
      : h("div", { class: "card" }, h("div", { class: "empty" }, h("div", { class: "empty-icon" }, icon("folder-git-2")), h("h2", { class: "empty-title" }, "No repositories yet"),
        h("p", { class: "empty-description" }, "Their Git data shows here once there are some."))),
    unread > 0 ? h("p", { class: "usage-unread" }, (unread === 1 ? "1 repository you can't read counts" : unread + " repositories you can't read count") + " toward these too.") : "");
}
