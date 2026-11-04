import { annotationStatuses } from "../lib/annotations.js";
import { branchStatuses } from "../lib/branches.js";
import { app, h } from "../lib/dom.js";
import { icon } from "../lib/icons.js";
import { viewerLinks } from "../lib/layout.js";
import { reviewStates } from "../lib/reviews.js";
import { base } from "../lib/state.js";

// Help: the Console's reference pages, behind the sign-in. /help/components draws every component of the design
// system's Figma file, in the file's order and under its names, each with the class components.css gives it, so a
// reviewer holds the page beside the Figma file's Components page, in either theme, and a designer finds which code a
// component is. It draws from the classes alone: what differs from Figma is a bug in components.css or in the file.

export const view = (rest) => rest[0] === "components" ? "help.components" : "help";
export const crumbs = (rest) => rest[0] === "components" ? [{ label: "Help", href: "help" }, { label: "Components" }] : [{ label: "Help" }];

// showHelp draws the help page at rest, the path's parts after help/, under the Console's bar.
export function showHelp(rest) {
  const bar = h("header", { class: "bar" }, h("a", { class: "brand", href: base, "data-nav": "" }, "Cobranch"),
    h("nav", {}, h("a", { href: base + "help", "data-nav": "", class: "current" }, "Help")), h("span", { class: "spacer" }), ...viewerLinks());
  const main = h("main", { class: "page" });
  document.body.className = "";
  app.replaceChildren(bar, main);
  show({ main, rest });
}

export default function show({ main, rest }) {
  if (rest[0] === "components") return main.append(...components());
  main.append(h("h1", {}, "Help"), h("ul", {}, h("li", {}, h("a", { href: base + "help/components", "data-nav": "" }, "Components"),
    ": every component of the design system, as the Console draws it.")));
}

// section is one of the Components page's sections: its title, what it says, the Figma components and classes it
// shows, and their specimens.
const section = (title, says, names, ...specimens) => h("section", { class: "specimens" }, h("h2", {}, title), h("p", {}, says),
  h("p", { class: "specimens-names" }, names.map(([figma, css], i) => [i ? " · " : null, figma, " ", h("code", {}, css)])), ...specimens);
// grid lays specimens out under column headings, a row of them per line, as the Figma page does.
const grid = (heads, rows) => h("div", { class: "specimens-grid", style: "--columns: " + heads.length }, heads.map((t) => h("span", { class: "specimens-head" }, t)),
  rows.flat().map((cell) => h("div", {}, cell)));
const badge = (s, cls = "badge") => h("span", { class: cls + " tone-" + s.tone }, cls === "badge" ? icon(s.icon) : null, s.word);
const tones = ["neutral", "info", "success", "warning", "danger", "ai"];
const toneName = (t) => t === "ai" ? "AI" : t[0].toUpperCase() + t.slice(1);

function components() {
  return [h("h1", {}, "Components"),
    h("p", {}, "Every component of the design system's Figma file, drawn by the Console's own classes in both themes. Hover a control to see its hover state and Tab to it to see its focus ring; the user menu switches the theme."),
    buttons(), iconButtons(), statusBadges(), tags(), avatars(), inputs(), tabs(), menus(), segmented(), tables(), patterns()];
}

function buttons() {
  const styles = [["Primary", "btn btn-primary"], ["Secondary", "btn"], ["Ghost", "btn btn-ghost"], ["Danger", "btn btn-danger"]];
  return section("Buttons", "One primary action per view. Secondary for the other actions, Ghost for toolbars and dense rows, Danger only for destructive, irreversible actions. Medium (32px) is the default; Small (28px) sits in tables and headers.",
    [["Button", ".btn"], ["Style", ".btn-primary .btn-ghost .btn-danger"], ["Size=Small", ".btn-sm"]],
    grid(["Style", "Medium", "Medium, disabled", "Small", "Small, disabled", "With icons"], styles.map(([name, cls]) => [name,
      h("button", { class: cls, type: "button" }, "Button"), h("button", { class: cls, type: "button", disabled: true }, "Button"),
      h("button", { class: cls + " btn-sm", type: "button" }, "Button"), h("button", { class: cls + " btn-sm", type: "button", disabled: true }, "Button"),
      h("button", { class: cls, type: "button" }, icon("plus"), "Button", icon("chevron-down"))])));
}

function iconButtons() {
  const button = (cls) => h("button", { class: cls, type: "button", "aria-label": "More actions", title: "More actions" }, icon("ellipsis"));
  return section("Icon buttons", "Square buttons for a single icon: overflow menus, copy, close. Always give them a tooltip and an accessible name; prefer a labelled Button when there is room.",
    [["Icon button", ".icon-btn"], ["Style=Ghost", ".icon-btn-ghost"], ["Size=Small", ".icon-btn-sm"]],
    grid(["Style", "Medium", "Small", "Disabled"], [["Secondary", "icon-btn"], ["Ghost", "icon-btn icon-btn-ghost"]].map(([name, cls]) => [name, button(cls), button(cls + " icon-btn-sm"),
      Object.assign(button(cls), { disabled: true })])));
}

function statusBadges() {
  const set = (name, table) => [name, h("div", { class: "specimens-row" }, Object.values(table).map((s) => badge(s)))];
  return section("Status badges", "Status is always icon + word + colour, never colour alone. Merged is solid ink: the branch is done and no longer asks for attention. Badge/Quiet is the same word with a dot instead of a pill, for lists and rows where a pill would shout.",
    [["Badge/Branch status, Badge/Annotation, Badge/Review", ".badge .tone-*"], ["Badge/Quiet", ".badge-quiet .tone-*"]],
    grid(["Set", "Statuses"], [set("Badge/Branch status", branchStatuses), set("Badge/Annotation", annotationStatuses), set("Badge/Review", reviewStates),
      ["Badge/Quiet", h("div", { class: "specimens-row" }, tones.map((t) => badge({ tone: t, word: toneName(t) }, "badge-quiet")))]]));
}

function tags() {
  return section("Tags", "Tags label things (issue labels, environments, principal kinds). Square corners tell them apart from status pills. The AI tone marks anything an agent or AI review produced.",
    [["Tag", ".tag .tone-*"]],
    h("div", { class: "specimens-row" }, tones.map((t) => h("span", { class: "tag tone-" + t }, t === "ai" ? icon("sparkles") : null, toneName(t)))));
}

function avatars() {
  const kinds = [["User", "avatar", "NL"], ["Unclaimed", "avatar avatar-unclaimed", "DE"], ["Agent", "avatar avatar-agent", icon("sparkles")], ["Machine", "avatar avatar-machine", icon("cog")]];
  const sizes = [["20", ""], ["24", " avatar-24"], ["32", " avatar-32"], ["40", " avatar-40"]];
  return section("Avatars", "People are circles, machines are rounded squares. Agents are violet with the sparkles mark; jobs, API keys, OAuth apps and workload identities are ink squares with a glyph (cog, key-round, server). A commit author with no account yet is a dashed circle until someone claims it.",
    [["Avatar", ".avatar"], ["Kind", ".avatar-unclaimed .avatar-agent .avatar-machine"], ["Size", ".avatar-24 .avatar-32 .avatar-40"]],
    grid(["Kind", ...sizes.map(([s]) => s)], kinds.map(([name, cls, content]) => [name, ...sizes.map(([, size]) => h("span", { class: cls + size, role: "img", "aria-label": name },
      content instanceof Node ? content.cloneNode(true) : content))])),
    h("div", { class: "specimens-row" }, [["cog", "A job"], ["key-round", "An API key"], ["server", "An app or a workload"]].map(([glyph, label]) =>
      h("span", { class: "avatar avatar-32 avatar-machine", role: "img", "aria-label": label, title: label }, icon(glyph)))));
}

function inputs() {
  const field = (label, input, help) => h("label", { class: "field" }, h("span", { class: "field-label" }, label), input, h("span", { class: "field-help" }, help));
  return section("Inputs", "A text field is a label above a 32px field, with optional helper or error text below. Placeholder text is a hint, never the label. Errors say what to do, in status/danger colour, next to the field.",
    [["Input", ".field .field-label .input .field-help"]],
    h("div", { class: "specimens-fields" },
      field("Label", h("input", { class: "input", placeholder: "Placeholder" }), "Helper text"),
      field("Focus it", h("input", { class: "input", value: "Value" }), "Click the field to see its focus"),
      field("Label", h("input", { class: "input", placeholder: "Placeholder", "aria-invalid": "true" }), "Say what to do about the error"),
      field("Label", h("input", { class: "input", placeholder: "Placeholder", disabled: true }), "Helper text"),
      field("Search", h("span", { class: "input" }, icon("search"), h("input", { type: "search", placeholder: "Filter issues" })), "Show icon, for search")));
}

function tabs() {
  const tab = (label, selected, count) => h("button", { class: "tab", type: "button", role: "tab", "aria-selected": selected ? "true" : "false" }, label, count ? h("span", { class: "tab-count" }, count) : null);
  return section("Tabs", "Tabs switch between views of the same object (a branch's Overview, Commits, Files, Chat). The selected tab is ink with a 2px underline; the others are muted. A count says how many items wait behind a tab.",
    [["Tab", ".tab-bar .tab .tab-count"]],
    h("div", { class: "tab-bar", role: "tablist" }, tab("Overview", true), tab("Commits", false, "12"), tab("Files"), tab("Chat", false, "3")));
}

function menus() {
  const item = (glyph, label, extra = {}, ...after) => h("button", { class: "menu-item", type: "button", role: "menuitem", ...extra }, icon(glyph), label, ...after);
  return section("Menus", "Menus hold secondary actions behind a button or a right click. Items are 32px, icon then label then an optional shortcut. Destructive items come last, after a divider, in danger colour.",
    [["Menu", ".menu"], ["Menu item", ".menu-item .menu-shortcut"], ["Tone=Danger", ".menu-item-danger"], ["Divider", ".menu-divider"]],
    h("div", { class: "specimens-row" },
      h("div", { class: "menu specimens-menu", role: "menu" }, item("pencil", "Rename branch"), item("link", "Copy link", {}, h("span", { class: "menu-shortcut" }, "⌘L")), item("copy", "Copy SHA"),
        h("hr", { class: "menu-divider" }), Object.assign(item("trash-2", "Delete branch"), { className: "menu-item menu-item-danger" })),
      h("div", { class: "menu specimens-menu", role: "menu" }, item("user", "Owner", { role: "menuitemradio", "aria-checked": "true" }), item("user", "Member", { role: "menuitemradio", "aria-checked": "false" }),
        item("user", "Guest", { role: "menuitemradio", "aria-checked": "false", disabled: true }))));
}

function segmented() {
  const choices = ["Light", "Dark", "System"].map((label) => h("button", { type: "button", "aria-pressed": label === "System" ? "true" : "false", onclick: () => {
    for (const c of choices) c.setAttribute("aria-pressed", c === choices.find((b) => b.textContent === label) ? "true" : "false");
  } }, label));
  return section("Segmented control", "One choice of a few, side by side, as the user menu picks the theme. The chosen segment is pressed.",
    [["Segmented (in App/User menu)", ".segmented"]],
    h("div", { class: "segmented specimens-segmented", role: "group", "aria-label": "Theme" }, choices));
}

function tables() {
  const rows = [["feature/push-worker", branchStatuses.processing, h("span", { class: "avatar" }, "NL"), "Noa Levi", "2 minutes ago"],
    ["agent/eng-212-annotations", branchStatuses.ready, h("span", { class: "avatar avatar-agent" }, icon("sparkles")), "Reviewer", "1 hour ago"],
    ["fix/sign-in-redirect", branchStatuses.blocked, h("span", { class: "avatar avatar-unclaimed" }, "DE"), "dana@example.com", "Yesterday"]];
  return section("Tables", "Tables list objects of one kind: branches, commits, members. Header cells sit on bg/sunken; body rows are divided by border/subtle and hovered. SHAs, paths and branch names are mono. A cell may hold a badge, an avatar or a button instead of text.",
    [["Table/Header cell, Table/Cell", ".table"], ["Kind=Muted", ".cell-muted"], ["Kind=Mono", ".cell-mono"]],
    h("table", { class: "table" }, h("thead", {}, h("tr", {}, ["Branch", "Status", "Author", "Updated"].map((t) => h("th", {}, t)))),
      h("tbody", {}, rows.map(([branch, status, avatar, who, when]) => h("tr", {}, h("td", { class: "cell-mono" }, branch), h("td", {}, badge(status)),
        h("td", {}, h("span", { class: "cell-person" }, avatar, who)), h("td", { class: "cell-muted" }, when))))));
}

function patterns() {
  const text = (title, meta, cls = "row-title") => h("div", { class: "row-text" }, h("span", { class: cls }, title), h("span", { class: "row-meta" }, meta));
  const ghost = (glyph, label) => h("button", { class: "icon-btn icon-btn-ghost icon-btn-sm", type: "button", "aria-label": label, title: label }, icon(glyph));
  const annotation = (s) => h("li", { class: "annotation-row" }, h("span", { class: "tone-" + s.tone }, icon(s.icon, s.word)), h("span", { class: "row-title" }, "ci/test"),
    h("span", { class: "row-description" }, "412 tests passed"), h("span", { class: "avatar avatar-machine", role: "img", "aria-label": "A job" }, icon("cog")),
    h("span", { class: "row-aside" }, "1m 12s"), h("a", { href: "#" }, "Details"));
  return section("Patterns", "Rows that recur across screens, composed from the components above. Lists are rows on bg/surface inside a border/default card with radius/lg; each row is divided by border/subtle.",
    [["Commit row", ".commit-row"], ["Annotation row", ".annotation-row"], ["Branch row", ".branch-row"], ["Repository row", ".repository-row"], ["Issue row", ".issue-row"], ["Member row", ".member-row"], ["the list", ".list .row-text .row-title .row-meta"]],
    h("ul", { class: "list" },
      h("li", { class: "commit-row" }, h("span", { class: "avatar avatar-24" }, "NL"), text("Hand pushes to the push worker before returning", "Noa Levi committed 12 minutes ago"),
        h("span", { class: "row-icons" }, [annotationStatuses.successful, annotationStatuses.successful, annotationStatuses.in_progress].map((s) => h("span", { class: "tone-" + s.tone }, icon(s.icon, s.word)))),
        h("code", { class: "row-mono" }, "9f3c2e1"), ghost("copy", "Copy the SHA"))),
    h("ul", { class: "list" }, Object.values(annotationStatuses).map(annotation)),
    h("ul", { class: "list" },
      h("li", { class: "branch-row" }, icon("git-branch"), text("feature/push-worker", "into main · 4 commits · updated 2 minutes ago by Noa Levi"), badge(branchStatuses.processing),
        h("span", { class: "avatar" }, "NL"), h("span", { class: "row-aside" }, icon("message-square", "Messages"), " 8"))),
    h("ul", { class: "list" },
      h("li", { class: "repository-row" }, icon("folder-git-2"), h("div", { class: "row-text" }, h("span", { class: "row-title" }, "website"),
        h("span", { class: "row-description" }, "The project's website and its docs."), h("span", { class: "row-meta" }, "12 branches · 2 ready to merge · updated 5 minutes ago")),
        h("span", { class: "tag" }, "Private"))),
    h("ul", { class: "list" },
      h("li", { class: "issue-row" }, h("span", { class: "tone-neutral" }, icon("circle", "Open")), h("code", { class: "row-mono" }, "DEV-42"), h("span", { class: "row-title" }, "Issue title"),
        h("span", { class: "tag tone-info" }, "Git"), h("span", { class: "row-branch" }, icon("git-branch"), "feature/push-worker"), h("span", { class: "avatar" }, "NL"))),
    h("ul", { class: "list" },
      h("li", { class: "member-row" }, h("span", { class: "avatar avatar-32" }, "NL"), text("Noa Levi", "noa@example.com"), h("span", { class: "row-aside" }, "Joined Oct 2026"),
        h("button", { class: "btn btn-ghost btn-sm", type: "button" }, "Owner", icon("chevron-down")), ghost("ellipsis", "More actions"))));
}
