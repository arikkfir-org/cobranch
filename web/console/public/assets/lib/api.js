import { countReads } from "./router.js";
import { config } from "./state.js";

// api calls the REST API with the session cookie, a body as JSON unless headers name another type, and rejects with
// the problem it answered; signal, when given, abandons the call.
export async function api(method, path, body, headers = {}, signal = undefined) {
  const options = { method, credentials: "include", headers: { Accept: "application/json", ...headers }, signal };
  if (body !== undefined) {
    options.headers["Content-Type"] = headers["Content-Type"] || "application/json";
    options.body = JSON.stringify(body);
  }
  const r = await fetch(config.restUrl + "/v1" + path, options);
  countReads(r);
  if (r.status === 204) return null;
  const data = await r.json().catch(() => ({}));
  if (!r.ok) throw Object.assign(new Error(data.detail || data.title || r.statusText), { status: r.status, code: data.code });
  return data;
}
// apiPage reads a page of a list the REST API pages, at the API's path or at the URL a previous page linked, and
// the URL of the next page, as its Link names it: null after the last.
export async function apiPage(pathOrUrl) {
  const url = new URL(pathOrUrl.startsWith("/") ? config.restUrl + "/v1" + pathOrUrl : pathOrUrl, location.href).href;
  const r = await fetch(url, { credentials: "include", headers: { Accept: "application/json" } });
  countReads(r);
  const data = await r.json().catch(() => ({}));
  if (!r.ok) throw Object.assign(new Error(data.detail || data.title || r.statusText), { status: r.status, code: data.code });
  const next = /<([^>]*)>\s*;\s*rel="next"/.exec(r.headers.get("Link") || "");
  return { data, next: next ? new URL(next[1], url).href : null };
}
