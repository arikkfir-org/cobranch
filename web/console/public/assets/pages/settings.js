import { api } from "../lib/api.js";
import { logo } from "../lib/brand.js";
import { app, h } from "../lib/dom.js";
import { when } from "../lib/format.js";
import { viewerLinks } from "../lib/layout.js";
import { base, permissions } from "../lib/state.js";
import { setTheme, theme, themes } from "../lib/theme.js";

// The viewer's own settings: their theme, their personal tokens, which act as them wherever a password or a bearer
// token goes, and their SSH keys, which act as them over SSH.
export async function showMySettings(shown = null) {
  const bar = h("header", { class: "bar" }, h("a", { class: "brand", href: base, "data-nav": "" }, logo()),
    h("nav", {}, h("a", { href: base + "settings", "data-nav": "", class: "current" }, "Your settings")), h("span", { class: "spacer" }), ...viewerLinks());
  const main = h("main", { class: "page" });
  document.body.className = "";
  app.replaceChildren(bar, main);
  const error = h("p", { class: "alert", hidden: true });
  const fail = (err) => { error.textContent = err.message; error.hidden = false; };
  const refresh = (created) => showMySettings(created || null).catch(fail);
  const [tokens, keys] = await Promise.all([api("GET", "/me/tokens").then((d) => d.tokens), api("GET", "/me/ssh-keys").then((d) => d.sshKeys)]);
  const state = (t) => (new Date(t.expiresAt) <= Date.now() ? "expired " + when(t.expiresAt) : "expires " + when(t.expiresAt)) +
    (t.lastUsedAt ? ", last used " + when(t.lastUsedAt) : ", never used");
  const row = (t) => h("tr", {}, h("td", {}, t.name, h("p", {}, "made " + when(t.createdAt))),
    h("td", {}, h("div", { class: "perms" }, t.permissions.map((p) => h("code", {}, p)))),
    h("td", {}, h("code", {}, "…" + t.hint), h("p", { class: "muted" }, state(t))),
    h("td", { class: "actions" }, h("button", { class: "button small danger", type: "button", onclick: () => api("DELETE", "/me/tokens/" + t.id).then(() => refresh()).catch(fail) }, "Revoke")));
  const sections = [h("h1", {}, "Your settings"), error, themeSection()];
  if (shown) sections.push(revealToken(shown));
  sections.push(h("section", { class: "panel" }, h("h3", {}, "Personal tokens"),
    h("p", {}, "A personal token acts as you, narrowed to the permissions it lists: it holds only what both its list and your roles give. Git asks for one as your password over HTTPS, and scripts send it as ", h("code", {}, "Authorization: Bearer …"), ". Every token ends within a year; revoke one the moment you stop using it."),
    tokens.length ? h("table", {}, h("thead", {}, h("tr", {}, h("th", {}, "Token"), h("th", {}, "Permissions"), h("th", {}, "Secret"), h("th", {}))), h("tbody", {}, tokens.map(row)))
      : h("p", {}, "No personal tokens yet.")));
  const name = h("input", { name: "name", required: true, maxlength: 100, placeholder: "Laptop's Git" });
  const picks = permissions.map((p) => h("label", {}, h("input", { type: "checkbox", value: p, checked: p === "repo.read" || p === "repo.push" }), h("code", {}, p)));
  const lifetime = tokenLifetime();
  sections.push(h("section", { class: "panel" }, h("h3", {}, "Make a token"),
    h("form", { onsubmit: (e) => {
      e.preventDefault();
      const chosen = picks.map((l) => l.firstChild).filter((c) => c.checked).map((c) => c.value);
      if (chosen.length === 0) return fail(new Error("Pick at least one permission for the token."));
      api("POST", "/me/tokens", { name: name.value, permissions: chosen, ...lifetime.value() }).then(refresh).catch(fail);
    } }, h("label", {}, "Name", h("span", {}, "What it's for, so you know which to revoke."), name),
      h("label", {}, "Permissions", h("span", {}, "Reading and pushing to repositories is what Git needs."), h("div", { class: "picks" }, picks)), lifetime.el,
      h("div", {}, h("button", { class: "button primary", type: "submit" }, "Make the token")))));
  sections.push(...sshKeySections(keys, refresh, fail));
  main.replaceChildren(...sections);
}

// themeSection picks the viewer's theme: light, dark, or the system's, which follows their operating system.
function themeSection() {
  const names = { light: "Light", dark: "Dark", system: "System" };
  const pick = (t) => h("label", { class: "check" }, h("input", { type: "radio", name: "theme", value: t, checked: theme() === t, onchange: () => setTheme(t) }), names[t]);
  return h("section", { class: "panel" }, h("h3", {}, "Theme"),
    h("p", {}, "Light or dark, or System to follow your operating system's setting as it changes."),
    h("div", { class: "row start" }, themes.map(pick)));
}

// sshKeySections are the settings' SSH keys: the viewer's, each with its fingerprint and a button that deletes it,
// and the form that adds one.
function sshKeySections(keys, refresh, fail) {
  const row = (k) => h("tr", {}, h("td", {}, k.name, h("p", {}, "added " + when(k.createdAt))),
    h("td", {}, h("code", {}, k.fingerprint), h("p", { class: "muted" }, k.lastUsedAt ? "last used " + when(k.lastUsedAt) : "never used")),
    h("td", { class: "actions" }, h("button", { class: "button small danger", type: "button", onclick: () => api("DELETE", "/me/ssh-keys/" + k.id).then(() => refresh()).catch(fail) }, "Delete")));
  const name = h("input", { name: "name", maxlength: 100, placeholder: "Laptop" });
  const publicKey = h("textarea", { name: "publicKey", required: true, rows: 3, placeholder: "ssh-ed25519 AAAA… you@laptop" });
  return [h("section", { class: "panel" }, h("h3", {}, "SSH keys"),
    h("p", {}, "An SSH key acts as you over SSH, with everything your roles give: Git signs in with it instead of a password. It works until you delete it."),
    keys.length ? h("table", {}, h("thead", {}, h("tr", {}, h("th", {}, "Key"), h("th", {}, "Fingerprint"), h("th", {}))), h("tbody", {}, keys.map(row)))
      : h("p", {}, "No SSH keys yet.")),
  h("section", { class: "panel" }, h("h3", {}, "Add an SSH key"),
    h("form", { onsubmit: (e) => {
      e.preventDefault();
      api("POST", "/me/ssh-keys", { name: name.value, publicKey: publicKey.value }).then(() => refresh()).catch(fail);
    } }, h("label", {}, "Public key", h("span", {}, "The line of your .pub file: Ed25519, ECDSA, or RSA of 2048 bits or more."), publicKey),
      h("label", {}, "Name", h("span", {}, "What it's for; the key's comment when empty."), name),
      h("div", {}, h("button", { class: "button primary", type: "submit" }, "Add the key"))))];
}

// revealToken shows a personal token just made, which the API never shows again.
function revealToken(created) {
  const code = h("code", {}, created.secret);
  const copy = h("button", { class: "button small", type: "button", onclick: () => {
    navigator.clipboard.writeText(created.secret).then(() => { copy.textContent = "Copied"; }).catch(() => { getSelection().selectAllChildren(code); });
  } }, "Copy");
  return h("section", { class: "panel notice" }, h("h3", {}, "Your new token"),
    h("p", {}, "Copy ", h("strong", {}, created.token.name), " now: it is shown here and never again. Give it to Git as your password, with any user name."),
    h("div", { class: "row" }, code, copy));
}

// tokenLifetime chooses when a personal token ends: in 30 days (the default), 7 or 90 days, a year, or on a date
// within a year.
function tokenLifetime() {
  const day = 24 * 60 * 60 * 1000;
  const local = (t) => { const d = new Date(t); return d.getFullYear() + "-" + String(d.getMonth() + 1).padStart(2, "0") + "-" + String(d.getDate()).padStart(2, "0"); };
  const date = h("input", { type: "date", hidden: true, min: local(Date.now() + day), max: local(Date.now() + 365 * day) });
  const pick = h("select", { onchange: () => { date.hidden = pick.value !== "date"; date.required = !date.hidden; } },
    h("option", { value: "30" }, "Expires in 30 days"), h("option", { value: "7" }, "Expires in 7 days"), h("option", { value: "90" }, "Expires in 90 days"),
    h("option", { value: "365" }, "Expires in a year"), h("option", { value: "date" }, "Expires on a date…"));
  const el = h("label", {}, "Expiry", pick, date);
  const value = () => pick.value === "30" ? {} : pick.value === "date" ? { expiresAt: new Date(date.value + "T00:00:00").toISOString() }
    : { expiresAt: new Date(Date.now() + Number(pick.value) * day).toISOString() };
  return { el, value };
}
