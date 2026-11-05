# The Cobranch design system

> **Snapshot.** This design lives in its [design system page](https://claude.ai/artifact/K6oZu5M6kiXuEBiqFNgPcW),
> linked with Cobranch's other designs; this file is a snapshot of it, with its diagrams, as of 2026-11-04, and synced
> again once Cobranch's shape settles.

**Decision**: Cobranch's interface puts clarity first and simplicity second. It is drawn in ink (near-black on white,
inverted in dark mode) with blue for its one primary action, uses other colour only where colour means something, and
says every status with an icon, a word and a tone. The source of truth is the Figma file
[Cobranch](https://www.figma.com/design/ZJVoycorwQStSjh89hnSrK/Cobranch), and the Console follows it: its tokens are
generated from the file's export into
[`tokens.css`](https://github.com/arikkfir-org/cobranch/blob/main/web/console/public/assets/tokens.css) (see
[Design to code](#design-to-code)).

Cobranch is a Kfirs project, © 2026 Arik Kfir. The design system is [ENG-194](https://linear.app/arikkfir/issue/ENG-194);
the screens are [ENG-200](https://linear.app/arikkfir/issue/ENG-200); bringing it to the Console is
[ENG-253](https://linear.app/arikkfir/issue/ENG-253) and the slices after it.

![The cover of the Cobranch Figma file](design-system/cover.png)

| Figma page | Holds |
| --- | --- |
| Cover | Title, status, a sample of the language, credit |
| Foundations | Principles, colour in light and dark, type scale, spacing, radii, elevation, layout |
| Icons | 70 Lucide icons as components |
| Components | 57 components: controls, badges, tags, avatars, menus, tables, cards, meters, counters, charts, dialogs, the app shell, list rows, brand |
| Screens | 67 screens: sign-in, sign-up, the organization's pages, the repository's, the branch (light and dark), the issue's, the agents', artifacts and settings |

## Principles

Six rules decide every screen; when a design breaks one, the design changes, not the rule.

1. **Ink for the interface, blue for its one action.** Chrome and text are near-black on white (inverted in dark mode);
   the primary action is blue. Other colour is reserved for meaning, so it stands out when it appears.
2. **Status is an icon, a word and a colour.** Never colour alone: a badge reads correctly in grayscale, to colour-blind
   users and in a screen reader.
3. **Violet means AI.** Agents, AI reviews and agent work use violet and the sparkles icon, and nothing else does. A
   person can always tell what a machine did.
4. **Shape tells who acted.** People are circles; agents, jobs, keys and apps are rounded squares; a commit author not
   yet linked to a user is a dashed circle.
5. **One primary action per view.** It is the only blue button on screen ("Merge", "New repository"); everything else
   is secondary or ghost.
6. **Code is mono, prose is sans.** Branch names, repository names, SHAs, ids, paths and environments use JetBrains
   Mono; everything people wrote uses Inter.

![The Foundations page: principles, colour, type, spacing, radii and elevation](design-system/foundations.png)

## How it fits together

**Two layers of colour, components from tokens only, screens from instances only**

![Two layers of colour, components from tokens only, screens from instances only](design-system/layers.svg)

A visual change is made in Figma first; `tokens.css` is generated from the file's export, and a page composes the
classes that map its components. A fix to a component reaches every screen and every page.

## Colour

Colour has two layers: primitives hold raw values, and semantic tokens alias them per mode. Components use semantic
tokens only, and Light and Dark are two modes of the one `Color` collection, so a frame changes theme by switching its
mode. Both themes are equal: the system's setting decides, and a person may override it. The choice is kept with the person (`/v1/me/preferences`), so every browser they sign in from follows it; each browser keeps a copy to draw its first frame in the right theme.

**Primitives** (`Primitives`): a cool gray in 15 steps, from `gray/0` #ffffff to `gray/1000` #0c0d0f; blue, green,
amber, red and violet in 11 steps each (50 to 950); and black at 6, 10, 12, 40, 50 and 60% for overlays and shadows.

**Semantic tokens** (`Color`):

| Token | Light | Dark | Use |
| --- | --- | --- | --- |
| `bg/canvas` | gray/0 | gray/950 | Page background |
| `bg/surface` | gray/0 | gray/900 | Cards, panels, inputs |
| `bg/sunken` | gray/50 | gray/1000 | Code chips, wells |
| `bg/hover` | gray/100 | gray/850 | Hovered rows and ghost buttons |
| `bg/selected` | gray/150 | gray/800 | Selected nav item or row |
| `bg/inverse` | gray/900 | gray/50 | Merged badge, tooltips, the mark |
| `bg/overlay` | black 40% | black 60% | Behind dialogs |
| `fg/default` | gray/900 | gray/50 | Text and icons |
| `fg/muted` | gray/600 | gray/400 | Secondary text, metadata |
| `fg/subtle` | gray/500 | gray/500 | Placeholders |
| `fg/disabled` | gray/400 | gray/700 | Disabled controls |
| `fg/inverse` | gray/0 | gray/950 | Text on `bg/inverse` |
| `fg/link` | blue/600 | blue/400 | Links |
| `border/default` | gray/200 | gray/800 | Cards and controls |
| `border/subtle` | gray/150 | gray/850 | Dividers between rows |
| `border/strong` | gray/300 | gray/700 | Inputs, the composer |
| `border/focus` | blue/500 | blue/400 | Focus ring |
| `action/primary/bg`, `bg-hover`, `fg` | blue/600, blue/700, gray/0 | blue/500, blue/400, gray/0 | The one primary button |
| `action/secondary/bg`, `bg-hover`, `border`, `fg` | gray/0, gray/100, gray/300, gray/900 | gray/900, gray/850, gray/700, gray/50 | Secondary buttons |
| `action/danger/bg`, `bg-hover`, `fg` | red/600, red/700, gray/0 | red/600, red/500, gray/0 | Destructive confirmations |
| `avatar/user/bg`, `fg` | gray/150, gray/700 | gray/800, gray/200 | People |
| `avatar/agent/bg`, `fg` | violet/600, gray/0 | violet/500, gray/0 | Agents |
| `avatar/machine/bg`, `fg` | gray/700, gray/0 | gray/600, gray/0 | Jobs, keys, apps |
| `shadow/soft`, `shadow/strong` | black 6%, 12% | black 40%, 60% | Elevation |

**Status tones** (`status/<tone>/fg`, `bg`, `border`, `solid`): Light uses steps 700, 50, 200 and 500 of the tone's hue;
Dark uses 300, 950, 800 and 400. Neutral differs slightly: its `bg` is gray/100 in light and gray/850 in dark, its dark
`border` gray/700 and its dark `solid` gray/500.

| Tone | Hue | Means |
| --- | --- | --- |
| `neutral` | gray | No state yet, empty, paused, optional |
| `info` | blue | In progress, processing, syncing |
| `success` | green | Passed, approved, ready, synced |
| `warning` | amber | Needs a person: changes requested, draft |
| `danger` | red | Failed, blocked |
| `ai` | violet | Made by or about an agent |

An environment's Badge/Quiet pair keeps its sync quiet: its health carries the green, so Synced is neutral beside it, and
Paused, Out of sync and a Missing object are warning, each waiting on a person; Out of sync turns danger once the
newest delivery failed. The Repository screen's tags keep Synced success and Paused neutral.

Contrast: body and metadata text pass WCAG AA in both modes (`fg/muted` 4.8:1 light, 8.8:1 dark), and every status `fg`
on its `bg` is at least 6:1. `fg/subtle` is 3.1:1 in light, so it holds placeholders only, never information.

## Typography

Two families and 14 text styles cover every screen: Inter for prose and interface, JetBrains Mono for anything that is
code, a name or an id. Both are open-source (SIL Open Font License) and served with the Console. Body text is 13 to
14 px, dense enough for lists of branches and commits and still easy to read.

| Style | Font | Size / line height (px) | Use |
| --- | --- | --- | --- |
| Display | Inter Semi Bold | 30 / 36, -0.6 px | Sign-in and empty states |
| Heading/Large | Inter Semi Bold | 24 / 32, -0.4 px | Page titles |
| Heading/Medium | Inter Semi Bold | 18 / 26, -0.2 px | Dialog titles |
| Heading/Small | Inter Semi Bold | 15 / 22, -0.1 px | Card titles |
| Body/Large | Inter Regular | 16 / 24 | Lead paragraphs |
| Body/Medium | Inter Regular | 14 / 20 | Descriptions, issue text |
| Body/Medium Strong | Inter Medium | 14 / 20 | Emphasis, callout titles |
| Body/Small | Inter Regular | 13 / 18 | Rows, metadata, chat |
| Body/Small Strong | Inter Medium | 13 / 18 | Names in rows |
| Label/Small | Inter Medium | 12 / 16 | Form labels, section labels |
| Caption | Inter Regular | 12 / 16 | Timestamps, counts |
| Code/Large | JetBrains Mono Medium | 20 / 28 | Branch name as a page title |
| Code/Medium | JetBrains Mono Regular | 13 / 20 | Branch names in rows, code blocks |
| Code/Small | JetBrains Mono Regular | 12 / 16 | SHAs, issue keys, file paths, environments |

Weights stop at Semi Bold: hierarchy comes from size and colour (`fg/default` against `fg/muted`), not from bold text.

## Spacing, radii and elevation

Spacing follows a 4 px grid with a 2 px half-step, and every padding, gap and radius in the file is bound to a token, so
a change to a token moves every screen. The density is comfortable: rows are 36 px, table rows included, each above its 1 px divider.

| Group | Tokens (px) | Use |
| --- | --- | --- |
| `space/*` | 0, 2, 4, 6, 8, 12, 16, 20, 24, 32, 40, 48, 64 | 8 to 12 inside controls, 16 inside cards, 24 between page sections, 32 page margins |
| `size/control-*` | sm 28, md 32, lg 40 | Button and input heights |
| `size/icon-*` | sm 16, md 20 | Icons |
| `size/row`, `size/row-sm` | 36, 32 | Rows and table rows; `row-sm` is no longer used |
| `size/topbar`, `size/sidebar`, `size/pane` | 48, 240, 320 | App shell |
| `radius/*` | none 0, xs 2, sm 4, md 6, lg 8, xl 12, full 999 | xs for a chart's bars; sm for tags and code chips; md for buttons, inputs and menu items; lg for cards, menus and machine avatars; xl for dialogs; full for people, status badges and counts |

**Elevation** is rare: content sits on flat cards with a 1 px border, and shadows mark only what floats above the page.

| Style | Shadow | Use |
| --- | --- | --- |
| Elevation/1 | 0 1 2 `shadow/soft` | Tooltips and toasts |
| Elevation/2 | Elevation/1 + 0 4 12 -2 `shadow/strong` | Menus, popovers |
| Elevation/3 | 0 2 6 `shadow/soft` + 0 12 32 -4 `shadow/strong` | Dialogs |
| Focus/Ring | 2 px `bg/canvas` + 4 px `border/focus` | Keyboard focus on any control |

## The marks

The mark puts a person and an agent on one trunk, in an ink square (`bg/inverse`, the glyph `fg/inverse`); at 16 px it
gives way to the small mark, the branch graph, which is also the favicon. Both are components of the file, and the
Console draws them from its export.

| Component | What it is | Where |
| --- | --- | --- |
| [Brand/Logo](https://www.figma.com/design/ZJVoycorwQStSjh89hnSrK/Cobranch?node-id=11-395) | The mark (28 px) and the wordmark, Inter Semi Bold 17 px | The sidebar, sign-in and the Console's cards |
| [Brand/Mark small](https://www.figma.com/design/ZJVoycorwQStSjh89hnSrK/Cobranch?node-id=73-594) | The branch graph in a 16 px ink square, a 1.5 px stroke | Below 24 px, and the favicon |
| [Brand/Credit](https://www.figma.com/design/ZJVoycorwQStSjh89hnSrK/Cobranch?node-id=11-403) | "Cobranch is a Kfirs project" | The foot of the sidebar |

## Layout

A fixed sidebar and top bar, and only the content scrolls. The sidebar holds, top to bottom: the logo (to Overview),
Overview and Activity, then Issues, Repositories and Agents, each a section with its last five children (agents with a status dot),
then the organization switcher, Settings and the credit line. The top bar holds the breadcrumbs, the conversation
toggle and the avatar menu (Account; Theme, a Light, Dark and System segmented control; Preferences; Log out). Tasks
have no page of their own: they live on their agent's page and on the issue or branch they came from.

**The app shell: sidebar, top bar, content and the conversation pane**

![The app shell: sidebar, top bar, content and the conversation pane](design-system/shell.svg)

**The conversation pane** is one pane on the right, 320 px, toggled from the top bar, with pin and close. Its subject
follows the page; its header names the subject, a scope line says who sees it, and "Ask" starts an agent's task from
the composer.

| Page | Conversation | Who is in it |
| --- | --- | --- |
| Organization (Overview, Settings) | your organization chat | you; tag an agent to bring it in |
| Repository, and its files, jobs and artifacts | your repository chat | you; tag an agent to bring it in |
| Issue | the issue's chat | everyone who can see the issue, agents included |
| Branch, and any commit, file or job in it | the branch's chat | everyone who can see the branch, agents included |
| Agent | your chat with that agent | you and the agent, which knows its tasks and queue |

Settings are tabs, scoped to the organization: General, Members, Roles, API keys, Agents, Secrets, Models and Usage.
Organization-level lists (repositories, issues, agents, models) are tables with a header row; lists inside a page
(branches on a repository, tasks on an agent) are rich two-line rows; both share the card, the row height and the
dividers. A table sits in a bordered card only when the card holds more than the table; otherwise the table is the
card. Labels sit above their fields.

## Iconography

Icons come from [Lucide](https://lucide.dev) (ISC licence), drawn at 16 px with a 1.5 px stroke and round joins, so they
match Inter's weight at 13 to 14 px. The file holds 70 of them as components named `icon/<lucide name>`; their stroke is
bound to `fg/default`, and an instance takes the colour of its context.

- An icon never stands alone for a state: it sits beside a word, or carries a tooltip when space is short (icon
  buttons).
- One icon, one meaning across the product. The fixed ones:

| Meaning | Icon |
| --- | --- |
| Branch, merge, commit | `git-branch`, `git-merge`, `git-commit-horizontal` |
| Repository | `folder-git-2` |
| Processing, in progress | `loader-circle` |
| Success, ready | `circle-check` |
| Failed | `circle-x` |
| Blocked | `octagon-alert` |
| Pending | `clock` |
| Empty | `circle-dashed` |
| Approved | `badge-check` |
| Changes requested | `circle-alert` |
| Agent, AI | `sparkles` |
| Job, key, app | `cog`, `key-round` |
| Chat | `message-square` |
| Delivery, environment | `globe` |

![The Icons page](design-system/icons.png)

## Components

The Components page holds 57 components built only from the tokens above; screens are assembled from their instances,
so a fix to a component reaches every screen. In the Console each maps to one class named after it (`Badge/Quiet` is
`.badge-quiet`), in `components.css`, and a page composes those classes and styles no component of its own; the app
shell's components (App/\*, Nav item, Nav/Sub item, Chat message and Brand/Credit) are drawn around every page by
`lib/shell.css`, one root class each. The Console's `/help/components` page, behind the sign-in, draws every class of `components.css` and the
marks in both themes, for review beside the file. Each component in the file links its source and ends its description
with the classes that draw it; Figma's Code Connect would do this in Dev Mode, but needs an Organization or Enterprise
plan.

| Component | Variants | Properties | In the Console |
| --- | --- | --- | --- |
| Brand/Logo, Brand/Mark small, Brand/Credit | None | None: the large mark with the wordmark, the small mark (also the favicon), the credit line | `logo()` and `markSmall()` in `lib/brand.js`; the credit `.app-sidebar-credit` (`lib/shell.css`) |
| Button | Style: Primary, Secondary, Ghost, Danger × Size: Medium 32, Small 28 × State: Default, Hover, Disabled | Label, leading icon, trailing icon (each optional, swappable) | `.btn`, `.btn-primary`, `.btn-secondary`, `.btn-ghost`, `.btn-danger`, `.btn-sm` |
| Icon button | Style: Secondary, Ghost × Size: Medium, Small | Icon | `.icon-btn`, `.icon-btn-ghost`, `.icon-btn-sm` |
| Input, Select, Textarea | State: Default, Focus, Error, Disabled | Label, value, helper text (each optional); Input also a leading icon | `.field` with `.field-label` and `.field-help`, around `.input`, `.select` or `.textarea` |
| Copy field | None | Label, value, helper text; a Ghost Small copy Icon button inside | `.copy-field` in a `.field`, holding a `code` and an `.icon-btn` (`copyField()` in `lib/forms.js`) |
| Checkbox, Radio | Checked: Off, On × State: Default, Disabled | Label | `.checkbox`, `.radio`: a label around the native input |
| Segmented control | Selected: 1, 2, 3 | Three options (the theme: Light, Dark, System) | `.segmented`, with `aria-pressed` on the chosen button |
| Tab | State: Default, Selected | Label, icon, count | `.tab-bar`, `.tab`, `.tab-count` |
| Menu, Menu item | Item: Tone Default, Danger × State Default, Hover, Disabled | Label, icon, shortcut, checked | `.menu`, `.menu-item`, `.menu-item-danger`, `.menu-shortcut`, `.menu-divider` |
| Nav item, Nav/Sub item | State: Default, Hover, Selected (Sub item: Default, Selected) | Label, icon, count; Sub item a leading glyph and a status dot | `.nav-item`, `.nav-sub-item` (`lib/shell.css`) |
| Table/Header cell, Table/Cell, Table/Row | Cell kind: Text, Muted, Mono, Number, Actions, Two lines, Issue, Person, Unassigned, Badge, Avatar; Row state: Default, Muted, Selected | Text; Actions a small Ghost button; Two lines a title over its meta, after a 20px icon in a column that fills the row (a repository's); Issue its state icon, key, title and tags, filling the row; Person an avatar and a name; Badge a Badge/Quiet; Avatar a 20px avatar alone; Unassigned a subtle word or dash | `.table`, `.cell-muted`, `.cell-mono`, `.cell-number`, `.cell-actions`, `.cell-two-lines`, `.cell-issue`, `.cell-person`, `.cell-unassigned`; `.row-muted`, `.row-selected` on a `tr` |
| Badge/Branch status | Processing, Blocked, Empty, Merged, Ready | None: the status sets icon, word and tone | `.badge` with a `.tone-*` class |
| Badge/Annotation | Pending, In progress, Failed, Successful | None | `.badge` with a `.tone-*` class |
| Badge/Review | Approved, Changes requested, Neutral | None | `.badge` with a `.tone-*` class |
| Badge/Quiet | Tone: Neutral, Info, Success, Warning, Danger, AI | Label: a dot and a word, for lists | `.badge-quiet` with a `.tone-*` class |
| Tag | Tone: Neutral, Info, Success, Warning, Danger, AI | Label, icon | `.tag` with a `.tone-*` class |
| Avatar | Kind: User, Unclaimed, Agent, Machine × Size: 20, 24, 32, 40 | Initials (people), glyph (machines) | `.avatar`, `.avatar-unclaimed`, `.avatar-agent`, `.avatar-machine`, `.avatar-24`, `.avatar-32`, `.avatar-40` |
| Card, Page header | Card: Tone Default, Success, Danger | Title, meta, action (a button, or a link such as View all), body; description, actions | `.card` (`.card-success`, `.card-danger`), `.page-header` |
| Dialog | None | Title, body, secondary action | `.dialog` on a `dialog` |
| Banner | Tone: Info, Success, Warning, Danger | Title, message, action, dismiss | `.banner` with `.banner-info`, `.banner-success`, `.banner-warning` or `.banner-danger` |
| Empty state, Tooltip | None | Icon, title, description, action; label | `.empty`, and `.empty-centred` on a card, a card's body or a screen that shows nothing else, which holds it at 480px with 48px above and below; `.tooltip`, or `data-tooltip` on what it labels |
| App/Sidebar, App/Top bar | None | Organization, user; breadcrumb context and current page | `.app-sidebar`, `.app-topbar` (`lib/shell.css`) |
| App/Conversation pane, App/User menu | None | Subject and scope; the user menu holds the theme | `.app-pane`, `.app-user-menu` (`lib/shell.css`) |
| Commit row | None | Message, meta, SHA; author avatar and annotation icons nested | `.commit-row` in a `.list` |
| Annotation row | Status (as Badge/Annotation) | Name, description, duration; creator avatar nested | `.annotation-row` in a `.list` |
| Branch row | None | Name, meta, message count; status badge and author avatar nested | `.branch-row` in a `.list` |
| Chat message | Author: Person, Agent | Name, time, body, code reference | `.chat-message` (`lib/shell.css`) |
| Repository row, Issue row, Member row | None | Name or title, description or email, meta; tags, avatars and role nested | `.repository-row`, `.issue-row`, `.member-row` in a `.list` |
| Filters, Section header | None | A 320px search Input, count; title, description, a Secondary action | `.filters`, `.filters-count`; `.section-header` |
| Card/Row, Card/Footer | Row: State Default, Hover, Selected | Icon, title, meta, a small Ghost action; Primary and Secondary actions | `.card-row` in a `.card` (`.card-row-label` for a 200px label column); `.card-footer` |
| Fact row | Kind: Text, Code, Editable, Person, Who | A muted 104px label and a value; Code the value in Code/Small, for slugs, branches, SHAs and ids; Editable a Small Ghost button with a chevron, opening a menu; Person a 32px avatar, the name over the email; Who a 20px avatar before the name | `.facts` in a `.card`, of `.fact-row` (`.fact-label`, `.fact-value`); Code `.fact-row-code`; Person `.fact-row-person` (`.fact-person`, `.fact-person-name`, `.fact-person-email`); Who `.fact-row-who` |
| Activity/Event | None | Who did what, and when | `.event-row` |
| Activity/Item | None | Whoever did it as Avatar 20 of any kind, the sentence, which wraps, and how long ago | `.activity-item` in an `.activity-list` |
| Pattern/Agent row | Status: Working (Info), Watching (AI), Idle (Neutral) | Name, what it does now, which wraps; Avatar Agent 24 and the status Tag nested | `.agent-row` in a `.list` |
| Run/Step row, Log/Line | Step row: Status Successful, Failed, Running, Not started, Unknown (a step of a run kept before runs kept their steps' states), Definition; State Default, Selected | A step's status icon, name and how long it ran; a log line's time since its step started, and its text | `.step-row` (`.step-row-name`, `.step-row-time`, `.step-row-quiet`) in a `.card`; `.log` of `.log-line` (`.log-time`, `.log-text`), `.log-note` |
| Swatch | State: Default, Chosen | A label's colour as the Tag it gives, padded inside 1px of transparent border; Chosen ringed 2px in border/focus, its Tag with the check | `.swatches` of `.swatch` (a radio and its `.tag`), `.is-chosen` |
| Meter | Kind Full, Share; Tone Default, Warning (from 80%), Danger (at the cap) | Label, value | `.meter` (`.meter-head`, `.meter-label`, `.meter-value`, `.meter-track`), `.meter-share`, `.meter-bar`; `.meter-warning`, `.meter-danger` |
| Stat | None | A counter: its label in Label/Small with its icon at the end, its value in Heading/Large, and a Badge/Quiet under it that says more | `.stat` (`.stat-label`, `.stat-value`) and a `.badge-quiet` |
| Chart/Bars | State: Default, Hover; Tone: Info, AI, Accent | A Card of a value by day: title and total, a bar a day at most 10 px wide and 4 px apart, the first and last day; a pointed bar turns fg/default under a tooltip of its day and value | `.chart-bars` (`.tone-ai`, `.tone-accent`) in a `.card`, of `.chart-bars-plot`, `.chart-bars-bar`, `.chart-bars-tooltip` and `.chart-bars-axis` |

Two rules keep instances honest: never detach one to change it (add a property to the component instead), and never
place a node inside an instance (Figma refuses it; compose rows beside it).

![The Components page](design-system/components.png)

## Status language

Cobranch has three status vocabularies, one per object, and each word maps to exactly one icon and one tone everywhere it
appears: badges, rows, the branch view's requirements and the commit row's annotation icons. A status is a tinted pill
by default, and the quiet variant, a dot and a word, in lists.

| Object | Status | Icon | Tone |
| --- | --- | --- | --- |
| Branch | Processing | `loader-circle` | info |
| Branch | Blocked | `octagon-alert` | danger |
| Branch | Ready | `circle-check` | success |
| Branch | Empty | `circle-dashed` | neutral |
| Branch | Merged | `git-merge` | inverse (ink) |
| Annotation | Pending | `clock` | neutral |
| Annotation | In progress | `loader-circle` | info |
| Annotation | Failed | `circle-x` | danger |
| Annotation | Successful | `circle-check` | success |
| Review | Approved | `badge-check` | success |
| Review | Changes requested | `circle-alert` | warning |
| Review | Neutral | `message-square` | neutral |

Merged is ink rather than a colour: it is final, not a state anyone has to act on. A review that requests changes blocks
its branch, so the branch reads Blocked (danger) while the review itself reads Changes requested (warning). A blocked
branch always says why in the first line of its view, then lists each merge requirement with its own icon, so nobody
has to open the annotations to find the cause.

**A review that requests changes blocks its branch, and the branch says why**

![A review that requests changes blocks its branch, and the branch says why](design-system/blocked.svg)

How a branch's status is computed is the [product design's](product.md#branches).

## Principals and avatars

Every actor in Cobranch is a principal, and its avatar's shape and colour say what kind it is before its name is read.

| Kind | Shape | Fill | Content | Example |
| --- | --- | --- | --- | --- |
| User | Circle | `avatar/user` (light gray) | Initials | Arik Kfir, AK |
| Unclaimed | Dashed circle | None, `fg/subtle` outline | Initials from the commit | A commit author whose email no user has claimed |
| Agent | Rounded square (`radius/lg`) | `avatar/agent` (violet) | `sparkles` | Builder, Reviewer |
| Machine | Rounded square | `avatar/machine` (dark gray) | `cog`, or `key-round` for a key | Jobs, deploy keys, apps |

Sizes are 20 (inline in text and properties), 24 (reviews, participants), 32 (chat, members) and 40 (cover, profile).
Agents also carry an AI tag ("Agent", with `sparkles`) next to their name in chat and reviews, so the shape is never the
only cue.

## Screens

Sixty-seven screens on the Screens page, at 1440 wide unless named for a narrower one, and built only from library instances. They follow one
organization, Kfirs, its repositories and its three agents: Builder implements issues, Reviewer reviews every push, and
Researcher waits for a task.

| Screen | Shows |
| --- | --- |
| [Sign in](https://www.figma.com/design/ZJVoycorwQStSjh89hnSrK/Cobranch?node-id=12-2) | Google, GitHub and SSO, or email; a link to sign up |
| [Sign up: organization](https://www.figma.com/design/ZJVoycorwQStSjh89hnSrK/Cobranch?node-id=12-79) | Step 2 of 2: create an organization or ask to join one |
| [Overview](https://www.figma.com/design/ZJVoycorwQStSjh89hnSrK/Cobranch?node-id=13-60) | Counters, then tasks, spend and job minutes by day, then the same by repository, over a period: this month, last month or the last 30 days |
| [Overview · Empty](https://www.figma.com/design/ZJVoycorwQStSjh89hnSrK/Cobranch?node-id=131-12276) | An organization with nothing to count yet, and New repository |
| [Overview · 1024](https://www.figma.com/design/ZJVoycorwQStSjh89hnSrK/Cobranch?node-id=202-28680) | Below 1200 wide the counters wrap, four to a row, and a chart's total goes under its title |
| [Overview · Phone](https://www.figma.com/design/ZJVoycorwQStSjh89hnSrK/Cobranch?node-id=202-28173) | At 390: the header stacks, two counters to a row, the charts stacked, and the table keeps Repository and Spend |
| [Activity](https://www.figma.com/design/ZJVoycorwQStSjh89hnSrK/Cobranch?node-id=124-12015) | What needs you, the repositories pushed to last, the agents at work and what happened |
| [Repositories](https://www.figma.com/design/ZJVoycorwQStSjh89hnSrK/Cobranch?node-id=69-3127) | Every repository as a table: visibility, default branch, open branches, status, last push |
| [New repository](https://www.figma.com/design/ZJVoycorwQStSjh89hnSrK/Cobranch?node-id=131-12992) | A card of a repository's name and description; Create repository opens it |
| [Repository](https://www.figma.com/design/ZJVoycorwQStSjh89hnSrK/Cobranch?node-id=15-646) | Branches by status (no pull requests), merge requirements, environments |
| [Files](https://www.figma.com/design/ZJVoycorwQStSjh89hnSrK/Cobranch?node-id=117-9579) | A directory at a ref: its latest commit, each entry beside the commit that last changed it, the README; Go to file |
| [File](https://www.figma.com/design/ZJVoycorwQStSjh89hnSrK/Cobranch?node-id=117-10012) | A file as numbered lines with Raw, Blame and History; its Markdown, binary, image, symlink, threads, empty and unreadable states beside it |
| [Commits](https://www.figma.com/design/ZJVoycorwQStSjh89hnSrK/Cobranch?node-id=135-13176) | A ref's history: the commits a card per day beside a graph of their lines, a filter and the count; a path's history, filtered, empty and unknown-ref states beside it |
| [Commit](https://www.figma.com/design/ZJVoycorwQStSjh89hnSrK/Cobranch?node-id=135-13670) | One commit: its message, who wrote and pushed it, its annotations and jobs, its diff Unified or Split |
| [Branch](https://www.figma.com/design/ZJVoycorwQStSjh89hnSrK/Cobranch?node-id=18-1149) | Why it is blocked, merge requirements, commits, annotations, reviews; the branch chat in the pane |
| [Branch · Dark](https://www.figma.com/design/ZJVoycorwQStSjh89hnSrK/Cobranch?node-id=19-1864) | The same frame in the Dark mode, unchanged otherwise |
| [Branch · Files changed](https://www.figma.com/design/ZJVoycorwQStSjh89hnSrK/Cobranch?node-id=135-14230) | Its files listed, then each file's change with the chat's threads under their lines; a thread with replies, a reply open, a resolved thread and a file marked Viewed, folded and checked off, beside it |
| [Branch · Jobs](https://www.figma.com/design/ZJVoycorwQStSjh89hnSrK/Cobranch?node-id=135-14769) | Its pushes, newest first, each a card of its jobs' runs as Annotation rows, the head's with Re-run all, and a link that opens the earlier pushes |
| [Branch · Environment](https://www.figma.com/design/ZJVoycorwQStSjh89hnSrK/Cobranch?node-id=138-14602) | Each environment it delivers to, a card: its health, how it stands and its URL; its cluster, namespace, renders and when it is torn down; its objects with their health, readiness and age; its deliveries of commits and teardowns; Open, Redeliver or Resume and Pause. Its failed, waiting, paused, tearing-down, degraded and no-environment states beside it |
| [Jobs](https://www.figma.com/design/ZJVoycorwQStSjh89hnSrK/Cobranch?node-id=138-15120) | A repository's runs as a table: job, branch, commit, status, when, how long and who caused it, under a filter, Any job, Any branch, Any status and the count; empty, no-match and status menu states beside it |
| [Run](https://www.figma.com/design/ZJVoycorwQStSjh89hnSrK/Cobranch?node-id=138-15661) | One run: its job, status and actions, what it ran and why, its steps beside the selected step's log, each line with its time, or its job's definition; running, failed, waiting, webhook, no-logs and not-found states beside it |
| [Repository · Delivery](https://www.figma.com/design/ZJVoycorwQStSjh89hnSrK/Cobranch?node-id=138-16082) | The repository's environments, each with its health, how it stands, its branch, URL and newest delivery, and Open; the targets main's /cobranch.yaml declares; no-targets state beside it |
| [Repository · Environment](https://www.figma.com/design/ZJVoycorwQStSjh89hnSrK/Cobranch?node-id=240-39358) | One environment's card, the full width, under a button back to Delivery |
| [Issues](https://www.figma.com/design/ZJVoycorwQStSjh89hnSrK/Cobranch?node-id=70-3370) | The organization's open issues as a table: labels, repository, assignee, branch, last change |
| [Issues · Closed](https://www.figma.com/design/ZJVoycorwQStSjh89hnSrK/Cobranch?node-id=156-16863) | The Closed tab: done issues and canceled ones, each by its state's icon |
| [Issues · Filter set](https://www.figma.com/design/ZJVoycorwQStSjh89hnSrK/Cobranch?node-id=156-17300) | A filter picked from its menu, which the button then names |
| [Issues · Assignee menu](https://www.figma.com/design/ZJVoycorwQStSjh89hnSrK/Cobranch?node-id=164-20377) | The Assignee filter: anyone, you, nobody, then members, then agents |
| [Issues · Label menu](https://www.figma.com/design/ZJVoycorwQStSjh89hnSrK/Cobranch?node-id=164-20839) | The Label filter: each label as its tag, and the way to the labels |
| [Issues · No issues yet](https://www.figma.com/design/ZJVoycorwQStSjh89hnSrK/Cobranch?node-id=131-12658) | The organization before its first issue |
| [Issues · No open issues](https://www.figma.com/design/ZJVoycorwQStSjh89hnSrK/Cobranch?node-id=168-20779) | Every issue closed; New issue opens the next |
| [Issues · No closed issues](https://www.figma.com/design/ZJVoycorwQStSjh89hnSrK/Cobranch?node-id=168-21055) | The Closed tab before any issue closes |
| [Issues · No issue matches](https://www.figma.com/design/ZJVoycorwQStSjh89hnSrK/Cobranch?node-id=168-21330) | Filters that match nothing, said back, with Clear filters |
| [Issues · 1024](https://www.figma.com/design/ZJVoycorwQStSjh89hnSrK/Cobranch?node-id=169-22273) | Below 1100 wide the Branch column goes |
| [Issues · Phone](https://www.figma.com/design/ZJVoycorwQStSjh89hnSrK/Cobranch?node-id=169-22902) | At 390: filters wrap, the table keeps Issue and Assignee |
| [Issues · Labels](https://www.figma.com/design/ZJVoycorwQStSjh89hnSrK/Cobranch?node-id=164-19483) | The organization's labels: each as its tag, what it is for, how many issues carry it |
| [Issues · Labels · New label](https://www.figma.com/design/ZJVoycorwQStSjh89hnSrK/Cobranch?node-id=164-20183) | Making a label, in a dialog: its name, its colour picked from swatches of its tag, its description |
| [New issue](https://www.figma.com/design/ZJVoycorwQStSjh89hnSrK/Cobranch?node-id=131-13409) | A card of an issue's repository, title, description, assignee and labels |
| [Issue](https://www.figma.com/design/ZJVoycorwQStSjh89hnSrK/Cobranch?node-id=15-1127) | Description, linked branches, a read-only activity timeline, properties changed from their menus, "Assign to an agent"; its comments live in the conversation pane |
| [Issue · Agent working](https://www.figma.com/design/ZJVoycorwQStSjh89hnSrK/Cobranch?node-id=168-21606) | The task its agent works on it in, and its start in the activity |
| [Issue · More menu](https://www.figma.com/design/ZJVoycorwQStSjh89hnSrK/Cobranch?node-id=168-21914) | Copy branch name, Copy link, and Delete issue |
| [Issue · New](https://www.figma.com/design/ZJVoycorwQStSjh89hnSrK/Cobranch?node-id=168-22186) | An issue just opened: no description, no branch, one event |
| [Issue · 1024](https://www.figma.com/design/ZJVoycorwQStSjh89hnSrK/Cobranch?node-id=169-22671) | Below 1100 wide the properties go under the activity |
| [Issue · Phone](https://www.figma.com/design/ZJVoycorwQStSjh89hnSrK/Cobranch?node-id=169-23300) | At 390, stacked the same way |
| [Agents](https://www.figma.com/design/ZJVoycorwQStSjh89hnSrK/Cobranch?node-id=68-2926) | Tabs Agents, Activity and Quotas; each agent's model, status and cap |
| [Agents · Activity](https://www.figma.com/design/ZJVoycorwQStSjh89hnSrK/Cobranch?node-id=94-5230) | What the organization's agents did, newest first |
| [Agents · Quotas](https://www.figma.com/design/ZJVoycorwQStSjh89hnSrK/Cobranch?node-id=94-5627) | Spend this month, by agent against its cap and by model |
| [Agent](https://www.figma.com/design/ZJVoycorwQStSjh89hnSrK/Cobranch?node-id=64-2654) | Working on, Queue and History of its tasks, its settings, and your private chat with it |
| [Agent · Task](https://www.figma.com/design/ZJVoycorwQStSjh89hnSrK/Cobranch?node-id=81-4443) | One task: its transcript of turns and tool calls, what it came from, and its spend against the cap |
| [Agent · Empty](https://www.figma.com/design/ZJVoycorwQStSjh89hnSrK/Cobranch?node-id=95-5663) | An agent with no tasks yet |
| [Repository · Settings](https://www.figma.com/design/ZJVoycorwQStSjh89hnSrK/Cobranch?node-id=138-16990) | Settings as tabs General, Access and Secrets; the repository's name, URL name and description, and its visibility, which its grants decide |
| [Repository · Settings · Access](https://www.figma.com/design/ZJVoycorwQStSjh89hnSrK/Cobranch?node-id=215-30550) | Every role that reaches the repository, a row each: who, the role, Direct or the organization's role it comes through, since when; Revoke on the direct ones |
| [Repository · Settings · Access · Grant role](https://www.figma.com/design/ZJVoycorwQStSjh89hnSrK/Cobranch?node-id=224-34472) | Granting a member, API key or agent one of the repository's roles, in a dialog |
| [Repository · Settings · Secrets](https://www.figma.com/design/ZJVoycorwQStSjh89hnSrK/Cobranch?node-id=215-30983) | The repository's own secrets, which its jobs read beside the organization's |
| [Repository · Issues](https://www.figma.com/design/ZJVoycorwQStSjh89hnSrK/Cobranch?node-id=138-16506) | The organization's issues about the repository, under tabs Open, Closed and All, filtered by assignee and label |
| [Repository · Artifacts](https://www.figma.com/design/ZJVoycorwQStSjh89hnSrK/Cobranch?node-id=75-3941) | What agents generated for the repository: documents, diagrams, reports and pages |
| [Artifact](https://www.figma.com/design/ZJVoycorwQStSjh89hnSrK/Cobranch?node-id=75-4455) | The document, About, Access and Versions; Share, Download and Edit |
| [Settings · General](https://www.figma.com/design/ZJVoycorwQStSjh89hnSrK/Cobranch?node-id=107-7859) | Settings as tabs; the organization's name, slug and issue key, who joins with what role, and its identity providers |
| [Settings · Members](https://www.figma.com/design/ZJVoycorwQStSjh89hnSrK/Cobranch?node-id=13-554) | A request to join, members with roles, invited people |
| [Settings · Roles](https://www.figma.com/design/ZJVoycorwQStSjh89hnSrK/Cobranch?node-id=107-8281) | Built-in and custom roles, each a set of permissions |
| [Settings · API keys](https://www.figma.com/design/ZJVoycorwQStSjh89hnSrK/Cobranch?node-id=109-8694) | The organization's keys with their roles and secrets, made, rotated and revoked |
| [Settings · API keys · New key](https://www.figma.com/design/ZJVoycorwQStSjh89hnSrK/Cobranch?node-id=109-9086) | Making a key, in a dialog, and its secret shown once |
| [Settings · Secrets](https://www.figma.com/design/ZJVoycorwQStSjh89hnSrK/Cobranch?node-id=109-9367) | The organization's own secrets, which its repositories' jobs share |
| [Settings · Usage](https://www.figma.com/design/ZJVoycorwQStSjh89hnSrK/Cobranch?node-id=109-9789) | What the organization uses against the installation's limits, and each repository's Git data |
| [Settings · Agents](https://www.figma.com/design/ZJVoycorwQStSjh89hnSrK/Cobranch?node-id=95-6091) | The organization's agents and their settings |
| [Settings · Models](https://www.figma.com/design/ZJVoycorwQStSjh89hnSrK/Cobranch?node-id=71-3605) | Providers (a secret and the models it serves) and models with their prices, the agents on each and the default; no cap, which is each agent's |
| [Settings · Models · Add model](https://www.figma.com/design/ZJVoycorwQStSjh89hnSrK/Cobranch?node-id=92-4735) | Adding a model, in a dialog |
| [Settings · Models · Empty](https://www.figma.com/design/ZJVoycorwQStSjh89hnSrK/Cobranch?node-id=92-4960) | Models before any provider is added |

Error states are drawn beside the screen they belong to.

### Sign in

![Sign in](design-system/sign-in.png)

### Sign up: organization

![Sign up, organization step](design-system/sign-up.png)

### Overview

![Overview](design-system/overview.png)

### Activity

![Activity](design-system/activity.png)

### Repositories

![Repositories](design-system/repositories.png)

### Repository

![Repository](design-system/repository.png)

### Branch

![Branch, light](design-system/branch.png)

![Branch, dark](design-system/branch-dark.png)

### Issues

![Issues](design-system/issues.png)

### Issue

![Issue](design-system/issue.png)

### Agents

![Agents](design-system/agents.png)

### Agents · Activity

![Agents, Activity tab](design-system/agents-activity.png)

### Agents · Quotas

![Agents, Quotas tab](design-system/agents-quotas.png)

### Agent

![Agent](design-system/agent.png)

### Agent · Task

![Agent, one task](design-system/agent-task.png)

### Agent · Empty

![Agent with no tasks](design-system/agent-empty.png)

### Repository · Artifacts

![Repository, Artifacts tab](design-system/artifacts.png)

### Artifact

![Artifact](design-system/artifact.png)

### Settings · Members

![Settings, Members tab](design-system/settings-members.png)

### Settings · Agents

![Settings, Agents tab](design-system/settings-agents.png)

### Settings · Models

![Settings, Models tab](design-system/settings-models.png)

![Settings, Models tab, adding a model](design-system/settings-models-add.png)

![Settings, Models tab, before any provider](design-system/settings-models-empty.png)

## Design to code

The Figma file is the source of truth, and the Console follows it, never leads.

1. **A visual change starts in Figma.** A colour, size, radius, text style, shadow, component or screen changes in the
   file first, and the file is where it is reviewed. A value that isn't a token is a Figma change, never a CSS one.
2. **Tokens flow one way, by export.** Every variable carries its CSS name in Figma's `codeSyntax`.
   [`figma-export.js`](https://github.com/arikkfir-org/cobranch/blob/main/web/console/tools/figma-export.js) exports the variables and the text and effect styles
   to `tokens.json`, and `go generate` writes
   [`tokens.css`](https://github.com/arikkfir-org/cobranch/blob/main/web/console/public/assets/tokens.css) from it;
   nobody edits `tokens.css` by hand, and CI fails when it differs from the export. A pull request that changes it names
   the Figma change it carries. The steps are in the tools'
   [README](https://github.com/arikkfir-org/cobranch/blob/main/web/console/tools/README.md). The Back-office's image
   takes the Console's `tokens.css`, typefaces and marks when it is built, so one export reaches both.
3. **Components map one to one.** Each Figma component is one class of the Console's component stylesheet, named after
   it. A page composes those classes and writes no component CSS of its own, so a component fix reaches every page.
4. **Screens are the acceptance test.** A page group is done when the Console matches its Figma screen at 1440 wide,
   light and dark, shown side by side in the pull request.
5. **Code never leads.** When the code needs what the file lacks (a state, an empty or error screen), the file gets it
   first. A forced divergence is said in the pull request and corrected in the file in the same unit of work.

**From a Figma variable to a CSS custom property to the Console, one way**

![From a Figma variable to a CSS custom property to the Console, one way](design-system/code.svg)

Semantic colours are `--color-*` custom properties (`bg/canvas` is `var(--color-bg-canvas)`), and spacing, sizes, radii
and primitives keep their own prefixes (`var(--space-16)`, `var(--radius-lg)`, `var(--gray-900)`). Text styles are
`font` shorthands with their tracking beside them (`font: var(--text-body-medium)`,
`letter-spacing: var(--text-body-medium-tracking)`), and effect styles `box-shadow` values (`var(--shadow-focus-ring)`).
Light applies by default; dark applies with `data-theme="dark"` on the root element, or with the system's dark
preference unless `data-theme="light"`.

```css
.card {
  background: var(--color-bg-surface);
  border: 1px solid var(--color-border-default);
  border-radius: var(--radius-lg);
  padding: var(--space-16);
}
```

## Decisions

| Question | Decision |
| --- | --- |
| Is violet right for AI, or should agents share the interface's ink? | Violet, for agents and AI work only (2026-10-27) |
| Should a branch with changes requested be Blocked, or a softer state of its own? | Blocked (2026-10-27) |
| Is Inter the face, or does Cobranch need a typeface of its own for the logo and headings? | Inter for the interface, JetBrains Mono for code, names and ids (2026-11-04) |
| Evolve the file, or start again? | Evolve it (2026-11-04) |
| What colour is the primary action? | Blue; ink stays for text and the mark (2026-11-04) |
| Light, dark, or both? | Both, equal: the system decides, and a person may override it (2026-11-04) |
| What is the shell? | A sidebar and a top bar, both fixed (2026-11-04) |
| How dense? | Comfortable: 36 px rows (2026-11-04), table rows too (2026-11-05), each above its divider |
| What is the mark? | A person and an agent on one trunk, in an ink square, from 24 px; the branch graph at 16 px and as the favicon (2026-11-04) |
| Where does the system live in code? | `tokens.css` and a component stylesheet in the Console; Figma stays the source (2026-11-04) |
| How are settings laid out? | Tabs, scoped to the organization (2026-11-04) |
| Which radii? | 4, 6, 8 and 12 (2026-11-04) |
| How is a status shown in a list? | A tinted pill by default, a quiet dot and word in lists (2026-11-04) |
| Is a table always in a card? | Only when the card holds more than the table (2026-11-04) |
| Where do labels go? | Above their fields (2026-11-04) |
| Where does a conversation live? | One pane on the right, whose subject follows the page (2026-11-04) |
| Tables or rich rows for lists? | Proposed: tables for organization-level lists, rich rows inside a page |
| How does an agent's spend show? | On each task row of the agent's page, against its cap, and the month so far on its facts (2026-11-05) |
| How are models capped? | They aren't: the cap per task is each agent's, and models carry prices only (2026-11-05) |
| Where do artifacts appear? | Proposed: a tab on their repository, and posted into the conversation that asked for them |
| Who may see an artifact? | Proposed: grants to the repository's members, the organization, people and agents, each view or edit |
