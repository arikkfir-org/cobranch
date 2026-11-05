import { go } from "../../lib/router.js";

// Roles is a tab of Settings now (pages/org/settings/roles.js): its old address opens it.
export default ({ org }) => go("/" + org.slug + "/settings/roles" + location.search + location.hash, true);
