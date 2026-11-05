import { tabPages } from "../../lib/assets.js";
import { go } from "../../lib/router.js";
import { tabBar, tabsOf } from "../../lib/tabs.js";

// Settings: one of the repository's tabs, whose views are tabs too, each a module of pages/repo/settings/ (lib/tabs.js)
// at <repository>/settings/<tab>, General (its index.js) at <repository>/settings.
export const nav = { label: "Settings", order: 90 };
export const crumbs = [{ label: "Settings" }];

// tabOf is the tab module at a part of the route after settings/, whether or not the viewer may open it.
const tabOf = (part) => (tabPages.get("repo/settings") || new Map()).get(part || "");

// view names the page the API records a tab's views under: the page each tab was before it became one.
export const view = (rest) => ({ access: "repository.access", secrets: "repository.secrets" })[rest[0]] || "repository.settings";
export const allowed = (can) => tabsOf("repo/settings", can).length > 0;
export const accepts = (rest) => rest.length === 0 || (rest.length === 1 && !!tabOf(rest[0])?.tab);

// The tab the route names draws under the bar of Settings' tabs; one the viewer may not open opens the first they may.
export default async (ctx) => {
  const { org, repo, root, rest, can } = ctx;
  const tabs = tabsOf("repo/settings", can);
  const part = rest[0] || "";
  const shown = tabs.find(([p]) => p === part);
  if (!shown) {
    go("/" + org.slug + "/" + repo.slug + "/settings" + (tabs[0][0] ? "/" + tabs[0][0] : ""), true);
    return [];
  }
  return [tabBar(tabs, part, (p) => root + "/settings" + (p ? "/" + p : ""), ctx, "Settings"), ...await shown[1].default(ctx)];
};
