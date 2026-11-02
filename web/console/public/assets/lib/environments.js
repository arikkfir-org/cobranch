import { api } from "./api.js";
import { h } from "./dom.js";
import { capitalized, shortSha } from "./format.js";
import { badgeOf } from "./icons.js";

// The delivery states, and the environments' syncs and healths, drawn as the run states are.
export const deliveryStates = {
  waiting: { word: "Waiting", icon: "circle-pause", tone: "neutral" },
  queued: { word: "Queued", icon: "clock", tone: "neutral" },
  rendering: { word: "Rendering", icon: "loader-circle", tone: "info" },
  applying: { word: "Applying", icon: "loader-circle", tone: "info" },
  deleting: { word: "Deleting", icon: "loader-circle", tone: "info" },
  succeeded: { word: "Succeeded", icon: "circle-check", tone: "success" },
  failed: { word: "Failed", icon: "circle-x", tone: "danger" },
  cancelled: { word: "Cancelled", icon: "ban", tone: "neutral" },
};
export const environmentSyncs = {
  synced: { word: "Synced", icon: "circle-check", tone: "success" },
  out_of_sync: { word: "Out of sync", icon: "circle-dashed", tone: "neutral" },
};
export const environmentHealths = {
  healthy: { word: "Healthy", icon: "circle-check", tone: "success" },
  progressing: { word: "Progressing", icon: "loader-circle", tone: "info" },
  unknown: { word: "Unknown", icon: "circle", tone: "neutral" },
  missing: { word: "Missing", icon: "circle-dashed", tone: "danger" },
  degraded: { word: "Degraded", icon: "octagon-alert", tone: "danger" },
};

// environmentsPanel lists the repository's environments, of branch unless it is empty, by target and then branch:
// each with its sync and health, where it delivers, the commit its cluster holds, and its newest delivery.
export async function environmentsPanel(path, at, branch) {
  const list = (await api("GET", path + "/environments" + (branch ? "?" + new URLSearchParams({ branch }) : ""))).environments;
  const rows = list.map((e) => h("tr", {},
    h("td", { class: "state" }, badgeOf(environmentSyncs, e.sync), " ", badgeOf(environmentHealths, e.health)),
    h("td", {}, h("a", { href: at.environment(e.id), "data-nav": "" }, h("code", {}, e.target.displayName), branch ? null : [" of ", h("code", {}, e.branch)]),
      e.paused ? [" ", h("span", { class: "chip" }, "Paused")] : null,
      h("p", { class: "muted" }, deliversTo(e), " · ", e.revision ? ["at ", h("a", { href: at.commit(e.revision), "data-nav": "" }, h("code", {}, shortSha(e.revision)))] : "nothing delivered yet"),
      e.latest ? h("p", { class: "muted" }, "Latest: #" + e.latest.number + " ", badgeOf(deliveryStates, e.latest.state), e.latest.reason ? " " + capitalized(e.latest.reason) : null) : null)));
  const none = h("p", {}, branch ? "This branch delivers to no environment. " : "No environments yet. ", "Each target that ", h("code", {}, "/cobranch.yaml"),
    " declares under ", h("code", {}, "delivery"), " on ", h("code", {}, "main"), " makes one for every branch it matches, on that branch's first push.");
  return h("section", { class: "panel" }, h("h3", {}, "Environments"), rows.length ? h("table", { class: "environments" }, h("tbody", {}, rows)) : none);
}

// deliversTo says where an environment delivers: its namespace, in its target's cluster or this installation's.
export const deliversTo = (e) => ["Namespace ", h("code", {}, e.namespace), e.cluster ? [" of cluster ", h("code", {}, e.cluster)] : " of this installation's cluster"];
