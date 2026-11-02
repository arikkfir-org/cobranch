import { h } from "../lib/dom.js";
import { showCard } from "../lib/layout.js";
import { config } from "../lib/state.js";

// What a failed sign-in's error code means, by the code the Auth server sent us back with.
const signInErrors = {
  provider: "The identity provider didn't sign you in. Try again.",
  flow: "The sign-in took too long, or began in another browser. Try again.",
  refused: "This installation doesn't let you sign up. Ask its operator for an invitation.",
  email_taken: "Another account already holds your email, through a different identity provider. Sign in with that one.",
  unknown_provider: "That identity provider isn't set up here.",
  internal: "Something went wrong on our side. Try again in a moment.",
};

export async function showSignIn() {
  const card = [];
  const params = new URLSearchParams(location.search);
  const code = params.get("error");
  if (code) card.push(h("p", { class: "alert" }, signInErrors[code] || signInErrors.internal));
  if (!config.authUrl) {
    return showCard(h("h2", {}, "Sign in"), ...card, h("p", {}, "Sign-in isn't set up yet: this installation lists no identity provider. Its operator adds one to the chart's ", h("code", {}, "auth.providers"), "."));
  }
  try {
    const { providers, seeded } = await (await fetch(config.authUrl + "/providers", { headers: { Accept: "application/json" } })).json();
    if (providers.length === 0 && seeded.length === 0) card.push(h("p", {}, "Sign-in isn't set up yet: this installation lists no identity provider."));
    card.push(h("div", { class: "buttons" }, providers.map((p) => h("a", { class: "button primary", href: config.authUrl + "/sign-in?" + new URLSearchParams({ provider: p.id, return: "/" }) }, "Sign in with " + p.name))));
    if (seeded.length) {
      card.push(h("div", { class: "demo" }, h("p", {}, "Demo data is on: sign in as one of its users."),
        h("div", { class: "buttons" }, seeded.map((u) => h("a", { class: "button", href: config.authUrl + "/sign-in?" + new URLSearchParams({ user: u.subject, return: "/" }) }, u.displayName + " (" + u.subject + ")")))));
    }
  } catch {
    card.push(h("p", {}, "Sign-in isn't answering. Try again in a moment."));
  }
  showCard(h("h2", {}, "Sign in"), ...card);
}
