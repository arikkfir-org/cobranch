import { secretsSections } from "../../lib/secrets.js";

export const nav = { label: "Secrets", order: 50 };
export const crumbs = [{ label: "Secrets" }];
export const allowed = (can) => can("secrets.manage");

// The repository's own secrets, which its jobs receive.
export default ({ org, repo, path, orgCan, fail }) => secretsSections(org, path + "/secrets", orgCan("org.grants.manage"), fail, [repo]);
