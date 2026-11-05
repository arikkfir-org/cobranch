import { orgPages, repoPages } from "./assets.js";
import { showOrganization } from "./organization.js";
import { base, config, follows, me, orgs } from "./state.js";
import * as help from "../pages/help.js";
import { showMySettings } from "../pages/settings.js";
import { showSignIn } from "../pages/sign-in.js";
import { showWelcome } from "../pages/welcome.js";

// The Console's own paths, which no organization's slug is.
const reserved = ["sign-in", "new", "join", "invitations", "me", "settings", "help"];
// go navigates within the Console; render draws the page for the path.
export function go(path, replace) {
  history[replace ? "replaceState" : "pushState"](null, "", base + path.replace(/^\//, ""));
  show("route");
}

// The page views the Console times for the installation's metrics: how long each page took to show and how many
// Git reads its API calls took, as their answers' Server-Timing names them. Nothing names who viewed what. view is
// the one in flight; drawing numbers the renders, so only the last one of a view, after any redirect, ends it.
let view = null;
let drawing = 0;
const views = [];
// show draws the page for the path as a page view: timed from the browser's navigation start for the Console's
// first load, from now for a move inside it. A move that starts while another is in flight, as a redirect does,
// continues that one's wait. A page that fails to draw reports nothing.
export function show(navigation) {
  const v = view || (view = { navigation, started: navigation === "load" ? 0 : performance.now(), reads: 0 });
  const n = ++drawing;
  let drawn;
  try {
    drawn = Promise.resolve(render());
  } catch (err) {
    drawn = Promise.reject(err);
  }
  drawn.then(() => {
    if (n !== drawing) return;
    view = null;
    const durationMs = Math.round(performance.now() - v.started);
    if (durationMs > 600000) return; // a tab left in the background
    views.push({ page: pageOf(route()), navigation: v.navigation, durationMs, gitReads: v.reads });
    if (views.length >= 20) reportViews();
  }, (err) => {
    if (n === drawing) view = null;
    throw err;
  });
}
// countReads adds the Git reads an answer names in Server-Timing to the view in flight.
export function countReads(r) {
  const timing = view && r.headers.get("Server-Timing");
  if (timing) view.reads += timing.split(",").filter((entry) => entry.split(";")[0].trim() === "git").length;
}
// pageOf names the page at the route's parts, as the REST API's page views do: by its module's view, a name or a
// function of the parts after the page's own, or else organization. or repository. and the page's part of the route.
function pageOf(parts) {
  if (!me) return "sign-in";
  const [org, page = "", sub = "", ...rest] = parts;
  if (org === "settings") return "settings";
  if (org === "help") return help.view(parts.slice(1));
  if (!org || reserved.includes(org)) return "welcome";
  const named = (m, name) => typeof m.view === "function" ? m.view(rest) : m.view || name;
  if (orgPages.has(page)) return named(orgPages.get(page), "organization." + page);
  return repoPages.has(sub) ? named(repoPages.get(sub), "repository." + sub) : "repository.code";
}
// reportViews sends the views kept so far, 20 at a time, with keepalive so a tab that closes still sends them, and
// without the session's cookie, which the route doesn't take. A report that fails is dropped: the numbers are a
// sample.
function reportViews() {
  while (views.length) {
    const body = JSON.stringify({ views: views.splice(0, 20) });
    fetch(config.restUrl + "/v1/page-views", { method: "POST", credentials: "omit", keepalive: true, headers: { "Content-Type": "application/json" }, body }).catch(() => {});
  }
}
setInterval(reportViews, 30000);
document.addEventListener("visibilitychange", () => { if (document.visibilityState === "hidden") reportViews(); });
// route is the page's path under the Console's root: [] for its home, [slug] or [slug, page] for an organization's,
// [slug, repository] or [slug, repository, page, ...what it shows] for a repository's, each part decoded.
function route() {
  const rest = location.pathname.slice(base.length).replace(/\/$/, "");
  const decode = (part) => { try { return decodeURIComponent(part); } catch { return part; } };
  return rest ? rest.split("/").map(decode) : [];
}
window.addEventListener("popstate", () => show("route"));
document.addEventListener("click", (e) => {
  const a = e.target.closest("a[data-nav]");
  if (a && !e.metaKey && !e.ctrlKey) {
    e.preventDefault();
    go(a.getAttribute("href").slice(base.length - 1));
  }
});

export function render() {
  follows(null);
  const parts = route();
  if (!me) return showSignIn();
  if (parts.length === 0) {
    const remembered = (() => { try { return localStorage.getItem("cobranch.org"); } catch { return null; } })();
    const first = orgs.find((o) => o.slug === remembered) || orgs[0];
    if (first) return go("/" + first.slug, true);
    return showWelcome();
  }
  if (parts[0] === "new") return showWelcome();
  if (parts[0] === "settings") return showMySettings();
  if (parts[0] === "help") return help.showHelp(parts.slice(1));
  if (reserved.includes(parts[0])) return showWelcome();
  return showOrganization(parts[0], parts[1] || "", parts[2] || "", parts.slice(3));
}
