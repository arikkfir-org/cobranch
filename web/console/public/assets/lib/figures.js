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

// named is one of the API's days as a chart's axis or counter names it: Today when it is, else its day.
const named = (date) => (date === day(new Date()) ? "Today" : dayName(date));

// bars is a Chart/Bars titled title in tone, with total in its header: a bar for each of days, as tall beside the
// highest as value makes it, which says "<day> · <says>" in its tooltip while pointed at or focused, and the first day
// and the last, Today when it is, under them. While at most one day has a value, one bar would say nothing a number
// doesn't, so the chart shows that day's says as a counter, over its day, or over the days when none has one.
export function bars(title, tone, total, days, value, says) {
  const valued = days.filter((d) => value(d));
  return h("section", { class: "card chart-bars tone-" + tone },
    h("header", { class: "card-header" }, h("h2", { class: "card-title" }, title), h("span", { class: "card-meta" }, total)),
    valued.length > 1 || !days.length ? plot(days, value, says) : counter(valued[0], days, says));
}

// counter is a Chart/Bars' Single state: one's says, or the first of days' when no day has a value, over its day, or
// the span of days.
function counter(one, days, says) {
  const first = days[0].date, last = days[days.length - 1].date;
  const when = one ? named(one.date) : first === last ? named(first) : dayName(first) + " – " + named(last);
  return h("div", { class: "chart-bars-counter" }, h("span", { class: "chart-bars-counter-value" }, says(one || days[0])), h("span", {}, when));
}

// plot is a Chart/Bars' bars of days and its axis, the first day and the last.
function plot(days, value, says) {
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
  return [h("div", { class: "chart-bars-plot", onpointerover: point, onfocusin: point, onpointerleave: leave, onfocusout: leave }, all, tip),
    h("div", { class: "chart-bars-axis" }, h("span", {}, days.length ? dayName(days[0].date) : ""), h("span", {}, days.length ? named(days[days.length - 1].date) : ""))];
}
