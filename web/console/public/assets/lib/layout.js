import { logo } from "./brand.js";
import { app, h } from "./dom.js";
import { base, config, me } from "./state.js";

export function showCard(...children) {
  document.body.className = "centered";
  app.replaceChildren(h("main", { class: "card" }, h("h1", { class: "brand" }, logo()), ...children));
}

export function signOut() {
  fetch(config.authUrl + "/sign-out", { method: "POST", credentials: "include" }).finally(() => location.replace(base));
}

export function notFound(slug) {
  showCard(h("h2", {}, "No such organization"), h("p", {}, "There is no organization ", h("code", {}, slug), " that you may see."), h("a", { class: "button", href: base, "data-nav": "" }, "Back"));
}

// viewerLinks are the bar's links of the viewer's own: their settings, under their name, and signing out.
export const viewerLinks = () => [h("a", { href: base + "settings", "data-nav": "", title: "Your settings, personal tokens and SSH keys" }, me.displayName),
  h("button", { class: "button small", type: "button", onclick: signOut }, "Sign out")];
