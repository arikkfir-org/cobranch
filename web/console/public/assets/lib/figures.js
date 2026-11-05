// The figures a page counts, as the design system's components draw them (components.css): a Stat, and a Chart/Bars of
// a value by day. The Overview draws its figures with them, and /help/components its specimens.

import { h } from "./dom.js";
import { icon } from "./icons.js";

// day is a date of the browser's calendar as the API writes days, such as 2026-11-05.
export const day = (d) => [d.getFullYear(), d.getMonth() + 1, d.getDate()].map((n) => String(n).padStart(2, "0")).join("-");
// dayName is one of the API's days as the charts name it, such as 5 Nov.
const dayName = (date) => {
  const [y, m, d] = date.split("-").map(Number);
  return new Date(y, m - 1, d).toLocaleDateString("en-GB", { day: "numeric", month: "short" });
};

// stat is a Stat: its label and icon, its value, and the Badge/Quiet under it in tone.
export const stat = (label, glyph, value, sub, tone) => h("div", { class: "stat" }, h("span", { class: "stat-label" }, label, icon(glyph)),
  h("span", { class: "stat-value" }, value), h("span", { class: "badge-quiet tone-" + tone }, sub));

// bars is a Chart/Bars titled title in tone, with total in its header: a bar for each of days, as tall beside the
// highest as value makes it, which says "<day> · <says>" in its tooltip while pointed at or focused, and the first day
// and the last, Today when it is, under them.
export function bars(title, tone, total, days, value, says) {
  const most = Math.max(0, ...days.map(value));
  const tip = h("span", { class: "chart-bars-tooltip", hidden: true });
  const all = days.map((d) => {
    const bar = h("span", { class: "chart-bars-bar", tabindex: "0", role: "img", "aria-label": dayName(d.date) + " · " + says(d) });
    bar.style.height = most ? (100 * value(d)) / most + "%" : "0";
    return bar;
  });
  const point = (e) => {
    const bar = e.target.closest(".chart-bars-bar");
    if (!bar) return;
    tip.textContent = bar.getAttribute("aria-label");
    tip.style.left = bar.offsetLeft + bar.offsetWidth / 2 + "px";
    tip.style.top = "calc(" + bar.offsetTop + "px - var(--space-6))";
    tip.hidden = false;
  };
  const leave = () => { tip.hidden = true; };
  const plot = h("div", { class: "chart-bars-plot", onpointerover: point, onfocusin: point, onpointerleave: leave, onfocusout: leave }, all, tip);
  const last = days.length ? days[days.length - 1].date : "";
  return h("section", { class: "card chart-bars tone-" + tone },
    h("header", { class: "card-header" }, h("h2", { class: "card-title" }, title), h("span", { class: "card-meta" }, total)),
    plot, h("div", { class: "chart-bars-axis" }, h("span", {}, days.length ? dayName(days[0].date) : ""), h("span", {}, last === day(new Date()) ? "Today" : last && dayName(last))));
}
