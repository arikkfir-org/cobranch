import { go } from "../../lib/router.js";

export const under = "settings";
export const heading = false;
// Secrets is a tab of Settings now (pages/repo/settings/secrets.js): its old address opens it.
export default ({ org, repo }) => {
  go("/" + org.slug + "/" + repo.slug + "/settings/secrets", true);
  return [];
};
