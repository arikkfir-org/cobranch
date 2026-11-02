import { secretsSections } from "../../lib/secrets.js";

export const nav = { label: "Secrets", order: 50 };
export const allowed = (can) => can("secrets.manage");
// The API's page views name no page of a repository's secrets yet: they count as the code's.
export const view = "repository.code";

// The repository's own secrets, which its jobs receive.
export default ({ org, repo, path, orgCan, fail }) => secretsSections(org, path + "/secrets", orgCan("org.grants.manage"), fail, [repo]);
