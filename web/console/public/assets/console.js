// The Console's entry, which index.html imports from the Console's root and release, as the page names them: its
// stylesheets and pages, where the APIs are, and who the viewer is, then the page for the path.

import { answer, api } from "./lib/api.js";
import { load } from "./lib/assets.js";
import { h } from "./lib/dom.js";
import { showCard } from "./lib/layout.js";
import { show } from "./lib/router.js";
import { setBase, setConfig, setMe, setOrgs, setPermissions } from "./lib/state.js";

// unavailable says the Console couldn't load its pages.
const unavailable = () => showCard(h("span", { class: "status" }, "Console unavailable"), h("p", {}, "The Console couldn't load its pages. Try again in a moment."));

// start draws the Console from its configuration (a promise), which names its files and holds the viewer's call, at
// root, the Console's root; when its pages don't load, again() loads the Console again, or answers false once it tried
// enough.
export function start(loaded, root, again) {
  setBase(root);
  return loaded.then(async (c) => {
    // A page of a release before this one names no files: this release's page does, once the Console loads again.
    if (!c.files) return again() || unavailable();
    const assets = load(c.files).then(() => true, () => false);
    setConfig({ restUrl: c.restUrl || "", authUrl: c.authUrl || "", gitUrl: (c.gitUrl || "").replace(/\/$/, ""), gitSshUrl: (c.gitSshUrl || "").replace(/\/$/, "") });
    try {
      setMe(await answer(c.me));
      const [orgs, permissions] = await Promise.all([api("GET", "/me/organizations").then((d) => d.organizations), api("GET", "/permissions?scope=organization").then((d) => d.permissions)]);
      setOrgs(orgs);
      setPermissions(permissions);
    } catch (err) {
      if (err.status !== 401) throw err;
    }
    if (!(await assets)) return again() || unavailable();
    show("load");
  }).catch(() => showCard(h("span", { class: "status" }, "REST API unreachable"), h("p", {}, "The Console couldn't reach this installation's REST API. Try again in a moment.")));
}
