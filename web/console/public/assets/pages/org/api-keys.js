import { go } from "../../lib/router.js";

// API keys is a tab of Settings now (pages/org/settings/api-keys.js): its old address opens it.
export default ({ org }) => go("/" + org.slug + "/settings/api-keys" + location.search + location.hash, true);
