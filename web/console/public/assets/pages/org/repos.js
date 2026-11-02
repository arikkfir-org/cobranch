import { go } from "../../lib/router.js";

// repos, which no repository's slug is (repos.ReservedSlugs), opens the organization's home.
export default ({ org }) => go("/" + org.slug);
