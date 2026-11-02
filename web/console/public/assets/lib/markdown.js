import { h } from "./dom.js";
import { go } from "./router.js";
import { base } from "./state.js";

// frameTokens are the Console's colours and fonts that a rendered document's frame takes from the page.
const frameTokens = ["--color-bg-raised", "--color-fg-default", "--color-fg-muted", "--color-border-default", "--color-accent",
  "--font-sans", "--font-mono"];
const frameStyle = "html,body{margin:0;background:transparent}body{color:var(--color-fg-default);font:14px/1.6 var(--font-sans);overflow-wrap:break-word}" +
  "body>:first-child{margin-top:0}body>:last-child{margin-bottom:0}a{color:var(--color-accent)}img{max-width:100%}" +
  "h1,h2{padding-bottom:.3em;border-bottom:1px solid var(--color-border-default)}h1{font-size:2em}h2{font-size:1.5em}h3{font-size:1.25em}" +
  "h1,h2,h3,h4,h5,h6{margin:24px 0 16px;line-height:1.25;font-weight:600}p,ul,ol,table,pre,blockquote,details{margin:0 0 16px}" +
  "code{font-family:var(--font-mono);font-size:85%;padding:.2em .4em;border-radius:6px;background:var(--color-border-default)}" +
  "pre{padding:16px;overflow:auto;border:1px solid var(--color-border-default);border-radius:8px;background:var(--color-bg-raised)}" +
  "pre code{padding:0;font-size:13px;background:none}" +
  "table{display:block;max-width:100%;overflow:auto;border-collapse:collapse}th,td{padding:6px 13px;border:1px solid var(--color-border-default)}" +
  "blockquote{margin-left:0;padding:0 1em;color:var(--color-fg-muted);border-left:4px solid var(--color-border-default)}" +
  "hr{border:0;border-top:1px solid var(--color-border-default)}li>input[type=checkbox]{margin:0 .4em 0 -1.3em}kbd{font-family:var(--font-mono);" +
  "padding:0 4px;border:1px solid var(--color-border-default);border-radius:4px}";

// rendered shows a document's HTML, which the API rendered and sanitized, in a frame that runs nothing and loads
// only images, the REST API's and other hosts': sandboxed without scripts and under its own Content Security
// Policy, as a second guard behind the sanitizer. The Console sizes the frame to its content and follows its links: its own pages
// within the Console, anchors by scrolling to them, anything else in a new tab.
export function rendered(html) {
  const style = getComputedStyle(document.documentElement);
  const tokens = frameTokens.map((t) => t + ":" + style.getPropertyValue(t)).join(";") + ";color-scheme:" + style.colorScheme;
  const policy = "default-src 'none'; img-src * data:; style-src 'unsafe-inline'";
  const frame = h("iframe", { class: "markdown", sandbox: "allow-same-origin", title: "Rendered document",
    srcdoc: '<!doctype html><meta charset="utf-8"><meta http-equiv="Content-Security-Policy" content="' + policy + '">' +
      "<style>:root{" + tokens + "}" + frameStyle + "</style><body>" + html });
  let width = 0;
  const fit = () => {
    const doc = frame.contentDocument;
    if (doc && doc.body) frame.style.height = doc.documentElement.scrollHeight + "px";
  };
  frame.addEventListener("load", () => {
    const doc = frame.contentDocument;
    if (!doc) return;
    fit();
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
