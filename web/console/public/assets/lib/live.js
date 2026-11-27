// Lists the Console shows live: it reads a list with its ETag, then asks the REST API again with that ETag and a wait,
// and the API answers once the list changes, or 304 when the wait ends without a change. A tab out of sight stops
// asking until it shows again, and its first ask then answers at once with what changed meanwhile.

import { answer } from "./api.js";
import { config, follows } from "./state.js";

// readTagged reads the list at the API's path, waiting up to wait seconds for it to differ from the one tag names, and
// answers its data and ETag, or null when it is still the one tag names; signal, when given, abandons the read.
export async function readTagged(path, tag = null, wait = 0, signal = undefined) {
  const headers = { Accept: "application/json" };
  if (tag) headers["If-None-Match"] = tag;
  const query = wait ? (path.includes("?") ? "&" : "?") + "wait=" + wait : "";
  const r = await fetch(config.restUrl + "/v1" + path + query, { credentials: "include", headers, signal });
  if (r.status === 304) return null;
  const next = r.headers.get("ETag");
  return { data: await answer(r), tag: next };
}

// shown waits until the tab shows.
export async function shown() {
  while (document.hidden) await new Promise((resolve) => document.addEventListener("visibilitychange", resolve, { once: true }));
}

// pause waits delay milliseconds before asking again after a failure, and answers the next delay: twice as long, at
// most a minute.
export async function pause(delay) {
  await new Promise((resolve) => setTimeout(resolve, delay));
  return Math.min(delay * 2, 60000);
}

// keepingFilter draws the page again with draw, keeping what the viewer typed in its filter, and the filter's focus: a
// page that follows its lists draws itself again once one changes, which may be while the viewer filters it.
export async function keepingFilter(draw) {
  const old = document.querySelector('input[type="search"]');
  const query = old ? old.value : "";
  const focused = !!old && document.activeElement === old;
  await draw();
  const filter = document.querySelector('input[type="search"]');
  if (!filter) return;
  if (query) {
    filter.value = query;
    filter.dispatchEvent(new Event("input"));
  }
  if (focused) filter.focus();
}

// followLists follows the lists a page shows, each a path and the ETag of what the page read, while still answers
// true, and calls changed once one of them changes, and then stops: a page draws itself again then, reading them
// again and following them anew. A list the viewer may no longer read stops it too, without changed, and so does the
// Console drawing another page, or the page following anew, which abandon the reads in flight.
export async function followLists(lists, still, changed) {
  const stop = new AbortController();
  follows(stop);
  const follow = async ([path, tag]) => {
    let delay = 1000;
    while (!stop.signal.aborted) {
      await shown();
      if (stop.signal.aborted || !still()) return false;
      try {
        if (await readTagged(path, tag, 15, stop.signal)) return true;
        delay = 1000;
      } catch (err) {
        if (stop.signal.aborted || [401, 403, 404].includes(err.status)) return false;
        delay = await pause(delay);
      }
    }
    return false;
  };
  const followed = lists.map((list) => follow(list).then((change) => { if (change) stop.abort(); return change; }));
  if ((await Promise.all(followed)).some(Boolean) && still()) changed();
}
