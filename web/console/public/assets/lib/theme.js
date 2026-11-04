// The person's theme: light, dark, or system, which follows the operating system's. tokens.css draws dark under
// data-theme="dark" on <html>, and under the system's dark preference unless data-theme="light"; index.html sets the
// attribute from this browser's copy of the choice before the Console's first paint, so no page flashes the other one.

const key = "cobranch.theme";
const changed = new Set();
export const themes = ["light", "dark", "system"];

// theme is the person's choice.
export function theme() {
  try {
    const t = localStorage.getItem(key);
    if (themes.includes(t)) return t;
  } catch { /* a convenience */ }
  return "system";
}

// setTheme draws the person's choice at once and keeps it in this browser, for its next first paint.
export function setTheme(choice) {
  if (!themes.includes(choice)) throw new Error("There is no theme " + choice + ".");
  if (choice === "system") delete document.documentElement.dataset.theme;
  else document.documentElement.dataset.theme = choice;
  try { localStorage.setItem(key, choice); } catch { /* a convenience */ }
  changed.forEach((f) => f());
}

// onThemeChange calls f whenever the colours change: the person chooses another theme, or the system's changes while
// they follow it. What draws with the colours outside the page's stylesheets, such as a rendered document's frame,
// draws again.
export function onThemeChange(f) {
  changed.add(f);
}
matchMedia("(prefers-color-scheme: dark)").addEventListener("change", () => changed.forEach((f) => f()));
