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

// tone is the Meter's tone for a share of a limit: Warning from 80%, Danger at or over it.
const tone = (share) => share >= 1 ? " meter-danger" : share >= 0.8 ? " meter-warning" : "";

// bar is a Meter's bar, as wide as share.
const bar = (share) => h("span", { class: "meter-bar", style: "width: " + Math.min(100, share * 100).toFixed(1) + "%" });

// meterAria is what a screen reader hears of a meter: its label and the share it shows, in percent.
const meterAria = (label, share) => ({ role: "meter", "aria-label": label, "aria-valuemin": "0", "aria-valuemax": "100", "aria-valuenow": String(Math.round(share * 100)) });

// gauge is one limit's row: a Meter of what is used against the limit; a limit of 0 caps nothing, and draws no track.
function gauge(label, usage, format) {
  const share = usage.limit ? usage.used / usage.limit : 0;
  return h("div", { class: "card-row" }, h("div", { class: "meter" + tone(share) },
    h("div", { class: "meter-head" }, h("span", { class: "meter-label" }, label),
      h("span", { class: "meter-value" }, usage.limit ? format(usage.used) + " of " + format(usage.limit) : format(usage.used) + " · no limit")),
    usage.limit ? h("div", { class: "meter-track", ...meterAria(label, share) }, bar(share)) : null));
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
      h("td", {}, h("div", { class: "share" }, h("div", { class: "meter-share", ...meterAria("Share of " + r.slug, share) }, bar(share)),
        h("span", { class: "cell-muted" }, Math.round(share * 100) + "%"))));
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
