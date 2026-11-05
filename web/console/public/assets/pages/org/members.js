import { go } from "../../lib/router.js";

// Members is a tab of Settings now (pages/org/settings/members.js): its old address opens it.
export default ({ org }) => go("/" + org.slug + "/settings/members" + location.search + location.hash, true);
