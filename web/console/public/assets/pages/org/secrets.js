import { api } from "../../lib/api.js";
import { h } from "../../lib/dom.js";
import { secretsSections } from "../../lib/secrets.js";

export const nav = { label: "Secrets", order: 70 };
export const allowed = (can) => can("secrets.manage");
export default ({ org, main, can }) => showOrgSecrets(org, main, can);

// showOrgSecrets renders the organization's own secrets, which its repositories' jobs share.
async function showOrgSecrets(org, main, can) {
  const error = h("p", { class: "alert", hidden: true });
  const fail = (err) => { error.textContent = err.message; error.hidden = false; };
  const repos = can("org.grants.manage") ? (await api("GET", "/orgs/" + org.slug + "/repos")).repositories : [];
  main.replaceChildren(h("h1", {}, "Secrets"), error, ...await secretsSections(org, "/orgs/" + org.slug + "/secrets", can("org.grants.manage"), fail, repos));
}
