// The person's theme: light, dark, or system, which follows the operating system's. tokens.css draws dark under
// data-theme="dark" on <html>, and under the system's dark preference unless data-theme="light"; index.html sets the
// attribute from this browser's copy of the choice before the Console's first paint, so no page flashes the other one.
// The choice itself is kept with the person (their preferences), so every browser they sign in from follows it.

import { api } from "./api.js";

const key = "cobranch.theme";
const changed = new Set();
export const themes = ["light", "dark", "system"];

// theme is the person's choice, as this browser last drew it.
export function theme() {
  try {
    const t = localStorage.getItem(key);
    if (themes.includes(t)) return t;
  } catch { /* a convenience */ }
  return "system";
}

// draw draws choice at once and keeps it in this browser, for its next first paint.
function draw(choice) {
  if (choice === "system") delete document.documentElement.dataset.theme;
  else document.documentElement.dataset.theme = choice;
  try { localStorage.setItem(key, choice); } catch { /* a convenience */ }
  changed.forEach((f) => f());
}

// followPreferences reads the signed-in person's preferences and draws their theme when this browser's copy is
// another, as when they chose it in another browser. It never fails: the copy stands in.
export async function followPreferences() {
  try {
    const p = await api("GET", "/me/preferences");
    if (themes.includes(p.theme) && p.theme !== theme()) draw(p.theme);
  } catch { /* the browser's copy stands in */ }
}

// setTheme draws the person's choice at once and saves it with them, the latest choice winning over one made
// meanwhile elsewhere; it rejects with the API's problem when the save fails, the choice still drawn here.
export function setTheme(choice) {
  if (!themes.includes(choice)) throw new Error("There is no theme " + choice + ".");
  draw(choice);
  return api("PATCH", "/me/preferences", { theme: choice }, { "Content-Type": "application/merge-patch+json", "If-Match": "*" });
}

// onThemeChange calls f whenever the colours change: the person chooses another theme, or the system's changes while
// they follow it. What draws with the colours outside the page's stylesheets, such as a rendered document's frame,
// draws again.
export function onThemeChange(f) {
  changed.add(f);
}
matchMedia("(prefers-color-scheme: dark)").addEventListener("change", () => changed.forEach((f) => f()));
