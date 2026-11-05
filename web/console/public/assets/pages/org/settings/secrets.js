import { api } from "../../../lib/api.js";
import { failure } from "../../../lib/forms.js";
import { secretsSections } from "../../../lib/secrets.js";

export const tab = { label: "Secrets", order: 50 };
export const allowed = (can) => can("secrets.manage");
export const view = "organization.secrets";
export default ({ org, main, can, actions }) => showOrgSecrets(org, main, can, actions);

// showOrgSecrets lists the organization's own secrets, which its repositories' jobs share.
async function showOrgSecrets(org, main, can, actions) {
  const error = failure();
  const repos = can("org.grants.manage") ? (await api("GET", "/orgs/" + org.slug + "/repos")).repositories : [];
  main.replaceChildren(error.el, ...await secretsSections(org, "/orgs/" + org.slug + "/secrets", can("org.grants.manage"), error.fail, repos, actions));
}
