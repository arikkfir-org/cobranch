import { h } from "./dom.js";

// codeSpans finds the inline code of Markdown text as a person types it: each run of backticks that a run of as many
// closes, backticks included, as [start, end) offsets into text. A run after a backslash opens nothing, as in Markdown,
// and a run nothing closes is plain text. Fenced code, three backticks to three, marks the same way.
export function codeSpans(text) {
  const spans = [];
  const runs = [...text.matchAll(/`+/g)];
  for (let i = 0; i < runs.length; i++) {
    const open = runs[i];
    if (open.index > 0 && text[open.index - 1] === "\\") continue;
    const close = runs.findIndex((r, j) => j > i && r[0].length === open[0].length);
    if (close < 0) continue;
    spans.push([open.index, runs[close].index + runs[close][0].length]);
    i = close;
  }
  return spans;
}

// The styles a textarea's backdrop copies from it, so that its text lies exactly under the textarea's own.
const copied = ["boxSizing", "paddingTop", "paddingRight", "paddingBottom", "paddingLeft", "borderTopWidth", "borderRightWidth",
  "borderBottomWidth", "borderLeftWidth", "borderStyle", "borderRadius", "fontFamily", "fontSize", "fontWeight", "fontStyle",
  "fontVariantLigatures", "letterSpacing", "lineHeight", "textIndent", "tabSize", "wordSpacing", "textTransform"];

// markCode puts inline code on Figma's Inline code background (bg/code) in a Markdown text box as it is typed, and
// answers the element to place where the textarea would go, with the textarea in it. A textarea draws its value in one
// style, so the marks are a backdrop beneath it: the same text, in the same box, transparent but for each span's
// background, under the textarea, whose own background moves to the backdrop. The backdrop follows what is typed and
// where the textarea scrolls, and leaves out the width of the textarea's scrollbar, so lines wrap where the textarea's
// do. Every Markdown text box uses it: the chat's composer, replies and edits, comments on lines, reviews and issues.
export function markCode(text) {
  const marks = h("div", { class: "code-marks", "aria-hidden": "true" });
  const box = h("div", { class: "code-marks-box" }, marks, text);
  let styled = false;
  const draw = () => {
    if (!text.isConnected) return;
    const style = getComputedStyle(text);
    if (!styled) {
      for (const k of copied) marks.style[k] = style[k];
      marks.style.background = style.backgroundColor;
      text.classList.add("code-marks-text");
      styled = true;
    }
    marks.style.width = text.clientWidth + parseFloat(style.borderLeftWidth) + parseFloat(style.borderRightWidth) + "px";
    marks.style.height = text.offsetHeight + "px";
    const value = text.value, nodes = [];
    let at = 0;
    for (const [start, end] of codeSpans(value)) {
      nodes.push(value.slice(at, start), h("mark", { class: "code-marks-span" }, value.slice(start, end)));
      at = end;
    }
    // A last line that is empty still takes its height in the textarea.
    nodes.push(value.slice(at) + "​");
    marks.replaceChildren(...nodes);
    marks.scrollTop = text.scrollTop;
    marks.scrollLeft = text.scrollLeft;
  };
  const later = () => requestAnimationFrame(draw);
  text.addEventListener("input", draw);
  text.addEventListener("focus", draw);
  text.addEventListener("scroll", () => { marks.scrollTop = text.scrollTop; marks.scrollLeft = text.scrollLeft; });
  // A value set by code, such as a composer clearing what it sent or writing an agent's name after @, fires no input:
  // draw after it too.
  const value = Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, "value");
  Object.defineProperty(text, "value", { configurable: true, get() { return value.get.call(this); }, set(v) { value.set.call(this, v); draw(); } });
  text.setRangeText = (...args) => { HTMLTextAreaElement.prototype.setRangeText.apply(text, args); draw(); };
  if (typeof ResizeObserver === "function") new ResizeObserver(later).observe(text);
  later();
  return box;
}
