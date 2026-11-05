import { annotationStatuses } from "../lib/annotations.js";
import { logo, mark, markSmall } from "../lib/brand.js";
import { branchStatuses } from "../lib/branches.js";
import { h } from "../lib/dom.js";
import { icon } from "../lib/icons.js";
import { reviewStates } from "../lib/reviews.js";
import { base } from "../lib/state.js";
import { onThemeChange, setTheme, theme, themes } from "../lib/theme.js";

// Help: the Console's reference pages, behind the sign-in. /help/components draws every component of components.css
// and the brand's marks, in the Figma file's order and under its names, each with the class that draws it, so a
// reviewer holds the page beside the Figma file's Components page, in either theme, and a designer finds which code a
// component is. It draws from the classes alone: what differs from Figma is a bug in components.css or in the file.
// The app shell's own components are the shell's to draw, not specimens here.

export const view = (rest) => ({ components: "help.components" })[rest[0]] || "help";
export const crumbs = ({ rest }) => rest[0] === "components" ? [{ label: "Help", href: base + "help" }, { label: "Components" }] : [{ label: "Help" }];

export default function show({ main, rest }) {
  if (rest[0] === "components") return main.append(...components());
  main.append(h("h1", {}, "Help"), h("ul", {}, h("li", {}, h("a", { href: base + "help/components", "data-nav": "" }, "Components"),
    ": the design system's components and marks, as the Console draws them.")));
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
    h("p", {}, "Every component of the Console's component stylesheet, and the brand's marks, as the design system's Figma file draws them. Hover a control to see its hover state and Tab to it to see its focus ring; the segmented control under Forms switches the theme, to compare both. The app shell's own components (App/*, Nav item, Nav/Sub item, Chat message and Brand/Credit) are drawn by the shell, not here."),
    brand(), buttons(), iconButtons(), statusBadges(), tags(), avatars(), inputs(), forms(), tabs(), menus(), tables(), surfaces(), patterns()];
}

function brand() {
  return section("Brand", "The mark is an ink square with a person and an agent on one trunk, for 24px and up; the logo sets the wordmark beside it. The small mark, the branch graph, is for 16px and the favicon. Both follow the theme. Brand/Credit is the app shell's.",
    [["Brand/Logo", "logo() mark()"], ["Brand/Mark small", "markSmall()"]],
    grid(["Brand/Logo", "The mark alone", "Brand/Mark small"], [[logo(), mark("Cobranch"), markSmall("Cobranch")]]));
}

function buttons() {
  const styles = [["Primary", "btn btn-primary"], ["Secondary", "btn"], ["Ghost", "btn btn-ghost"], ["Danger", "btn btn-danger"]];
  return section("Buttons", "One primary action per view. Secondary for the other actions, Ghost for toolbars and dense rows, Danger only for destructive, irreversible actions. Medium (32px) is the default; Small (28px) sits in tables and headers.",
    [["Button", ".btn"], ["Style", ".btn-primary .btn-secondary .btn-ghost .btn-danger"], ["Size=Small", ".btn-sm"]],
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

function forms() {
  const field = (label, control, help) => h("label", { class: "field" }, h("span", { class: "field-label" }, label), control, help ? h("span", { class: "field-help" }, help) : null);
  const select = (attrs = {}) => h("span", { class: "select" }, h("select", attrs, ["Anthropic", "OpenAI", "A local model"].map((m) => h("option", {}, m))));
  const textarea = (attrs = {}) => h("textarea", { class: "textarea", ...attrs }, "Work the issue or the branch you are given. Open a branch named after the issue, keep commits small, and ask in the chat before changing an API.");
  const check = (cls, type, label, attrs) => h("label", { class: cls }, h("input", { type, name: cls, ...attrs }), label);
  const choices = themes.map((t) => h("button", { type: "button", onclick: () => setTheme(t) }, t[0].toUpperCase() + t.slice(1)));
  const pressed = () => choices.forEach((c, i) => c.setAttribute("aria-pressed", themes[i] === theme() ? "true" : "false"));
  pressed();
  onThemeChange(() => { if (choices[0].isConnected) pressed(); });
  return section("Forms", "Select, Checkbox, Radio, Textarea and Segmented control follow Input: label above the control, 32px controls, radius/md, border/strong at rest, border/focus with the Focus/Ring effect when focused, status/danger when in error. The segmented control is the theme switch and any short exclusive choice; this one switches the Console's theme.",
    [["Select", ".select"], ["Textarea", ".textarea"], ["Checkbox", ".checkbox"], ["Radio", ".radio"], ["Segmented control", ".segmented"]],
    h("div", { class: "specimens-fields" },
      field("Label", select()), field("Focus it", select()), field("Label", select({ "aria-invalid": "true" }), "Say what to do about the error"), field("Label", select({ disabled: true }))),
    h("div", { class: "specimens-fields" },
      field("Label", textarea()), field("Focus it", textarea()), field("Label", textarea({ "aria-invalid": "true" }), "Say what to do about the error"), field("Label", textarea({ disabled: true }))),
    h("div", { class: "specimens-row" },
      h("div", { class: "specimens-stack" }, check("checkbox", "checkbox", "Label", {}), check("checkbox", "checkbox", "Label", { disabled: true }),
        check("checkbox", "checkbox", "Label", { checked: true }), check("checkbox", "checkbox", "Label", { checked: true, disabled: true })),
      h("div", { class: "specimens-stack" }, check("radio", "radio", "Option", {}), check("radio", "radio", "Option", { disabled: true }),
        check("radio", "radio", "Option", { checked: true }), h("label", { class: "radio" }, h("input", { type: "radio", name: "radio-disabled", checked: true, disabled: true }), "Option")),
      h("div", { class: "segmented specimens-segmented", role: "group", "aria-label": "Theme" }, choices)));
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

function surfaces() {
  const close = () => h("button", { class: "icon-btn icon-btn-ghost icon-btn-sm", type: "button", "aria-label": "Close", title: "Close" }, icon("x"));
  const dialog = (title, body, action, attrs = {}) => h("dialog", { class: "dialog", ...attrs },
    h("div", { class: "dialog-header" }, h("h2", { class: "dialog-title" }, title), close()), h("div", { class: "dialog-body" }, h("p", {}, body)),
    h("div", { class: "dialog-footer" }, h("button", { class: "btn", type: "button" }, "Cancel"), action));
  const modal = dialog("Delete repository?", "website, its 12 branches and its 48 issues are deleted for everyone in the organization. This cannot be undone.",
    h("button", { class: "btn btn-danger", type: "button" }, "Delete"));
  for (const b of modal.querySelectorAll("button")) b.addEventListener("click", () => modal.close());
  modal.addEventListener("click", (e) => { if (e.target === modal) modal.close(); });
  const banners = [["info", "info"], ["success", "circle-check"], ["warning", "triangle-alert"], ["danger", "circle-x"]].map(([tone, glyph]) =>
    h("div", { class: "banner banner-" + tone, role: tone === "danger" ? "alert" : "status" }, icon(glyph),
      h("div", { class: "banner-text" }, h("span", { class: "banner-title" }, "Banner title"), h("p", {}, "What happened and what to do about it, in a sentence.")),
      tone === "warning" ? h("button", { class: "btn btn-ghost btn-sm", type: "button" }, "Action") : null, close()));
  return section("Surfaces", "Containers and overlays: the card every list and table sits in, the dialog, banners for inline messages, the empty state, the tooltip and the page header. Spacing, radius and colour are tokens; a page composes these and writes no surface CSS of its own.",
    [["Card", ".card .card-header .card-title .card-meta .card-body"], ["Dialog", ".dialog .dialog-header .dialog-title .dialog-body .dialog-footer"], ["Banner", ".banner .banner-info .banner-success .banner-warning .banner-danger"],
      ["Empty state", ".empty .empty-icon .empty-title .empty-description"], ["Tooltip", ".tooltip [data-tooltip]"], ["Page header", ".page-header .page-header-title .page-header-description .page-header-actions"]],
    h("div", { class: "specimens-row" },
      h("div", { class: "card specimens-card" }, h("div", { class: "card-header" }, h("h2", { class: "card-title" }, "Card title"), h("span", { class: "card-meta" }, "12 items"),
        h("button", { class: "btn btn-sm", type: "button" }, "Action")),
        h("div", { class: "card-body" }, "The card holds a table, a list of rows or a form. When the card holds only a table, the table is the card: no header, no padding.")),
      dialog("Dialog title", "What the dialog asks or shows. Keep it to a sentence or two; a form goes here as fields with labels above them.",
        h("button", { class: "btn btn-primary", type: "button" }, "Save"), { open: true })),
    h("div", { class: "specimens-row" }, h("button", { class: "btn", type: "button", onclick: () => modal.showModal() }, "Open a dialog over the scrim"), modal),
    h("div", { class: "specimens-stack specimens-banners" }, banners),
    h("div", { class: "empty" }, h("div", { class: "empty-icon" }, icon("folder-git-2")), h("h2", { class: "empty-title" }, "Nothing here yet"),
      h("p", { class: "empty-description" }, "Say what would fill this, and how. One or two sentences, centred."), h("button", { class: "btn btn-primary", type: "button" }, icon("plus"), "Add one")),
    h("div", { class: "specimens-row" }, h("span", { class: "tooltip" }, "Tooltip"),
      h("button", { class: "icon-btn", type: "button", "aria-label": "Copy the SHA", "data-tooltip": "Copy the SHA" }, icon("copy")), h("span", { class: "specimens-head" }, "Hover or focus the button")),
    h("header", { class: "page-header" }, h("div", { class: "page-header-text" }, h("h1", { class: "page-header-title" }, "Page title"),
      h("p", { class: "page-header-description" }, "One line on what the page holds or where it stands.")),
      h("div", { class: "page-header-actions" }, h("button", { class: "btn", type: "button" }, "Secondary"), h("button", { class: "btn btn-primary", type: "button" }, icon("plus"), "Primary"))));
}

function patterns() {
  const text = (title, meta, cls = "row-title") => h("div", { class: "row-text" }, h("span", { class: cls }, title), h("span", { class: "row-meta" }, meta));
  const ghost = (glyph, label) => h("button", { class: "icon-btn icon-btn-ghost icon-btn-sm", type: "button", "aria-label": label, title: label }, icon(glyph));
  const annotation = (s) => h("li", { class: "annotation-row" }, h("span", { class: "tone-" + s.tone }, icon(s.icon, s.word)), h("span", { class: "row-title" }, "ci/test"),
    h("span", { class: "row-description" }, "412 tests passed"), h("span", { class: "avatar avatar-machine", role: "img", "aria-label": "A job" }, icon("cog")),
    h("span", { class: "row-aside" }, "1m 12s"), h("a", { href: "#" }, "Details"));
  return section("Patterns", "Rows that recur across screens, composed from the components above. Lists are rows on bg/surface inside a border/default card with radius/lg; each row is divided by border/subtle.",
    [["Commit row", ".commit-row"], ["Annotation row", ".annotation-row"], ["Branch row", ".branch-row"], ["Repository row", ".repository-row"], ["Issue row", ".issue-row"], ["Member row", ".member-row"], ["the list", ".list .row-text .row-title .row-meta"],
      ["Section header", ".section-header .section-header-title .section-header-description"], ["Filters", ".filters .filters-count"], ["Card/Row", ".card-row .card-row-text .card-row-title .card-row-meta .card-row-actions .card-row-label"],
      ["Card/Footer", ".card-footer"], ["Card Tone", ".card-success .card-danger"], ["Meter", ".meter .meter-share .meter-bar .meter-warning .meter-danger"],
      ["Table/Cell Number, Actions, Two lines", ".cell-number .cell-actions .cell-two-lines"]],
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
        h("button", { class: "btn btn-ghost btn-sm", type: "button" }, "Owner", icon("chevron-down")), ghost("ellipsis", "More actions"))),
    h("header", { class: "section-header" }, h("div", { class: "section-header-text" }, h("h2", { class: "section-header-title" }, "Section title"),
      h("p", { class: "section-header-description" }, "One line on what the section holds.")), h("button", { class: "btn", type: "button" }, icon("pencil"), "Action")),
    h("div", { class: "filters" }, h("div", { class: "input" }, icon("search"), h("input", { type: "search", placeholder: "Filter by name", "aria-label": "Filter by name" })),
      h("span", { class: "filters-count" }, "5 keys · 1 revoked")),
    h("section", { class: "card" },
      h("div", { class: "card-row" }, icon("key-round"), h("div", { class: "card-row-text" }, h("span", { class: "card-row-title" }, "Owner", h("span", { class: "tag" }, "Built-in")),
        h("span", { class: "card-row-meta" }, "Everything · 2 hold it")), h("div", { class: "card-row-actions" }, h("button", { class: "btn btn-ghost btn-sm", type: "button" }, "View"))),
      h("div", { class: "card-row", "aria-selected": "true" }, icon("key-round"), h("div", { class: "card-row-text" }, h("span", { class: "card-row-title" }, "Reviewer", h("span", { class: "tag tone-info" }, "Custom")),
        h("span", { class: "card-row-meta" }, "4 permissions · nobody holds it")), h("div", { class: "card-row-actions" }, h("button", { class: "btn btn-ghost btn-sm", type: "button" }, icon("pencil"), "Edit"),
        h("button", { class: "btn btn-ghost btn-sm", type: "button" }, "Delete"))),
      h("div", { class: "card-row" }, h("span", { class: "card-row-label" }, "Organization"), h("div", { class: "card-row-picks" },
        h("label", { class: "checkbox" }, h("input", { type: "checkbox", checked: true }), "Read the organization"), h("label", { class: "checkbox" }, h("input", { type: "checkbox" }), "Manage members"))),
      h("footer", { class: "card-footer" }, h("button", { class: "btn", type: "button" }, "Discard"), h("button", { class: "btn btn-primary", type: "button" }, "Save"))),
    ["success", "danger"].map((t) => h("section", { class: "card card-" + t }, h("header", { class: "card-header" }, h("h3", { class: "card-title" }, t === "success" ? "Key made" : "Danger zone")),
      h("div", { class: "card-body" }, t === "success" ? "A card that announces a thing done, shown once." : "Steps that can't be undone."))),
    h("section", { class: "card" }, [["", 0.41, "1,240 of 3,000"], [" meter-warning", 0.87, "2,610 of 3,000"], [" meter-danger", 1, "3,000 of 3,000"]].map(([t, share, value]) =>
      h("div", { class: "card-row" }, h("div", { class: "meter" + t }, h("div", { class: "meter-head" }, h("span", { class: "meter-label" }, "Job minutes"), h("span", { class: "meter-value" }, value)),
        h("div", { class: "meter-track" }, h("span", { class: "meter-bar", style: "width: " + share * 100 + "%" })), h("div", { class: "meter-share" + t }, h("span", { class: "meter-bar", style: "width: " + share * 100 + "%" })))))),
    h("table", { class: "table" }, h("thead", {}, h("tr", {}, h("th", {}, "Repository"), h("th", { class: "cell-number" }, "Total"), h("th", { "aria-label": "Actions" }))),
      h("tbody", {}, h("tr", {}, h("td", { class: "cell-two-lines" }, h("span", {}, "deploy-bot"), h("code", {}, "cbk_…3hWH")), h("td", { class: "cell-number" }, "1.2 GiB"),
        h("td", { class: "cell-actions" }, h("button", { class: "btn btn-ghost btn-sm", type: "button" }, "Rotate"), h("button", { class: "btn btn-ghost btn-sm", type: "button" }, "Revoke"))))));
}
