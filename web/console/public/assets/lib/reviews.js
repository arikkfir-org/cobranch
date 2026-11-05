import { api } from "./api.js";
import { messageBody } from "./chat.js";
import { h } from "./dom.js";
import { plural, shortSha, whenExactly } from "./format.js";
import { icon } from "./icons.js";
import { render } from "./router.js";

// The review states as the design system draws them, the same way.
export const reviewStates = {
  approved: { word: "Approved", icon: "badge-check", tone: "success" },
  changes_requested: { word: "Changes requested", icon: "circle-alert", tone: "warning" },
  neutral: { word: "Neutral", icon: "message-square", tone: "neutral" },
};
// reviewBadge is a review's state as a badge, the same way.
function reviewBadge(state) {
  const s = reviewStates[state];
  return h("span", { class: "badge tone-" + s.tone }, icon(s.icon), s.word);
}

// reviewDrafting is the drafting of comments on branch b's changes, for a viewer who may review its head and post to
// its chat, or null: the Changes tab drafts them, and the review forms there, on the branch's page and on its head's
// page send them.
export function reviewDrafting(path, b, can) {
  return b.base && b.commit && can("reviews.submit") && can("messages.post") ? draftsOf(path, b.name, b.head) : null;
}

// commitDrafting is the drafting of comments on the changes of a branch whose head is commit c, for a viewer who may
// review it and post to its chat, or null: the Changes tab links its head's page, whose review form sends them too.
// Of several branches at c with drafts, the first found holds.
export function commitDrafting(path, c, can) {
  if (!can("reviews.submit") || !can("messages.post")) return null;
  const prefix = "cobranch.review-drafts\n" + path + "\n", suffix = "\n" + c.sha;
  const keys = [...reviewDrafts.keys()];
  try {
    for (let i = 0; i < sessionStorage.length; i++) keys.push(sessionStorage.key(i));
  } catch {
    // Without the tab's storage, only the drafts of this page's life count.
  }
  for (const key of keys) {
    if (!key || !key.startsWith(prefix) || !key.endsWith(suffix)) continue;
    const drafting = draftsOf(path, key.slice(prefix.length, -suffix.length), c.sha);
    if (drafting.comments.length) return drafting;
  }
  return null;
}

// reviewDrafts holds the comments a reviewer drafted on a branch's changes, by repository, branch and head, until they
// submit their review, so that a review arrives whole: a head that moved on starts afresh. The tab's session keeps
// them too, so that loading the page again keeps them.
const reviewDrafts = new Map();

// draftsOf is the drafting of comments on the changes of the repository's branch at head: the comments, and changed,
// which keeps them as they are now and calls shown, which the review form sets to show how many it sends.
function draftsOf(path, branch, head) {
  const key = "cobranch.review-drafts\n" + [path, branch, head].join("\n");
  if (!reviewDrafts.has(key)) {
    let comments = [];
    try {
      const stored = JSON.parse(sessionStorage.getItem(key) || "[]");
      if (Array.isArray(stored)) comments = stored;
    } catch {
      comments = []; // a tab without storage, or drafts it can't read, starts afresh
    }
    reviewDrafts.set(key, { branch, comments, shown: () => {}, changed() {
      try {
        if (this.comments.length) sessionStorage.setItem(key, JSON.stringify(this.comments));
        else sessionStorage.removeItem(key);
      } catch {
        // Without the tab's storage, the drafts last until the page is loaded again.
      }
      this.shown();
    } });
  }
  return reviewDrafts.get(key);
}

// commitReviews lists a commit's reviews, their bodies rendered as Markdown: each reviewer's latest, the one that
// counts, in the order they first reviewed it, with their earlier ones folded beneath; and, for a viewer holding
// reviews.submit, a form to submit one, with the comments drafting holds.
export function commitReviews(path, c, can, fail, drafting) {
  const latest = [];
  for (const r of c.reviews) {
    const i = r.reviewer ? latest.findIndex((l) => l.reviewer && l.reviewer.id === r.reviewer.id) : -1;
    if (i >= 0) latest[i] = r;
    else latest.push(r);
  }
  const earlier = c.reviews.filter((r) => !latest.includes(r)).reverse();
  const item = (r, cls) => h("li", { class: cls || null },
    h("div", { class: "who" }, reviewBadge(r.state), h("strong", {}, r.reviewer ? r.reviewer.displayName : "A principal since gone"),
      r.person ? null : h("span", { class: "chip", title: "Only people's approvals count towards required approvals" }, "not a person"),
      h("span", { class: "muted" }, whenExactly(r.submittedAt))),
    r.html ? h("div", { class: "message" }, h("div", { class: "body" }, messageBody(r.html)))
      : r.body ? h("pre", { class: "message" }, r.body) : null);
  const panel = h("section", { class: "panel" }, h("h3", {}, "Reviews"));
  if (!c.reviews.length) panel.append(h("p", {}, "No reviews yet."));
  else panel.append(h("ul", { class: "reviews" }, latest.map((r) => item(r))));
  if (earlier.length) panel.append(h("details", {}, h("summary", { class: "muted" }, plural(earlier.length, "earlier review")),
    h("ul", { class: "reviews" }, earlier.map((r) => item(r, "earlier")))));
  if (can("reviews.submit")) panel.append(reviewForm(path, c, fail, drafting));
  return panel;
}

// reviewForm submits the viewer's review of commit c: Approve, Request changes or Comment, with what they write and
// the comments drafting holds, which open threads in its branch's chat. A review is never changed: submitting
// another replaces the viewer's verdict on c.
function reviewForm(path, c, fail, drafting) {
  const states = [["approved", "Approve"], ["changes_requested", "Request changes"], ["neutral", "Comment"]];
  const body = h("textarea", { name: "body", maxlength: "65536", placeholder: "What you found, in Markdown" });
  const button = h("button", { class: "button primary", type: "submit" }, "Submit review");
  const drafted = h("span", { class: "muted" });
  if (drafting) {
    drafting.shown = () => { drafted.textContent = drafting.comments.length ? "With " + plural(drafting.comments.length, "comment") + " on the changes." : ""; };
    drafting.shown();
  }
  const submit = (e) => {
    e.preventDefault();
    const review = { state: new FormData(e.target).get("state"), body: body.value };
    if (drafting && drafting.comments.length) Object.assign(review, { branch: drafting.branch, comments: drafting.comments });
    button.disabled = true;
    api("POST", path + "/commits/" + c.sha + "/reviews", review)
      .then(() => {
        if (drafting) {
          drafting.comments.length = 0;
          drafting.changed();
        }
        render();
      })
      .catch((err) => { button.disabled = false; fail(err); });
  };
  return h("form", { class: "review", onsubmit: submit },
    h("label", {}, "Your review of " + shortSha(c.sha), body),
    h("fieldset", { class: "states" }, h("legend", { class: "muted" }, "Verdict"),
      states.map(([value, label], i) => h("label", {}, h("input", { type: "radio", name: "state", value, checked: i === 2, required: true }), label))),
    h("div", { class: "row" }, button, drafted));
}
