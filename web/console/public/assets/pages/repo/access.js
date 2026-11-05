import { go } from "../../lib/router.js";

export const under = "settings";
export const heading = false;
// Access is a tab of Settings now (pages/repo/settings/access.js): its old address opens it.
export default ({ org, repo }) => {
  go("/" + org.slug + "/" + repo.slug + "/settings/access", true);
  return [];
};
