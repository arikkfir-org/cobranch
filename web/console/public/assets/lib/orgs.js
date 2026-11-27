// The viewer's organizations, as the Console holds them live: it reads them as it starts, then asks the REST API again
// with the list it holds, and the API answers once the list changes (an organization created, joined, left, renamed or
// deleted elsewhere, or a role granted or revoked), so the organization switcher lists them as they are. A tab out of
// sight stops asking until it shows again, and its first ask then answers at once with what changed meanwhile.

import { answer } from "./api.js";
import { config, setOrgs } from "./state.js";

// tag is the ETag of the list the Console holds, null before the first read.
let tag = null;

// read reads the viewer's organizations into the Console's state, waiting up to wait seconds for them to change from
// those it holds, and answers whether they changed.
async function read(wait) {
  const headers = { Accept: "application/json" };
  if (tag) headers["If-None-Match"] = tag;
  const r = await fetch(config.restUrl + "/v1/me/organizations" + (wait ? "?wait=" + wait : ""), { credentials: "include", headers });
  if (r.status === 304) return false;
  const next = r.headers.get("ETag");
  setOrgs((await answer(r)).organizations);
  tag = next;
  return true;
}

// readOrganizations reads the viewer's organizations again now, as a page does once it changed them.
export const readOrganizations = () => read(0);

// followOrganizations keeps the viewer's organizations as they are until the session ends, asking again a second after
// a read that fails, then longer, at most every minute.
export async function followOrganizations() {
  let delay = 1000;
  for (;;) {
    while (document.hidden) await new Promise((resolve) => document.addEventListener("visibilitychange", resolve, { once: true }));
    try {
      await read(15);
      delay = 1000;
    } catch (err) {
      if (err.status === 401) return;
      await new Promise((resolve) => setTimeout(resolve, delay));
      delay = Math.min(delay * 2, 60000);
    }
  }
}
