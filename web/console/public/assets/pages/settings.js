import { api } from "../lib/api.js";
import { ask, dialog } from "../lib/dialog.js";
import { h } from "../lib/dom.js";
import { avatar, icon } from "../lib/icons.js";
import { pageHeader, signOut, themeSwitch } from "../lib/layout.js";
import { me, orgs, permissions } from "../lib/state.js";

// The viewer's own pages, which the avatar menu opens: Account at /settings, and Preferences at
// /settings/preferences.
export const view = "settings";
export const crumbs = ({ rest }) => [{ label: rest[0] === "preferences" ? "Preferences" : "Account" }];
export default ({ main, rest }) => (rest[0] === "preferences" ? showPreferences(main) : showAccount(main));

const day = 24 * 60 * 60 * 1000;

// card is B's Card with its header: a title, a count, and an action.
const card = (title, meta, action, ...body) => h("section", { class: "card" },
  h("header", { class: "card-header" }, h("h2", { class: "card-title" }, title), meta ? h("span", { class: "card-meta" }, meta) : null, action || null), ...body);

// button is B's small Button, with its icon before its label.
const button = (glyph, label, onclick) => h("button", { class: "btn btn-sm", type: "button", onclick }, icon(glyph), h("span", {}, label));

// ago is how long ago a moment was, as a table of the viewer's things says it: 2 minutes ago, 3 days ago, 2 weeks
// ago, 4 months ago.
function ago(at) {
  const words = new Intl.RelativeTimeFormat(undefined, { numeric: "auto" });
  const seconds = (new Date(at).getTime() - Date.now()) / 1000;
  const units = [[60, "second"], [60, "minute"], [24, "hour"], [7, "day"], [4.35, "week"], [12, "month"]];
  let n = seconds;
  for (const [size, unit] of units) {
    if (Math.abs(n) < size) return unit === "second" ? "just now" : words.format(Math.round(n), unit);
    n /= size;
  }
  return words.format(Math.round(n), "year");
}

// showPreferences draws the viewer's preferences: their theme, light, dark, or the system's, which follows their
// operating system. The theme is kept with the person (lib/theme.js), so every browser they sign in from follows it,
// as the avatar menu's switch does; a save that fails is said above it (Preferences · Save failed), the choice still
// drawn here, and choosing again retries it.
function showPreferences(main) {
  const error = h("div", { class: "banner banner-danger", role: "alert", hidden: true }, icon("circle-x"),
    h("div", { class: "banner-text" }, h("span", { class: "banner-title" }, "Your theme wasn't saved"),
      h("p", {}, "Cobranch could not keep the theme with your account. It stays as you set it here; choose it again to retry.")),
    h("button", { class: "icon-btn icon-btn-ghost icon-btn-sm", type: "button", "aria-label": "Dismiss", title: "Dismiss", onclick: () => { error.hidden = true; } }, icon("x")));
  const saved = (err) => { error.hidden = !err; };
  main.replaceChildren(pageHeader({ title: "Preferences", description: "How Cobranch looks for you, in every browser you sign in from." }), error,
    card("Appearance", null, null, h("div", { class: "preference" },
      h("div", { class: "row-text" }, h("span", { class: "row-title" }, "Theme"),
        h("span", { class: "row-meta" }, "System follows your device; Light and Dark override it. The same switch is in the avatar menu.")),
      themeSwitch(saved))));
}

// showAccount draws the viewer's account: who they are, their personal tokens, which act as them wherever a password
// or a bearer token goes, and their SSH keys, which act as them over SSH. made is a token just made, whose secret it
// shows once.
async function showAccount(main, made = null) {
  const error = h("div", { class: "banner banner-danger", role: "alert", hidden: true }, icon("circle-x"), h("div", { class: "banner-text" }, h("p", {})));
  const fail = (err) => { error.querySelector("p").textContent = err.message; error.hidden = false; };
  const refresh = (created) => showAccount(main, created || null).catch(fail);
  const [tokens, keys] = await Promise.all([api("GET", "/me/tokens").then((d) => d.tokens), api("GET", "/me/ssh-keys").then((d) => d.sshKeys)]);
  main.replaceChildren(pageHeader({ title: "Account", description: "Who you are to Cobranch, the tokens that act as you, and the keys that push as you. Your name and email come from sign-in." }),
    ...[error, made ? revealToken(made) : null, profile(), tokensCard(tokens, refresh, fail), keysCard(keys, refresh, fail)].filter(Boolean));
}

// profile is who the viewer is: their name and email as sign-in gave them, and the organizations they are in.
function profile() {
  const email = me.emails && me.emails.length ? me.emails[0].email : "";
  const fact = (name, value) => h("tr", {}, h("td", { class: "cell-muted" }, name), h("td", {}, value));
  const member = orgs.length ? orgs.map((o) => o.name).join(", ") : "No organization yet";
  return card("Profile", null, button("log-out", "Log out", signOut),
    h("table", { class: "table facts" }, h("tbody", {},
      fact("Name", h("div", { class: "cell-person" }, avatar(me.displayName, false, 32),
        h("div", { class: "row-text" }, h("span", { class: "row-title" }, me.displayName), email ? h("span", { class: "row-meta" }, email) : null))),
      fact("Member of", member))));
}

// tokensCard lists the viewer's personal tokens, each with what it may do, when it was made and last used, when it
// ends and a button that revokes it, and opens the form that makes one.
function tokensCard(tokens, refresh, fail) {
  const scope = (t) => permissions.length && permissions.every((p) => t.permissions.includes(p)) ? "full access" : t.permissions.join(", ");
  const ends = (t) => {
    const left = new Date(t.expiresAt).getTime() - Date.now();
    return left <= 0 ? h("span", { class: "badge-quiet tone-danger" }, "Expired") : left < day ? "within a day" : "in " + Math.floor(left / day) + " days";
  };
  const revoke = (t) => ask("Revoke " + t.name + "?", "Whatever uses it stops working at once. This can't be undone.", "Revoke",
    () => api("DELETE", "/me/tokens/" + t.id).then(() => refresh()));
  const row = (t) => h("tr", {}, h("td", {}, h("div", { class: "row-text" }, h("code", { class: "cell-mono" }, t.name), h("span", { class: "row-meta" }, scope(t)))),
    h("td", {}, ago(t.createdAt)), h("td", {}, t.lastUsedAt ? ago(t.lastUsedAt) : "never"), h("td", {}, ends(t)),
    h("td", {}, h("button", { class: "icon-btn icon-btn-ghost icon-btn-sm", type: "button", "aria-label": "Revoke " + t.name, title: "Revoke", onclick: () => revoke(t).catch(fail) }, icon("x"))));
  return card("Personal tokens", tokens.length === 1 ? "1 token" : tokens.length + " tokens", button("plus", "New token", () => newToken(refresh)),
    tokens.length ? h("table", { class: "table listing" }, h("thead", {}, h("tr", {}, ["Token", "Created", "Last used", "Expires"].map((t) => h("th", {}, t)), h("th", { "aria-label": "Actions" }))),
      h("tbody", {}, tokens.map(row)))
      : empty("key-round", "No personal tokens", "A token acts as you, narrowed to the permissions it lists: Git asks for one as your password over HTTPS, and scripts send it as a bearer token."));
}

// keysCard lists the viewer's SSH keys, each with its fingerprint, when it was added and a button that deletes it,
// and opens the form that adds one.
function keysCard(keys, refresh, fail) {
  const remove = (k) => ask("Delete " + k.name + "?", "Git over SSH no longer signs in with it. This can't be undone.", "Delete",
    () => api("DELETE", "/me/ssh-keys/" + k.id).then(() => refresh()));
  const row = (k) => h("tr", {}, h("td", {}, k.name), h("td", { class: "cell-mono cell-muted" }, k.fingerprint), h("td", {}, ago(k.createdAt)),
    h("td", {}, h("button", { class: "icon-btn icon-btn-ghost icon-btn-sm", type: "button", "aria-label": "Delete " + k.name, title: "Delete", onclick: () => remove(k).catch(fail) }, icon("x"))));
  return card("SSH keys", keys.length === 1 ? "1 key" : keys.length + " keys", button("plus", "Add key", () => addKey(refresh)),
    keys.length ? h("table", { class: "table listing" }, h("thead", {}, h("tr", {}, ["Key", "Fingerprint", "Added"].map((t) => h("th", {}, t)), h("th", { "aria-label": "Actions" }))),
      h("tbody", {}, keys.map(row)))
      : empty("key-round", "No SSH keys", "An SSH key acts as you over SSH, with everything your roles give: Git signs in with it instead of a password."));
}

// empty is B's Empty state in a card: what a list with nothing in it would hold.
const empty = (glyph, title, description) => h("div", { class: "empty" }, h("div", { class: "empty-icon" }, icon(glyph)),
  h("h2", { class: "empty-title" }, title), h("p", { class: "empty-description" }, description));

// field is B's Input field: its label, the control, and help under it.
const field = (label, control, help) => h("div", { class: "field" }, h("label", { class: "field-label", for: control.id || null }, label), control, help ? h("p", { class: "field-help" }, help) : null);

// newToken opens the form that makes a personal token: its name, its permissions, and when it ends, and shows the
// token made, once.
function newToken(refresh) {
  const name = h("input", { class: "input", id: "token-name", maxlength: 100, placeholder: "Laptop's Git" });
  const picks = permissions.map((p) => h("label", { class: "checkbox" }, h("input", { type: "checkbox", value: p, checked: p === "repo.read" || p === "repo.push" }), h("code", {}, p)));
  const lifetime = tokenLifetime();
  dialog("New personal token", [
    h("p", {}, "A token acts as you, with only what both its permissions and your roles give. Every token ends within a year."),
    field("Name", name, "What it's for, so you know which to revoke."),
    h("div", { class: "field" }, h("span", { class: "field-label" }, "Permissions"), h("div", { class: "picks" }, picks), h("p", { class: "field-help" }, "Reading and pushing to repositories is what Git needs.")),
    lifetime.el,
  ], "Make the token", "btn-primary", () => {
    const chosen = picks.map((l) => l.firstChild).filter((c) => c.checked).map((c) => c.value);
    if (!name.value.trim()) throw new Error("Name the token, so you know which to revoke.");
    if (chosen.length === 0) throw new Error("Pick at least one permission for the token.");
    return api("POST", "/me/tokens", { name: name.value.trim(), permissions: chosen, ...lifetime.value() }).then(refresh);
  });
  name.focus();
}

// addKey opens the form that adds an SSH key: its public key and its name.
function addKey(refresh) {
  const publicKey = h("textarea", { class: "textarea", id: "key-public", rows: 3, placeholder: "ssh-ed25519 AAAA… you@laptop" });
  const name = h("input", { class: "input", id: "key-name", maxlength: 100, placeholder: "Laptop" });
  dialog("Add an SSH key", [
    field("Public key", publicKey, "The line of your .pub file: Ed25519, ECDSA, or RSA of 2048 bits or more."),
    field("Name", name, "What it's for; the key's comment when empty."),
  ], "Add the key", "btn-primary", () => {
    if (!publicKey.value.trim()) throw new Error("Paste the public key: the line of your .pub file.");
    return api("POST", "/me/ssh-keys", { name: name.value.trim(), publicKey: publicKey.value.trim() }).then(() => refresh());
  });
  publicKey.focus();
}

// revealToken shows a personal token just made, which the API never shows again.
function revealToken(created) {
  const code = h("code", {}, created.secret);
  const copy = h("button", { class: "btn btn-ghost btn-sm", type: "button", onclick: () => {
    navigator.clipboard.writeText(created.secret).then(() => { copy.textContent = "Copied"; }).catch(() => { getSelection().selectAllChildren(code); });
  } }, "Copy");
  return h("div", { class: "banner banner-success", role: "status" }, icon("circle-check"),
    h("div", { class: "banner-text" }, h("span", { class: "banner-title" }, "Copy " + created.token.name + " now"),
      h("p", {}, "It is shown here and never again. Give it to Git as your password, with any user name: ", code)), copy);
}

// tokenLifetime chooses when a personal token ends: in 30 days (the default), 7 or 90 days, a year, or on a date
// within a year.
function tokenLifetime() {
  const local = (t) => { const d = new Date(t); return d.getFullYear() + "-" + String(d.getMonth() + 1).padStart(2, "0") + "-" + String(d.getDate()).padStart(2, "0"); };
  const date = h("input", { class: "input", type: "date", "aria-label": "Expiry date", hidden: true, min: local(Date.now() + day), max: local(Date.now() + 365 * day) });
  const pick = h("select", { id: "token-expiry", onchange: () => { date.hidden = pick.value !== "date"; } },
    h("option", { value: "30" }, "In 30 days"), h("option", { value: "7" }, "In 7 days"), h("option", { value: "90" }, "In 90 days"),
    h("option", { value: "365" }, "In a year"), h("option", { value: "date" }, "On a date…"));
  const el = h("div", { class: "field" }, h("label", { class: "field-label", for: "token-expiry" }, "Expires"), h("div", { class: "select" }, pick), date);
  const value = () => {
    if (pick.value === "30") return {};
    if (pick.value !== "date") return { expiresAt: new Date(Date.now() + Number(pick.value) * day).toISOString() };
    if (!date.value) throw new Error("Pick the day the token ends.");
    return { expiresAt: new Date(date.value + "T00:00:00").toISOString() };
  };
  return { el, value };
}
