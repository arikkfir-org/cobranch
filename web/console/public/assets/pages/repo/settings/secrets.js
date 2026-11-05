import { h } from "../../../lib/dom.js";
import { secretsSections } from "../../../lib/secrets.js";

export const tab = { label: "Secrets", order: 20 };
export const allowed = (can) => can("secrets.manage");

// The repository's own secrets, which its jobs receive beside the organization's; who reads each is said where it
// changes, in Change.
export default async ({ org, repo, path, orgCan, fail }) => [h("div", { class: "repo-secrets" },
  await secretsSections(org, path + "/secrets", orgCan("org.grants.manage"), fail, [repo], null, {
    readers: false,
    description: ["Values this repository's jobs read by ID, from ", h("code", {}, "/cobranch.yaml"), ", beside the organization's. A value is sealed as it arrives, never shown again; change it to replace it."],
  }))];
