import { h } from "./dom.js";

export const when = (at) => new Date(at).toLocaleDateString(undefined, { dateStyle: "medium" });
export const whenExactly = (at) => new Date(at).toLocaleString(undefined, { dateStyle: "medium", timeStyle: "short" });
export const shortSha = (sha) => sha.slice(0, 7);
export const subject = (message) => message.split("\n")[0] || "(no message)";
// who is a commit's author or committer by the name the commit gives, with the user behind them when they differ.
export const who = (person) => person.principal && person.principal.displayName !== person.name
  ? [person.name, " ", h("span", { class: "chip", title: "The user who holds " + person.email }, person.principal.displayName)] : person.name;

// size is a file's size as people read it.
export function size(bytes) {
  if (bytes < 1024) return bytes + " B";
  const units = ["KiB", "MiB", "GiB", "TiB"];
  let n = bytes / 1024, u = 0;
  while (n >= 1024 && u < units.length - 1) { n /= 1024; u++; }
  return n.toFixed(n < 10 ? 1 : 0) + " " + units[u];
}
export const plural = (n, word) => n + " " + word + (n === 1 ? "" : "s");

export const capitalized = (text) => text.charAt(0).toUpperCase() + text.slice(1);

// ago is how long ago a moment was, as a conversation says it: just now, 14 min, 2 h, yesterday, then its date.
export function ago(at) {
  const minutes = Math.floor((Date.now() - new Date(at).getTime()) / 60000);
  if (minutes < 1) return "just now";
  if (minutes < 60) return minutes + " min";
  if (minutes < 24 * 60) return Math.floor(minutes / 60) + " h";
  return minutes < 48 * 60 ? "yesterday" : when(at);
}
