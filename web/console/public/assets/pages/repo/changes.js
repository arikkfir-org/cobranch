import { go } from "../../lib/router.js";
import { base } from "../../lib/state.js";

export const under = "";
export const accepts = (rest) => rest.length > 0;
export const heading = false;
// A branch's changes were their own page here before they became its Files changed tab: the address opens the tab.
export default ({ at, rest }) => {
  go(at.changes(rest.join("/")).slice(base.length - 1), true);
  return [];
};
