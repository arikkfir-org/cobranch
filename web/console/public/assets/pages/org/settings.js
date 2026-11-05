import { tabPages } from "../../lib/assets.js";
import { pageHeading } from "../../lib/layout.js";
import { go } from "../../lib/router.js";
import { tabBar, tabsOf } from "../../lib/tabs.js";

// Settings: one page with tabs, always about the organization shown, each tab a module of pages/org/settings/
// (lib/tabs.js) at /<organization>/settings/<tab>, General (its index.js) at /<organization>/settings.
export const nav = { label: "Settings", order: 90, icon: "settings", place: "foot" };

// tabOf is the tab module at a part of the route after settings/, whether or not the viewer may open it.
const tabOf = (part) => (tabPages.get("org/settings") || new Map()).get(part || "");

export const crumbs = ({ root, sub }) => [{ label: "Settings", href: root + "/settings" }, ...(tabOf(sub)?.tab ? [{ label: tabOf(sub).tab.label }] : [])];
export const view = (after) => tabOf(after[0])?.view || "organization.settings";

// heading is Settings' title, what it holds, the slot a tab puts its actions in, and the tabs the viewer may open.
export const heading = ({ org, can, sub, root }) => {
  const head = pageHeading({ title: "Settings", actions: [],
    description: "Who is in " + org.name + " and what they may do, its API keys, agents, secrets, the models its agents run on, and what it uses." });
  head.append(tabBar(tabsOf("org/settings", can), sub || "", (p) => root + "/settings" + (p ? "/" + p : ""), { org, can }, "Settings"));
  return head;
};

// The tab the route names draws under the heading, given its actions' slot; a tab the viewer may not open, or none,
// opens General.
export default function show({ org, main, can, sub, rest, head }) {
  const shown = tabsOf("org/settings", can).find(([p]) => p === (sub || ""));
  if (!shown) return go("/" + org.slug + "/settings", true);
  return shown[1].default({ org, main, can, rest, actions: head.querySelector(".app-page-heading-actions") });
}
