import { api } from "./api.js";
import { messageBody } from "./chat.js";
import { h } from "./dom.js";
import { fromNow, plural, shortSha, whenExactly } from "./format.js";
import { icon, principalAvatar } from "./icons.js";
import { render } from "./router.js";
import { agents } from "./state.js";

// The review states as the design system draws them, the same way.
export const reviewStates = {
  approved: { word: "Approved", icon: "badge-check", tone: "success", count: "approval" },
  changes_requested: { word: "Changes requested", icon: "circle-alert", tone: "warning", count: "change request" },
  neutral: { word: "Neutral", icon: "message-square", tone: "neutral", count: "comment" },
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

// commitReviews is the card of a commit's reviews (Review), their bodies rendered as Markdown: each reviewer's latest,
// the one that counts, in the order they first reviewed it, with their earlier ones folded beneath; and, for a viewer
// holding reviews.submit, a form to submit one, with the comments drafting holds.
export function commitReviews(path, c, can, fail, drafting) {
  const latest = [];
  for (const r of c.reviews) {
    const i = r.reviewer ? latest.findIndex((l) => l.reviewer && l.reviewer.id === r.reviewer.id) : -1;
    if (i >= 0) latest[i] = r;
    else latest.push(r);
  }
  const earlier = c.reviews.filter((r) => !latest.includes(r)).reverse();
  const tally = Object.entries(reviewStates).map(([state, s]) => [latest.filter((r) => r.state === state).length, s.count]).filter(([n]) => n)
    .map(([n, word]) => plural(n, word)).join(" · ");
  const card = h("section", { class: "card", id: "reviews" }, h("div", { class: "card-header" }, h("h2", { class: "card-title" }, "Reviews"),
    tally ? h("span", { class: "card-meta" }, tally) : null));
  if (!c.reviews.length) card.append(h("div", { class: "card-body" }, h("p", { class: "muted" }, "No reviews of " + shortSha(c.sha) + " yet.")));
  else card.append(h("ul", { class: "list" }, latest.map((r) => reviewItem(c, r))));
  if (earlier.length) card.append(h("details", { class: "earlier-reviews" }, h("summary", {}, plural(earlier.length, "earlier review")),
    h("ul", { class: "list" }, earlier.map((r) => reviewItem(c, r, true)))));
  if (can("reviews.submit")) card.append(reviewForm(path, c, fail, drafting));
  return card;
}

// reviewItem is a review (Review): its reviewer, Agent for an agent and Not a person for a key, the commit and when,
// its state, and its body beneath.
function reviewItem(c, r, older) {
  const reviewer = r.reviewer;
  const agent = reviewer && agents.has(reviewer.id);
  const kind = agent ? h("span", { class: "tag tone-ai" }, icon("sparkles"), "Agent")
    : r.person ? null : h("span", { class: "tag", title: "Only people's approvals count towards required approvals" }, "Not a person");
  return h("li", { class: "review" + (older ? " earlier" : "") },
    h("div", { class: "review-header" }, reviewer ? principalAvatar(reviewer, 24) : null,
      h("strong", { class: "review-who" }, reviewer ? reviewer.displayName : "A principal since gone"), kind,
      h("span", { class: "review-when", title: whenExactly(r.submittedAt) }, "on " + shortSha(c.sha) + " · " + fromNow(r.submittedAt)), reviewBadge(r.state)),
    r.html ? h("div", { class: "review-body chat-message-body" }, messageBody(r.html)) : r.body ? h("pre", { class: "review-body" }, r.body) : null);
}

// reviewForm submits the viewer's review of commit c: Approve, Request changes or Comment, with what they write and
// the comments drafting holds, which open threads in its branch's chat. A review is never changed: submitting
// another replaces the viewer's verdict on c.
function reviewForm(path, c, fail, drafting) {
  const states = [["approved", "Approve"], ["changes_requested", "Request changes"], ["neutral", "Comment"]];
  const body = h("textarea", { class: "textarea", name: "body", maxlength: "65536", placeholder: "What you found, in Markdown" });
  const button = h("button", { class: "btn btn-primary", type: "submit" }, "Submit review");
  const drafted = h("span", { class: "field-help" });
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
  return h("form", { class: "card-body review-form", onsubmit: submit },
    h("label", { class: "field" }, h("span", { class: "field-label" }, "Your review of " + shortSha(c.sha)), body),
    h("div", { class: "review-form-actions" }, h("div", { class: "review-states", role: "radiogroup", "aria-label": "Verdict" },
      states.map(([value, label], i) => h("label", { class: "radio" }, h("input", { type: "radio", name: "state", value, checked: i === 2, required: true }), label))),
    drafted, button));
}
