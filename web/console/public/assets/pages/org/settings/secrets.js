import { api } from "../../../lib/api.js";
import { failure } from "../../../lib/forms.js";
import { followLists, keepingFilter } from "../../../lib/live.js";
import { render } from "../../../lib/router.js";
import { secretsSections } from "../../../lib/secrets.js";

export const tab = { label: "Secrets", order: 50 };
export const allowed = (can) => can("secrets.manage");
export const view = "organization.secrets";
export default ({ org, main, can, actions }) => showOrgSecrets(org, main, can, actions);

// showOrgSecrets lists the organization's own secrets, which its repositories' jobs share, and follows them while they
// show: once they change, here or elsewhere, it draws the page again.
async function showOrgSecrets(org, main, can, actions) {
  const error = failure();
  const path = "/orgs/" + org.slug + "/secrets";
  const repos = can("org.grants.manage") ? (await api("GET", "/orgs/" + org.slug + "/repos")).repositories : [];
  let tag = null;
  main.replaceChildren(error.el, ...await secretsSections(org, path, can("org.grants.manage"), error.fail, repos, actions, { read: (t) => { tag = t; } }));
  followLists([[path, tag]], () => error.el.isConnected, () => keepingFilter(render).catch(error.fail));
}
