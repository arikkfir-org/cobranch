# Cobranch artifacts

> **Snapshot.** This design lives in its [artifacts page](https://claude.ai/artifact/GadW2fC9NMBLA86DqcHCBk), linked
> with Cobranch's other designs; this file is generated from it, with its diagrams, as of 2026-11-05, and synced again
> once Cobranch's shape settles.

**Status**: Agreed 2026-11-05 · [ENG-259](https://linear.app/arikkfir/issue/ENG-259) · Extends [principals and
permissions](principals-and-permissions.md#scopes-and-grants) with artifact and person scopes · Agents write them
through [their tasks](agents.md) · Built by [ENG-259](https://linear.app/arikkfir/issue/ENG-259), in four pull requests

An artifact is a document, a diagram, a report or a page that an agent writes when someone asks for one. It belongs to
the person who asked and is attached to the repository it was asked about, if any; every edit keeps the versions before
it, and grants decide who lists, views or edits it: its owner, the agent, people and agents it is shared with, those
whose grants reach its repository or organization, and the repository’s or the organization’s members as a whole once
shared. It shows in the organization’s Artifacts page, its repository’s Artifacts tab and the conversation it was
generated in.

## Scope

What an agent produces today is a commit or a chat message, so a document, a diagram or a report it writes on request
has nowhere to live, no history and no access of its own ([ENG-259](https://linear.app/arikkfir/issue/ENG-259)). This
design adds artifacts: written by agents when asked, each belonging to the person who asked and attached to the
repository it was asked about, kept in versions, and reached through grants on the artifact, its owner, its repository
and its organization, as everything else in Cobranch is ([principals](principals-and-permissions.md#scopes-and-grants)).
It follows the owner's answers of 2026-11-05 to the design system's questions 21, where artifacts show, and 22, who
reaches them.

**An agent writes an artifact for the person who asked, and posts it where it was asked for**

![An agent writes an artifact for the person who asked, and posts it where it was asked for](artifacts/overview.svg)

The Console shows artifacts in three places (answer 21): the organization’s Artifacts page, a repository’s Artifacts tab
for those attached to it, and the conversation each was generated in; each opens a page of its own.

| Kind | What it is for | Written as |
| --- | --- | --- |
| Document | Release notes, a plan, a guide | Markdown, or a page's HTML |
| Diagram | A sequence, a flow, an architecture | SVG |
| Report | Findings, a summary of activity | Markdown |
| Page | A self-contained page to read | HTML, run in a sandbox |

- **The kind says what it is for, the format how it is written.** A version names its format, `text/markdown`,
  `image/svg+xml` or `text/html`; any kind takes any of them, and the kind only picks the icon and the filter.
- **Text only, up to 1 MiB a version.** Bodies are UTF-8 text without NUL characters; an image an artifact needs is
  drawn in SVG or embedded as a `data:` URL.
- **People write them too.** The API that the agent's tool calls is the one a person or a script calls, with the same
  checks: an agent is just who usually writes the first version.

| Where | Lists | Answer |
| --- | --- | --- |
| The organization's Artifacts page, a sidebar item | Every artifact of the organization the viewer may list or read: those attached to its repositories, the viewer's own and those shared with them; filters Kind, Repository and Mine | 21.1 |
| A repository's Artifacts tab | Those attached to the repository that the viewer may list or read | 21.2 |
| The conversation pane | Those generated in the conversation, each a card where it was posted | 21.3 |

## Generating one

**Generating one: the task creates it, the harness posts the card**

![Generating one: the task creates it, the harness posts the card](artifacts/generate.svg)

The card is a message of the thread whose artifact field names the artifact: anyone who reads the thread sees the card,
and opens the artifact only if the grants let them. An `update_artifact` call adds a version the same way.

- **Asked in a conversation, or by a task.** A person asks an agent in any conversation ([Ask](agents.md)), or assigns
  it a task that asks for an artifact. The task's harness offers the model two tools, `create_artifact` and
  `update_artifact`, beside its chat and workspace tools.
- **Attached to the conversation's repository.** An artifact asked for in a branch's chat, or in a conversation about a
  repository, is attached to that repository; one asked for in an issue's chat or a conversation about the organization
  is attached to none (21.3.1, 22.4). A person creating one through the API attaches it to a repository, or to none.
- **Created through the API, as the task's token,** so the task's assigner caps what the agent may do, as for every call
  a task makes. Attaching it to a repository takes `artifacts.create` there, for both; an artifact attached to none
  takes only `org.read`, since it reaches nobody but its owner.
- **Exactly once.** The tool's call key is the artifact's ID, and a version's: a pod that restarts after the call and
  runs it again gets the same artifact back, created by the same principal, and creates nothing more.
- **Whose it is.** It belongs to the person who asked: the task's assigner, or whoever created it through the API. Its
  owner holds Artifact owner on it, and the agent Artifact editor, so it can revise it in a later task.
- **Private until shared.** Every artifact starts with both audiences at none, wherever it was asked for (21.3.2, 22.3):
  its owner reads it, and so do those whose grants reach its repository or organization, its Maintainers and Owners
  among them. The owner shares it from its page.
- **The card.** The harness posts a message into the task's thread that names the artifact; the pane draws it as a card
  with the title, the kind and who wrote it, which opens the artifact's page. The card is a message like any other: it
  shows to whoever reads the thread, and opens only for those the grants let in, so in a shared chat it reads "Shared
  with you only" to its owner until they share it.
- **Provenance.** An artifact keeps the agent, the task and the branch it came from, and each version the principal and
  the task that wrote it, so its page says "Generated by Builder from task #16 on feature/push-worker".

## Access

**What a principal may do with an artifact: grants at four scopes, the audience, then its credential**

![What a principal may do with an artifact: grants at four scopes, the audience, then its credential](artifacts/access.svg)

Nothing reaches an artifact by default but its owner, the agent that wrote it, and, for an attached one, the
repository’s and organization’s managers, whose roles give every artifact permission. Without `artifacts.read` an
artifact answers 404, as if it weren’t there; with `artifacts.list` alone it shows in lists.

| Permission | Granted on | Allows |
| --- | --- | --- |
| `artifacts.create` | A repository, or the organization for all of its repositories | Attach new artifacts to the repository |
| `artifacts.list` | A person, a repository or the organization | See the artifacts it reaches in lists, without opening them |
| `artifacts.read` | An artifact, a person, a repository or the organization | View an artifact, its versions and who reaches it |
| `artifacts.write` | An artifact, a repository or the organization | Add a version; change its title and summary |
| `artifacts.manage` | An artifact, a repository or the organization | Change its audience; share and unshare it; delete it |

| Role | ID | Permissions | Granted |
| --- | --- | --- | --- |
| Artifact lister | 309 | `artifacts.list` | On a person, a repository or the organization |
| Artifact viewer | 306 | `artifacts.read` | On one artifact, a person, a repository or the organization |
| Artifact editor | 307 | `artifacts.read`, `artifacts.write` | On one artifact, a repository or the organization |
| Artifact owner | 308 | `artifacts.read`, `artifacts.write`, `artifacts.manage` | The same; its owner's, on each new artifact |
| Owner (organization), Maintainer | 201, 303 | gain all five | Their scope, as today |
| Developer | 302 | gains `artifacts.create` | Its scope, as today |

- **Four scopes reach an artifact** (22.1, 22.2). A grant on the artifact reaches it alone. A grant on a person reaches
  every artifact they own, and everyone holds `artifacts.list` and `artifacts.read` on themselves. A grant on a
  repository reaches the artifacts attached to it, and one at the organization those attached to any of its
  repositories; an artifact attached to none is reached only through itself, its owner and its organization audience.
  Each gives only artifact permissions, so the repository's Maintainers and the organization's Owners manage every
  attached artifact.
- **Lists show what may be listed or read.** A list holds the artifacts the viewer may list, through a person, a
  repository or the organization, or read; one they may list but not read shows its title, kind and owner, and opening
  it answers 404.
- **The audience is the repository and the organization as a whole.** Each artifact has a repository audience and an
  organization audience, none, view or edit: view gives `artifacts.read` to every principal holding `repo.read` on its
  repository, or `org.read` at its organization; edit adds `artifacts.write`. An artifact attached to no repository has
  an organization audience alone. They are the Access card's "cobranch · members" and "Kfirs · everyone" rows, drawn as
  grants, and kept as two columns because no principal stands for a repository's readers.
- **People and agents, one at a time.** Share grants Artifact viewer, editor or owner on the artifact to one of the
  organization's members or agents, replacing any artifact role they held there; it takes `artifacts.manage` on the
  artifact and every permission of the role, since nobody grants more than they hold. A member who leaves the
  organization loses every grant on its artifacts, as on its secrets.
- **All of one's artifacts at once.** A person grants Artifact lister or viewer on themselves to another person or an
  agent, from their account: the grantee then lists, or reads, every artifact they own, in each organization both belong
  to, since every check starts from `org.read`. Nobody else grants on a person.
- **Narrowed by the credential.** A personal token uses only the artifact permissions it lists; a task token only those
  the task's assigner holds on the artifact too; a job's run token reaches no artifact.
- **Invisible without `artifacts.read`.** An artifact the viewer can't read answers 404, as one that isn't there does,
  and a list leaves it out unless the viewer may list it.
- **The list's badge** names the widest audience: Organization, Repository, or Only you when neither is set, whatever
  people it is shared with one by one.

## Versions

**Every edit adds a version; none is ever rewritten**

![Every edit adds a version; none is ever rewritten](artifacts/versions.svg)

An edit that names a version the artifact has moved past is refused with 412, so two editors never overwrite each other.
Renaming or summarizing an artifact changes its row, not its versions.

- **Append only.** A version is never changed or deleted while its artifact lives: an edit adds the next one, numbered
  from 1, and the artifact shows its newest.
- **One writer at a time.** Adding a version names the artifact's ETag in `If-Match`; a version added meanwhile makes it
  412, and the Console's editor shows the newer version to merge into.
- **Each keeps who and why:** its author, the task it came from if any, its format and a note of up to 500 characters
  ("Added the upgrading section").
- **Changing the row, not the body.** The title (up to 200 characters), the summary (up to 500) and the audience change
  the artifact's row and its ETag, never a version.

## Showing a body

**A body is untrusted: it is shown as text, an image or a sandboxed frame, never as the Console’s own markup**

![A body is untrusted: it is shown as text, an image or a sandboxed frame, never as the Console’s own markup](artifacts/rendering.svg)

An artifact never runs in the Console’s origin: its page’s own script runs nothing it read from a body, and the raw
endpoint, on the API’s origin, runs nothing either, so opening it in a tab can’t act for its visitor.

- **Untrusted, always.** An agent writes what its model and its inputs tell it to, so a body may carry anything a page
  can: scripts, forms, links that look like the Console's. None of it ever runs in the Console's origin, which holds the
  session.
- **Markdown** is rendered on the server by the renderer that renders a README ([code browsing](code-browsing.md)),
  which drops raw HTML and keeps only safe links, and the Console shows the HTML it answers inline, as it shows a
  README.
- **SVG** is shown as an `<img>` of the raw endpoint: an image runs no script and loads nothing.
- **HTML** is shown in an `<iframe sandbox>` of the raw endpoint with no allowances: an opaque origin, no scripts, no
  forms, no popups, no navigation of the Console. The raw endpoint itself answers with
  `Content-Security-Policy: sandbox; default-src 'none'; img-src data:; style-src 'unsafe-inline'`, so a page opened in
  a tab of its own, on the API's origin, runs nothing and loads nothing either.
- **Download** asks the raw endpoint for `Content-Disposition: attachment`, named after the title and the format.

## Data model

**Two tables, two scopes and four roles**

![Two tables, two scopes and four roles](artifacts/tables.svg)

Both tables carry the audit trigger, `artifact_versions`’ without its body; deleting a repository deletes its artifacts,
their versions and the grants on them.

| Table | Change | Why |
| --- | --- | --- |
| `artifacts` | New: `id`, `org_id`, `repo_id` (cascade; none when attached to no repository), `owner_id` (set null), `title`, `summary`, `kind`, `repository_access`, `organization_access`, `agent_id`, `task_id` (set null), `branch`, `head`, `version`, `created_by`, `created_at`, `updated_by`, `updated_at`; triggers `bump_version`, `audit()` and one that deletes the grants on it | The artifact, whose it is, where it is attached and its audience; `head` is its newest version's number |
| `artifact_versions` | New: `artifact_id` and `number` (key), `id` (unique, the call's key), `media_type`, `body` (text, up to 1 MiB), `author_id`, `task_id`, `note`, `created_at`; trigger `audit('body')` | Every version, never rewritten |
| `grants` | Scopes `artifact` and `user` (constraint `grants_scope_check` keeps `secret`) | Grants on one artifact, and on one person's artifacts |
| `roles` | 306 Artifact viewer, 307 Artifact editor, 308 Artifact owner, 309 Artifact lister; 201 and 303 append the five artifact permissions, 302 `artifacts.create` | The built-in roles, appended to, never set whole |
| `chat_messages` | `artifact_id`, set null when the artifact goes (migration 00053) | The card in the conversation |

- **Bodies in PostgreSQL, not the object store.** A version is a document of at most 1 MiB, written once with its row:
  keeping it in the row makes the artifact, its version and its grants one transaction, deleted with the repository by a
  cascade, and backed up with everything else. The object store holds Git's packs and job logs, which are large, binary
  or many; an artifact is none of these, and moving bodies there later changes only the store.
- **Every change is audited,** a version's body left out of the audit log, and recorded as an event of the organization:
  `artifact.created`, `artifact.changed`, `artifact.version_added`, `artifact.shared`, `artifact.unshared`,
  `artifact.deleted`.

## API

| REST | Does | Takes |
| --- | --- | --- |
| `GET /orgs/{org}/artifacts` | The organization's artifacts the viewer may list or read, newest change first; `kind`, `repositoryId` and `mine` filter | `org.read`; `artifacts.list` or `artifacts.read` on each |
| `POST /orgs/{org}/artifacts` | Creates one attached to no repository, with its first version: `id`, `title`, `summary`, `kind`, `organizationAccess`, `mediaType`, `body`, `note` | `org.read` |
| `GET /orgs/{org}/repos/{repo}/artifacts` | The artifacts attached to the repository that the viewer may list or read, newest change first; `kind` filters | `repo.read`; `artifacts.list` or `artifacts.read` on each |
| `POST /orgs/{org}/repos/{repo}/artifacts` | Creates one attached to the repository: the same fields, `repositoryAccess` and `branch` too | `artifacts.create` on the repository |
| `GET /orgs/{org}/artifacts/{id}` | The artifact, with the viewer's permissions on it | `artifacts.read` |
| `PATCH /orgs/{org}/artifacts/{id}` | Its title and summary, and its audience | `artifacts.write`; `artifacts.manage` for the audience; `If-Match` |
| `DELETE /orgs/{org}/artifacts/{id}` | Deletes it, its versions and the grants on it | `artifacts.manage`; `If-Match` |
| `GET`, `POST /orgs/{org}/artifacts/{id}/versions` | Its versions, newest first; adds one | `artifacts.read`; `artifacts.write` and `If-Match` |
| `GET /orgs/{org}/artifacts/{id}/versions/{n}` | A version with its body, and its HTML for Markdown | `artifacts.read` |
| `GET /orgs/{org}/artifacts/{id}/versions/{n}/raw` | Its bytes, sandboxed; `download=true` for an attachment | `artifacts.read` |
| `GET`, `POST /orgs/{org}/artifacts/{id}/access`, `DELETE …/access/{principal}` | Who holds a role on it, and the audience; shares and unshares | `artifacts.read`; `artifacts.manage` and the role's permissions |
| `GET`, `POST /me/artifact-access`, `DELETE …/{principal}` | Who lists or reads all of the viewer's artifacts; grants Artifact lister or viewer on oneself, and takes it back | Signed in as a user |

- **One artifact, one address.** An artifact is addressed under its organization by its ID, as a secret is, so someone
  it is shared with who can't read its repository still opens it, and one attached to no repository has an address too.
- **A listed item says what the viewer may do:** its permissions, `artifacts.list` alone when it may be listed but not
  opened.
- **GraphQL** answers the same: `Organization.artifacts`, `Repository.artifacts`, `Organization.artifact`, `Artifact`
  with its owner, versions and access, `User.artifactAccess` on `viewer`, and mutations `createArtifact` (with or
  without a repository), `updateArtifact`, `addArtifactVersion`, `deleteArtifact`, `shareArtifact`, `unshareArtifact`,
  `shareMyArtifacts` and `unshareMyArtifacts`.
- **Refusals:** 404 `artifact.not_found`; 409 `artifact.exists` for an ID another principal or repository took; 412
  `precondition.failed`; 400 for a title, a body, a kind, an audience or a format out of bounds, its code naming which
  (`artifact.title_invalid`, `artifact.body_invalid`, …).

## The Console

**The Console: an organization page, a tab on the repository, a page per artifact, a card in the pane**

![The Console: an organization page, a tab on the repository, a page per artifact, a card in the pane](artifacts/console.svg)

The organization’s page, the tab and the artifact’s page are files of their own under the Console’s organization and
repository pages, on the new shell; a list’s Access column shows the widest audience as a quiet badge: Only you,
Repository or Organization.

- **Three places** (21). The organization's Artifacts page is a sidebar item after Agents, listing the organization's
  artifacts with filters Kind, Repository and Mine; the repository's Artifacts tab lists those attached to it; the
  conversation pane shows each as a card where it was generated. The organization's page takes the slug `artifacts`,
  which no repository may take from then on: the migration that ships the page renames any repository that has it.
- **The page** shows the body as above, About (owner, repository, kind, generated by, from task, created), Access with a
  Shared switch and Manage for those holding `artifacts.manage`, and Versions, each opening that version; Share opens
  the access dialog, Download saves the newest version, and Edit opens the body in an editor that adds a version.
- **Account** gains who lists or reads all of one's artifacts, with Add and Remove.
- **Ask an agent for one** starts an Ask in the repository's conversation, prefilled with "Write a … about …".
- **On the new shell.** The page, the tab and the artifact's page are files of their own under the Console's
  organization and repository pages, built after the app shell ([ENG-255](https://linear.app/arikkfir/issue/ENG-255))
  from the Figma screens Artifacts, Repository · Artifacts and Artifact.

## Decisions

| Decision | Why | Rejected |
| --- | --- | --- |
| Each artifact belongs to the person who asked, and is attached to the conversation's repository, or to none in an issue's or the organization's conversation | The owner's answers 21.3.1 and 22.4: the repository is where it is found, the person whose it is | A repository owning every artifact: an issue's or the organization's conversation has none to give |
| Access is grants: roles on the artifact, its owner, its repository and its organization, plus two audience columns | The owner's answer 22: `artifacts.read` and `artifacts.list` on an artifact, a person, a repository or the organization; the audience covers "the repository's members" and "everyone", which no principal stands for | One scope per artifact: no sharing with one person |
| An owner column beside the Artifact owner grant | A grant on a person needs to know whose an artifact is; the grant still lets ownership be shared | Ownership by grant alone: "a person's artifacts" would change whenever one is shared as owner |
| `artifacts.list` apart from `artifacts.read` | The owner's answer 22.2: someone may know what exists without opening it | Lists of readable artifacts only |
| Starts private, wherever it was asked for | The owner's answer 21.3.2: nothing goes past the person who asked until they share it | Readable by a shared conversation's readers at once |
| Three places: the organization's page, the repository's tab and the conversation | The owner's answer 21 | The repository's tab alone, which an artifact attached to none never reaches |
| Bodies in PostgreSQL, up to 1 MiB | One transaction with the row and its grants; small text | The object store: a second write to keep in step, for no gain at this size |
| Untrusted bodies run nowhere: Markdown sanitized, SVG as an image, HTML in a sandbox with no allowances | The Console holds the session | Pages with scripts: a sandbox with `allow-scripts` still phishes and calls out |

## Failure modes and security

- **A prompt-injected agent** could write a body that phishes, or share an artifact wide: the body runs nothing, its
  creator can set only the audience the task's assigner may give, and every share is an event in the organization's
  audit trail.
- **A leaked artifact link** opens nothing without a grant; the raw endpoint takes the same session or token as the rest
  of the API.
- **A restarted pod** creates nothing twice: the call's key is the artifact's and the version's ID.
- **A full database:** each version is capped at 1 MiB, and only principals holding `artifacts.create` add artifacts.
  Whether artifacts count against an organization's quota is an open question.

## Rollout

**Four pull requests, each deployable on its own**

![Four pull requests, each deployable on its own](artifacts/rollout.svg)

The third waits for the conversation subjects’ backend ([ENG-234](https://linear.app/arikkfir/issue/ENG-234)), since its
card is a chat message; the fourth waits for the app shell ([ENG-255](https://linear.app/arikkfir/issue/ENG-255)).

- **Migration 00049** adds the tables, the scope and the roles, and appends the permissions to the built-in roles;
  rolling it back deletes every artifact and the grants on them. **00055** adds the owner, lets an artifact be attached
  to no repository, and adds the scope `user`, `artifacts.list` and Artifact lister. The Console's pages come with a
  migration that renames any repository named `artifacts`. **00053** adds the card's column, after the conversation
  subjects' 00048.
- **Nothing to configure.** No value of the chart changes: artifacts live in the database the installation already runs.

## Open questions

- **Quotas.** Should an organization's artifacts count against a limit of their own, as its Git data does
  ([quotas](quotas.md))?
- **Pages that run scripts.** A chart or an interactive report would need a sandbox with `allow-scripts`; this design
  runs none until a separate origin for artifacts exists.
- **Attaching later.** Should an owner move an artifact to a repository, or off one, once it exists? This design sets it
  once, when it is created.

Cobranch is a Kfirs project, created by Arik Kfir.
