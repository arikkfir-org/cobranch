// The Console's entry, which index.html imports from the Console's root once config.json named it: its stylesheets
// and pages, where the APIs are, and who the viewer is, then the page for the path.

import { api } from "./lib/api.js";
import { load } from "./lib/assets.js";
import { h } from "./lib/dom.js";
import { showCard } from "./lib/layout.js";
import { show } from "./lib/router.js";
import { setBase, setConfig, setMe, setOrgs, setPermissions } from "./lib/state.js";

// start draws the Console from config.json's answer (a promise), at root, the Console's root.
export function start(loaded, root) {
  setBase(root);
  const assets = load().then(() => true, () => false);
  return loaded.then(async (c) => {
    setConfig({ restUrl: c.restUrl || "", authUrl: c.authUrl || "", gitUrl: (c.gitUrl || "").replace(/\/$/, ""), gitSshUrl: (c.gitSshUrl || "").replace(/\/$/, "") });
    try {
      setMe(await api("GET", "/me"));
      const [orgs, permissions] = await Promise.all([api("GET", "/me/organizations").then((d) => d.organizations), api("GET", "/permissions?scope=organization").then((d) => d.permissions)]);
      setOrgs(orgs);
      setPermissions(permissions);
    } catch (err) {
      if (err.status !== 401) throw err;
    }
    if (!(await assets)) return showCard(h("span", { class: "status" }, "Console unavailable"), h("p", {}, "The Console couldn't load its pages. Try again in a moment."));
    show("load");
  }).catch(() => showCard(h("span", { class: "status" }, "REST API unreachable"), h("p", {}, "The Console couldn't reach this installation's REST API. Try again in a moment.")));
}
