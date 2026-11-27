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

// watchLists asks the API again about the lists it is given, each a path and the ETag of what was read, while still
// answers true and until signal aborts, and answers true once one of them changes, abandoning the others' reads. A
// list read without an ETag isn't asked about, and a list the viewer may no longer read stops being asked about; it
// answers false once none is left to ask about. With gone, a list that is no longer there (404) changed too: a page
// of one thing, such as an issue, shows that it was deleted.
export async function watchLists(lists, still, signal, { gone = false } = {}) {
  const stop = new AbortController();
  const abort = () => stop.abort();
  signal.addEventListener("abort", abort, { once: true });
  const watch = async ([path, tag]) => {
    let delay = 1000;
    while (!stop.signal.aborted) {
      await shown();
      if (stop.signal.aborted || !still()) return false;
      try {
        if (await readTagged(path, tag, 15, stop.signal)) return true;
        delay = 1000;
      } catch (err) {
        if (stop.signal.aborted) return false;
        if (err.status === 404) return gone;
        if ([401, 403].includes(err.status)) return false;
        delay = await pause(delay);
      }
    }
    return false;
  };
  const tagged = lists.filter(([, tag]) => tag);
  const changed = await new Promise((resolve) => {
    let left = tagged.length;
    if (!left) resolve(false);
    tagged.forEach((list) => watch(list).then((change) => {
      left--;
      if (change || !left) resolve(change);
    }));
  });
  stop.abort();
  signal.removeEventListener("abort", abort);
  return changed && !signal.aborted;
}

// followLists follows the lists a page shows, each a path and the ETag of what the page read, while still answers
// true, and calls changed once one of them changes, and then stops: a page draws itself again then, reading them
// again and following them anew. A list the viewer may no longer read stops it too, without changed unless options
// say a list gone is a change (watchLists), and so does the Console drawing another page, or the page following anew,
// which abandon the reads in flight. It starts a task later, once the draw that called it has put its elements in the
// page, so that a part of a page may follow what it read before the page that awaits it shows it.
export async function followLists(lists, still, changed, options = {}) {
  const stop = new AbortController();
  follows(stop);
  await new Promise((resolve) => setTimeout(resolve));
  if (await watchLists(lists, still, stop.signal, options) && still()) changed();
}

// typed are the kinds of the page's fields that take text, its filter aside.
const typed = (el) => el.tagName === "TEXTAREA" || (el.tagName === "INPUT" && !["search", "checkbox", "radio", "button", "submit", "reset", "hidden"].includes(el.type));

// writing reports whether the viewer is in the middle of something a draw would lose: a dialog or a menu open, the
// focus in one of the page's fields that takes text other than its filter, or such a field holding what the viewer
// typed and hasn't saved, which differs from the value the page drew it with (its defaultValue).
function writing() {
  if (document.querySelector("dialog[open], .app-popover")) return true;
  const el = document.activeElement;
  if (el && el.closest("main.page") && (el.isContentEditable || el.tagName === "SELECT" || typed(el))) return true;
  return [...document.querySelectorAll("main.page input, main.page textarea")].some((f) => typed(f) && !f.disabled && f.value !== f.defaultValue);
}

// drawAgain draws a page again with draw once a list it follows changed, keeping what the viewer typed in its filter,
// once the viewer is no longer writing in it (writing); a draw that fails leaves the page as it was and is tried again
// a second later, then longer, while still answers true.
export async function drawAgain(draw, still) {
  let delay = 1000;
  while (still()) {
    while (writing() && still()) await new Promise((resolve) => setTimeout(resolve, 1000));
    if (!still()) return;
    try {
      await keepingFilter(draw);
      return;
    } catch {
      delay = await pause(delay);
    }
  }
}
