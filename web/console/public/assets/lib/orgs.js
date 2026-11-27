// The viewer's organizations, as the Console holds them live: it reads them as it starts, then asks the REST API again
// with the list it holds, and the API answers once the list changes (an organization created, joined, left, renamed or
// deleted elsewhere, or a role granted or revoked), so the organization switcher lists them as they are. A tab out of
// sight stops asking until it shows again, and its first ask then answers at once with what changed meanwhile.

import { pause, readTagged, shown } from "./live.js";
import { setOrgs } from "./state.js";

// tag is the ETag of the list the Console holds, null before the first read.
let tag = null;

// read reads the viewer's organizations into the Console's state, waiting up to wait seconds for them to change from
// those it holds, and answers whether they changed.
async function read(wait) {
  const got = await readTagged("/me/organizations", tag, wait);
  if (!got) return false;
  setOrgs(got.data.organizations);
  tag = got.tag;
  return true;
}

// readOrganizations reads the viewer's organizations again now, as a page does once it changed them.
export const readOrganizations = () => read(0);

// followOrganizations keeps the viewer's organizations as they are until the session ends, asking again a second after
// a read that fails, then longer, at most every minute.
export async function followOrganizations() {
  let delay = 1000;
  for (;;) {
    await shown();
    try {
      await read(15);
      delay = 1000;
    } catch (err) {
      if (err.status === 401) return;
      delay = await pause(delay);
    }
  }
}
