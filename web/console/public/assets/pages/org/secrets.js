import { go } from "../../lib/router.js";

// Secrets is a tab of Settings now (pages/org/settings/secrets.js): its old address opens it.
export default ({ org }) => go("/" + org.slug + "/settings/secrets" + location.search + location.hash, true);
