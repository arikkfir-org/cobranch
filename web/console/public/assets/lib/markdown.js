import { h } from "./dom.js";
import { go } from "./router.js";
import { base } from "./state.js";
import { onThemeChange } from "./theme.js";

// frameStyle draws a rendered document with the design system's tokens, which the frame takes from the page (tokens), as
// the File · Markdown screen draws one.
const frameStyle = "html,body{margin:0;background:transparent}body{color:var(--color-fg-default);font:var(--text-body-medium);overflow-wrap:break-word}" +
  "body>:first-child{margin-top:0}body>:last-child{margin-bottom:0}a{color:var(--color-fg-link)}img{max-width:100%}" +
  "h1{font:var(--text-heading-medium);letter-spacing:var(--text-heading-medium-tracking)}" +
  "h2{font:var(--text-heading-small);letter-spacing:var(--text-heading-small-tracking)}" +
  "h3,h4,h5,h6{font:var(--text-body-medium-strong);letter-spacing:var(--text-body-medium-strong-tracking)}" +
  "h1,h2,h3,h4,h5,h6,p,ul,ol,table,pre,blockquote,details{margin:0 0 var(--space-12)}ul,ol{padding-left:var(--space-20)}li+li{margin-top:var(--space-4)}" +
  "code{font:var(--text-code-small);padding:var(--space-2) var(--space-4);border-radius:var(--radius-md);background:var(--color-bg-hover)}" +
  "pre{padding:var(--space-12);overflow:auto;border-radius:var(--radius-md);background:var(--color-bg-sunken)}" +
  "pre code{padding:0;font:var(--text-code-small);background:none}" +
  "table{display:block;max-width:100%;overflow:auto;border-collapse:collapse}th,td{padding:var(--space-6) var(--space-12);border:1px solid var(--color-border-default)}" +
  "blockquote{margin-left:0;padding:0 var(--space-16);color:var(--color-fg-muted);border-left:4px solid var(--color-border-default)}" +
  "hr{border:0;border-top:1px solid var(--color-border-default)}li>input[type=checkbox]{margin:0 .4em 0 -1.3em}kbd{font:var(--text-code-small);" +
  "padding:0 var(--space-4);border:1px solid var(--color-border-default);border-radius:var(--radius-sm)}";

// frameTokens are the tokens frameStyle draws with.
const frameTokens = [...new Set(frameStyle.match(/--[a-z0-9-]+/g))];

// fonts is the Console's typefaces' stylesheet, which a frame loads from where the Console's modules come from.
const fonts = new URL("../fonts.css", import.meta.url);

// frames are the rendered documents' frames on the page, which draw again in the theme's colours when it changes.
const frames = new Set();
onThemeChange(() => frames.forEach((frame) => {
  const style = frame.isConnected && frame.contentDocument && frame.contentDocument.getElementById("tokens");
  if (style) style.textContent = tokens();
  else if (!frame.isConnected) frames.delete(frame);
}));

// tokens are the frame's tokens, as the page has them now, and its colour scheme.
function tokens() {
  const style = getComputedStyle(document.documentElement);
  return ":root{" + frameTokens.map((t) => t + ":" + style.getPropertyValue(t)).join(";") + ";color-scheme:" + style.colorScheme + "}";
}

// rendered shows a document's HTML, which the API rendered and sanitized, in a frame that runs nothing and loads
// only images, the REST API's and other hosts', and the Console's typefaces: sandboxed without scripts and under its
// own Content Security Policy, as a second guard behind the sanitizer. The Console sizes the frame to its content and
// follows its links: its own pages within the Console, anchors by scrolling to them, anything else in a new tab.
export function rendered(html) {
  const policy = "default-src 'none'; img-src * data:; style-src 'unsafe-inline' " + fonts.origin + "; font-src " + fonts.origin;
  const frame = h("iframe", { class: "markdown", sandbox: "allow-same-origin", title: "Rendered document",
    srcdoc: '<!doctype html><meta charset="utf-8"><meta http-equiv="Content-Security-Policy" content="' + policy + '">' +
      '<link rel="stylesheet" href="' + fonts.href + '"><style id="tokens">' + tokens() + "</style><style>" + frameStyle + "</style><body>" + html });
  frames.add(frame);
  let width = 0;
  const fit = () => {
    const doc = frame.contentDocument;
    if (doc && doc.body) frame.style.height = doc.documentElement.scrollHeight + "px";
  };
  frame.addEventListener("load", () => {
    const doc = frame.contentDocument;
    if (!doc) return;
    fit();
    doc.fonts.ready.then(fit);
    doc.querySelectorAll("img").forEach((img) => img.addEventListener("load", fit));
    doc.addEventListener("click", (e) => follow(e, frame));
    if (location.hash) reveal(frame, location.hash.slice(1), "auto");
  });
  new ResizeObserver(([entry]) => {
    if (entry.contentRect.width !== width) { width = entry.contentRect.width; fit(); }
  }).observe(frame);
  return frame;
}

// follow follows a click on a link in a rendered document's frame: the Console's own pages within the Console, with
// the anchor they name, which their frame scrolls to once it loads; an anchor of the document by scrolling to it;
// anything else in a new tab.
function follow(e, frame) {
  const a = e.target.closest ? e.target.closest("a[href]") : null;
  if (!a) return;
  e.preventDefault();
  const href = a.getAttribute("href");
  if (href.startsWith("#")) return reveal(frame, href.slice(1), "smooth");
  const url = new URL(href, location.href);
  if (url.origin === location.origin && url.pathname.startsWith(base) && !e.metaKey && !e.ctrlKey) return go(url.pathname.slice(base.length - 1) + url.hash);
  open(url.href, "_blank", "noopener");
}

// reveal scrolls the page to the element of a rendered document's frame whose id is the anchor, if it has one.
function reveal(frame, anchor, behavior) {
  let id = anchor;
  try { id = decodeURIComponent(anchor); } catch { /* not percent-encoded: the id as written */ }
  const target = frame.contentDocument && frame.contentDocument.getElementById(id);
  if (target) scrollTo({ top: scrollY + frame.getBoundingClientRect().top + target.getBoundingClientRect().top - 16, behavior });
}
