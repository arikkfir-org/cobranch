# Cobranch issues

> **Snapshot.** This design lives in its [issues page](https://claude.ai/artifact/QM3VmRAtnSvttwT9bNaN8n), linked with
> Cobranch's other designs; this file is generated from it, with its diagrams, as of 2026-11-02, and synced again once
> Cobranch's shape settles.

**Status**: Proposed 2026-11-02, questions taken · [ENG-217](https://linear.app/arikkfir/issue/ENG-217) · Builds the
[product’s issues](product.md#issues-and-integrations) on [branch status](branch-status.md) · Adds no workload and no
chart value · Built by [ENG-217](https://linear.app/arikkfir/issue/ENG-217), in four pull requests

An organization tracks its work in Linear-like issues, `ACME-42`, with a state, a priority, an assignee and a Markdown
description. A branch whose name holds an issue’s identifier links to it, as can anyone by hand; a link starts the issue
and a merge finishes it, and assigning an issue to an agent hands it over as the agent’s task.

## Scope

How an organization tracks its work ([ENG-217](https://linear.app/arikkfir/issue/ENG-217)): Linear-like issues that its
members create, assign and move through their states, linked to the branches that work on them, by a branch's name or by
hand, and handed to an agent as its task. The [product design](product.md#issues-and-integrations) fixed that issues
belong to the organization and link to branches by name or by hand, and the [design system's Issue
screen](design-system.md#screens) drew the page; this one builds them on [organizations](sign-in-and-organizations.md),
[Git hosting](git-hosting.md#a-push) and [branch status](branch-status.md#branch-status), and agrees its seams with
agents ([ENG-215](https://linear.app/arikkfir/issue/ENG-215)).

**This slice gives organizations issues and links them to branches; agents take them through their tasks**

![This slice gives organizations issues and links them to branches; agents take them through their tasks](issues/scope.svg)

Nothing here needs a new workload: the API servers serve issues, and the push worker links branches as it processes
pushes.

Choices beyond the agreed designs are under [Decisions](#decisions), and the owner's questions under [Open
questions](#open-questions).

## Issues

An issue is a piece of the organization's work: what is wrong or wanted, who does it, and how far it got. It belongs to
the organization, not to a repository, so one issue can span repositories and the work on it can start before anyone
knows where it lands.

**An issue: the organization’s key and a number, what it says, and who and what it touches**

![An issue: the organization’s key and a number, what it says, and who and what it touches](issues/anatomy.svg)

A branch works on one issue at most; an issue has any number of branches, in any of the organization’s repositories.

- **Its identifier is the organization's key and a number,** such as `ACME-42`. The number counts the organization's
  issues from 1 and is never reused. The key is an organization setting of 1 to 10 uppercase letters and digits,
  starting with a letter; a new organization's comes from its slug's first word (`acme-corp` gives `ACME`), and
  `org.manage` changes it. A changed key renames every identifier at once; links already made stay, and the Console
  still opens an issue by its old identifier, since it reads only the number.
- **What it says.** A title of 1 to 200 characters, a description in Markdown of at most 65,536 characters, rendered as
  the Console renders a repository's Markdown ([code browsing](code-browsing.md)), a state, a priority (None, Urgent,
  High, Medium or Low), one assignee, any of the organization's [labels](#labels), and optionally the repository it is
  about.
- **One assignee, any principal of the organization** who acts: a member, or one of its agents once they exist.
  Assigning an agent hands it the issue as its task ([below](#agents)).
- **Its repository is a hint,** not an owner: the agent its task goes to works there, and the Console filters by it.
  Only viewers who read that repository see which one it is.
- **A version, its ETag.** Each change bumps an issue's version. Its ETag is the version and the organization's key,
  `"3-ACME"`, since a changed key renames the issue without changing it, and while an agent works on it, its task's
  number and state, `"3-ACME-12-running"`, since the task moves on without changing it. REST's `PATCH` and `DELETE` name
  the ETag they were made from in `If-Match`, by its version: 412 when another change came first, 428 without it, as for
  annotations ([branch status](branch-status.md#annotations)); a key changed meanwhile doesn't refuse them. GraphQL's
  `expectedVersion` does the same, optionally.
- **Its activity** is the [architecture](architecture.md#events-and-background-work)'s events with the issue as their
  subject: `issue.created`, `issue.changed` with each field's old and new value (a label's by name),
  `issue.branch_linked` and `issue.branch_unlinked`, each naming its actor, so the issue page shows who moved it and
  when; among them `issue.task_started`, read from the tasks its assignments gave agents, names the agent, the task and
  its branch, and only to the readers of the task's repository. A deleted issue's events go with it, and the organization's own event `issue.deleted` keeps its number and
  title. Its [comments](#comments) show beside them, a chat of its own; the table carries trigger `audit()`.
- **Deleting is for mistakes.** `issues.manage` deletes an issue, its links and its activity with it; work that won't be
  done is Canceled, which keeps its history.

## States

Six fixed states, in Linear's order; an organization doesn't define its own in this slice. People move an issue between
any two, and two transitions happen by themselves, from the branches linked to it.

**Six states; people move an issue between any two, and two moves come from its branches**

![Six states; people move an issue between any two, and two moves come from its branches](issues/states.svg)

A link moves only Backlog and Todo, and a merge never reopens a Done or Canceled issue: the automatic moves never fight
a person.

| State | Means | Badge |
| --- | --- | --- |
| Backlog | Known, not planned | `circle-dashed`, neutral |
| Todo | Planned, not started | `circle`, neutral |
| In progress | Someone works on it | `loader-circle`, info |
| In review | Its work waits for review | `eye`, warning |
| Done | Its work is merged | `circle-check`, success |
| Canceled | It won't be done | `circle-slash`, neutral |

- **A link starts it.** When a branch is linked to an issue in Backlog or Todo, by its name or by hand, the issue moves
  to In progress, for the principal who linked it, or pushed the branch.
- **A merge finishes it.** When a branch linked to an issue turns Merged ([branch
  status](branch-status.md#branch-status)), by a push into `main` or by asking Cobranch, the issue moves to Done, for
  the principal who merged it, unless it is Done or Canceled already. Its other branches stay linked.
- **Done and Canceled close it,** and the issue keeps when; reopening clears it. Lists show open issues by default.

## Labels

An organization's labels sort its issues across its repositories: one set, the organization's, as its issues are.

**An organization’s labels: a name, one of five colours and what it is for; an issue carries any of them**

![An organization’s labels: a name, one of five colours and what it is for; an issue carries any of them](issues/labels.svg)

The five colours are the design system’s status tones, so a label reads the same in both themes. A rename or a new
colour changes each issue that carries the label, so a client holding one reads it again; a new description doesn’t.

- **A label is a name, a colour and what it is for.** A name of 1 to 50 characters on one line, unique within the
  organization in any case, so `Bug` and `bug` can't both exist; one of five colours, gray (the default), blue, green,
  yellow or red, which the Console draws in the design system's neutral, info, success, warning and danger tones; and a
  description of at most 200 characters on one line.
- **An issue carries up to 20.** `issues.write` sets an issue's labels as one of its fields, from the version shown,
  like any other; the issue's activity records the change by the labels' names. Lists show each issue's labels, and
  filter by labels: an issue must carry every label named.
- **`issues.manage` manages them.** It creates, renames, recolours and deletes labels; `issues.read` sees them, and how
  many issues carry each. A rename or a new colour changes each issue that carries the label, bumping its version, so a
  client holding the issue's ETag reads the new name; a new description shows on no issue, so it changes none. Deleting
  a label takes it off its issues the same way.
- **A version, its ETag.** Each change bumps a label's version. Its ETag is the version and how many issues carry the
  label, `"3-12"`, since labelling an issue changes the count but not the label; `PATCH` and `DELETE` take it in
  `If-Match`, by its version, as for issues. The organization's events `label.created`, `label.changed` and
  `label.deleted` name who did it.

## Who may do what

Three permissions at the organization scope, added to the [catalog](principals-and-permissions.md#permissions), and to
the built-in roles by the migration that adds them.

**Three new permissions, at the organization; Members track work, Owners also delete**

![Three new permissions, at the organization; Members track work, Owners also delete](issues/permissions.svg)

The migration adds them to the built-in roles; custom roles take them when someone adds them.

| Permission | Allows |
| --- | --- |
| `issues.read` | See the organization's issues, their activity and links |
| `issues.write` | Create issues, change any field of them, their labels included, link and unlink branches |
| `issues.manage` | Delete issues; create, change and delete the organization's labels |

- **Members track work, Guests don't see it.** Owners hold all three, Members `issues.read` and `issues.write`, Guests
  none, since a Guest is there for the repositories granted to them. A custom role takes any of them.
- **Links respect repositories.** An issue lists only the branches of repositories its viewer reads, and a branch shows
  its issue only to a viewer who holds `issues.read`. Linking by hand takes `issues.write` and `repo.read` on the
  branch's repository.
- **An agent takes more.** Assigning an issue to an agent takes `agents.assign` too, and the task then does no more than
  its assigner can ([principals](principals-and-permissions.md#permissions)).

## Branches

A branch works on at most one issue; an issue has any number of branches. The link is a row of its own, so it outlives a
renamed key and survives the branch's deletion, as the [architecture](architecture.md#data-model) keeps a deleted
branch's row.

**A push links a branch by its name; a new issue links the branches that already name it**

![A push links a branch by its name; a new issue links the branches that already name it](issues/link.svg)

A hand link or unlink is a row the push worker never touches, so the person’s choice stands whatever the name says.

### By name

- **The name names the issue.** A branch whose name holds an issue's identifier as a word of its own, in any case, is
  linked to that issue: `acme-42-login`, `alice/ACME-42`, `fix/acme-42_retry`. A word is bounded by the name's ends or
  by anything but a letter or digit, so `acme-420` names issue 420 and `xacme-42` none. The first identifier in the name
  wins, and `main` is never linked.
- **The push worker links it,** while it processes the push that created the branch, or the first push since this slice
  to a branch with no link yet, so the push itself stays as fast as before. A name that names no issue yet is tried
  again when that issue is created: creating `ACME-42` links the organization's live branches that name it and have no
  link.
- **The Console offers the name.** An issue's page shows a branch name to copy, its identifier and its title in
  lowercase words, such as `acme-42-show-pushes-before-the-client-returns`, as Linear's "Copy git branch name" does.

### By hand

- **Linking by hand wins.** `issues.write` links a branch to another issue, or to an issue its name doesn't name, and
  unlinks it; the push worker never undoes either, so a branch unlinked by hand stays unlinked whatever its name says.
- **The Console's branch page** shows the branch's issue with its state, and a menu to link another issue by its
  identifier or to unlink.

## Comments

An issue's comments are a chat, as a branch's are ([agents](agents.md#branch-chats)): threads of Markdown messages that
people start, answer, edit, resolve and follow live, whose subject is the issue instead of a branch. One message model
serves both, so what a branch chat does, a comment does too.

**A comment is a message in the issue’s chat, which its followers read live**

![A comment is a message in the issue’s chat, which its followers read live](issues/comments.svg)

The same chat as a branch’s, with the issue as its subject and the issues’ permissions; GraphQL’s `issueChatUpdates` and
REST’s `…/chat/updates?wait=15` follow it alike.

- **Threads, not a flat list.** A comment starts a thread, and its answers stay under it, so a question and its answer
  read together and resolve together. An issue's threads point at no file: the issue may be about no repository, or
  several. The service and the schema both refuse an anchor on one.
- **The issues' permissions rule it,** at the organization, not the repository's: `issues.read` reads the comments,
  `issues.write` comments, answers, edits and deletes the viewer's own, and resolves and reopens their own threads, and
  `issues.manage` deletes anyone's and resolves anyone's. A viewer who can't see the organization finds no issue; a
  Guest, who can, is refused, as for the issue itself.
- **Live, as a branch chat is.** Each change appends its event, `chat.thread.started` to `chat.thread.reopened`, naming
  the issue as its subject, under a lock on the issue's chat, so followers read them in order; GraphQL's
  `issueChatUpdates` and REST's `…/chat/updates?wait=15` follow them. Each read checks the viewer's permissions again,
  and reads the issue again: a deleted issue ends its followers.
- **Messages render as the description does,** in the Markdown the Console renders a repository's in, with no repository
  of their own to link into.
- **They go with the issue.** Deleting an issue deletes its chat, its messages and their events with it; the audit log
  keeps what they said. A comment made while the issue is deleted either lands first, and goes with it, or finds no
  issue.
- **The issue page shows them** under its activity, newest thread first, with a box to comment for `issues.write`.

## Agents

The seams with agents ([ENG-215](https://linear.app/arikkfir/issue/ENG-215)), agreed with its thread on 2026-11-02:
tasks and chats are its own, and issues use both rather than a second model.

**Assigning an issue to an agent creates the agent’s task; its branch links back by name**

![Assigning an issue to an agent creates the agent’s task; its branch links back by name](issues/agent.svg)

Reassigning the issue cancels the agent’s open task. Mentioning the agent in the issue’s comments, its chat, hands it
work as in a branch chat, once agents follow mentions.

- **Assigning an agent creates its task,** in the same transaction as the assignment: the agents service checks it and
  the agents' store records it in the issue's transaction, so both happen or neither does. The viewer is the task's
  assigner, so the agent does no more than they can, and must read the issue's repository, where the task works. The
  task is of kind `issue`; its title is the issue's identifier and title, its instructions the description, cut to the
  20,000 characters a task holds with a line that says so. The task row names its issue (`agent_tasks.issue_id`), and
  the issue shows the task's number and state beside the agent, to viewers who read its repository.
- **Its task reads as the issue does.** The task carries the issue's title and description, so the agents service shows
  a task of kind `issue` only to viewers who hold `issues.read`, beside `repo.read` on the repository it names: a Guest
  who reads the repository finds neither the issue nor its task, and nobody without `issues.read` finds the task of an
  issue about no repository.
- **Reassigning cancels it.** Moving the issue to another assignee, or nobody, cancels the agent's open task in the same
  transaction, for whoever moved it; a task that has ended stays as it ended. Done or Canceled leaves a running task to
  finish its turn and record it. Deleting the issue cancels its task too, which then names no issue.
- **The agent works like anyone.** It pushes a branch named after the issue, which links by name and moves the issue to
  In progress, and the branch's merge moves it to Done.
- **Comments reach it as a chat does.** The issue's [comments](#comments) are a chat, so mentioning an agent there hands
  it work as a branch chat's mention does, once agents follow mentions.

## APIs

REST under `/v1/orgs/{org}`, GraphQL beside it, by the house's API rules: an ETag on each issue, `If-Match` on every
change, merge patches, and lists paged by an opaque cursor with `Link: rel="next"`.

| Operation | REST | GraphQL | Takes |
| --- | --- | --- | --- |
| List issues, filtered by state, assignee, repository, labels and text, newest first | `GET /issues` | `Organization.issues` | `issues.read` |
| Create an issue | `POST /issues`, 201 with `Location` | `createIssue` | `issues.write` |
| Read one, with its rendered description | `GET /issues/{number}`, 304 on its ETag | `Organization.issue(number)` | `issues.read` |
| Change fields | `PATCH /issues/{number}`, merge patch, `If-Match` | `updateIssue` | `issues.write`, and `agents.assign` for an agent |
| Its agent's task, number and state | the issue's `task` | `Issue.task` | `issues.read`, and `repo.read` on the task's repository |
| Delete | `DELETE /issues/{number}`, `If-Match` | `deleteIssue` | `issues.manage` |
| Its activity, oldest first, its tasks' starts among it | `GET /issues/{number}/activity` | `Issue.activity` | `issues.read` |
| Its branches | `GET /issues/{number}/branches` | `Issue.branches` | `issues.read`, and `repo.read` per branch |
| A branch's issue | the branch's `issue` | `Branch.issue` | `issues.read` |
| Link or unlink by hand | `PUT` or `DELETE /repos/{repo}/branches/{branch}/issue` | `linkBranch`, `unlinkBranch` | `issues.write`, `repo.read` |
| Its comments, newest thread first, with their messages | `GET /issues/{number}/chat` | `Issue.chat` | `issues.read` |
| Comment: start a thread, or answer one | `POST /issues/{number}/chat/threads`, `POST …/threads/{thread}/messages` | `startIssueChatThread`, `postIssueChatMessage` | `issues.write` |
| Edit or delete a message, resolve or reopen a thread | `PATCH` or `DELETE …/messages/{message}`, `PATCH …/threads/{thread}`, `If-Match` | `editIssueChatMessage`, `deleteIssueChatMessage`, `resolveIssueChatThread` | `issues.write` for the viewer's own, `issues.manage` for anyone's |
| Follow them live | `GET /issues/{number}/chat/updates?after=&wait=` | `issueChatUpdates` | `issues.read` |
| The key | the organization's `issueKey` | `Organization.issueKey`, `updateOrganization` | `org.manage` to change |
| List labels, by name, with their issues' count | `GET /labels` | `Organization.labels` | `issues.read` |
| Create a label | `POST /labels`, 201 with `Location`, 409 for a name taken | `createLabel` | `issues.manage` |
| Read one | `GET /labels/{id}`, 304 on its ETag | | `issues.read` |
| Change or delete one | `PATCH` (merge patch) or `DELETE /labels/{id}`, `If-Match` | `updateLabel`, `deleteLabel` | `issues.manage` |

## The Console

An organization gains an Issues page beside its repositories, as the [design system's Issue
screen](design-system.md#screens) draws it.

**The issue page, as the design system’s Issue screen draws it**

![The issue page, as the design system’s Issue screen draws it](issues/console.svg)

Each property saves as soon as it changes, naming the version it was made from; a change someone else made first reloads
the page instead of overwriting it.

- **The list** at `/<org>/issues` is a table with its header row, under the tabs Open, Closed and All with their
  counts, and filters by text, repository, assignee (Me and Nobody among them) and label; a filtered list counts what
  it shows of the tab's issues. Each row is the issue's state icon, identifier, title and labels, then its repository,
  assignee, branch and when it last changed.
- **The issue page** at `/<org>/issues/ACME-42` shows the identifier and state badge, the title and rendered
  description, its branches with their status as branch rows, its activity, newest first, and its [comments](#comments),
  live; the side column edits state, priority, assignee, labels and repository, each change saved at once with the
  version it was made from, and shows who created it. The assignee menu lists the organization's agents too, for a
  viewer who holds `agents.assign`, and an agent's task shows under it while it is queued, running or waiting, linked to
  the task.
- **Labels** at `/<org>/issues/labels` lists the organization's labels with what each is for and how many issues carry
  it, linked to the list filtered by it; for `issues.manage`, each row edits or deletes its label, and a dialog adds
  one, its name in lower case and its colour picked from swatches of its tag.
- **New issue** at `/<org>/issues/new` asks for a title, then the rest, as the side column does. `issues` joins the
  repository slugs the Console reserves.
- **A branch's page** shows its issue under its status, linked to the issue page, with Link and Unlink.

## Data model

**This slice’s tables: issues and their numbers, branch links, labels, and the key on organizations**

![This slice’s tables: issues and their numbers, branch links, labels, and the key on organizations](issues/er.svg)

`issues`, `branch_issues`, `labels` and `issue_labels` carry trigger `audit()`, as the chats’ tables do already, and
`issues` and `labels` trigger `bump_version()`. Every new row carries `org_id`, but `issue_labels`, which joins two that
do.

- **Numbers come from a counter per organization,** `issue_numbers`, incremented in the issue's transaction, so two
  issues created at once never share a number and a deleted one never frees its own.
- **`branch_issues` is keyed by the branch,** which keeps "a branch works on one issue" in the schema; a row with no
  issue is a branch unlinked by hand, which the push worker leaves alone.
- **`labels` is unique by name in any case,** an index on the organization and the lowercased name; `issue_labels` joins
  an issue to a label, and deleting either deletes the row.
- **`agent_tasks` takes `issue_id`,** which a deleted issue sets to null, and `task_kinds` the kind `issue`; the issue
  shows its assignee's newest task on it.
- **`chat_threads` takes `issue_id`** beside `branch_id`, exactly one of them set, and `repo_id` only with a branch; an
  issue's thread has no anchor. Its messages, their trigger `audit()` and the events stay as the branch chats have them.
- **Every new row carries `org_id`,** but `issue_labels`, which joins two rows that do; `issues`, `branch_issues`,
  `labels` and `issue_labels` carry trigger `audit()`, and `issues` and `labels` trigger `bump_version()`. The counter
  carries neither: it is bookkeeping.

## Decisions

| Decision | Why | Status |
| --- | --- | --- |
| Issues belong to the organization; a repository is an optional field | The product design's model; work often spans repositories or starts before anyone knows where | Proposed |
| An identifier is the organization's key and a number, `ACME-42`; the key comes from the slug and `org.manage` changes it | Linear's shape, which is what lets a branch's name link it; no teams in this slice, so one key per organization | Proposed |
| Six fixed states, Backlog to Canceled; no custom workflows yet | Opinionated, Linear's defaults; a workflow editor waits for someone who needs one | Proposed |
| A link moves Backlog or Todo to In progress; a linked branch's merge moves the issue to Done | Linear's default automation; branches are Cobranch's pull requests | Proposed |
| One assignee, any principal: a member or an agent | Everything is a principal; the task records the human who assigned the agent | Proposed |
| `issues.read`, `issues.write` and `issues.manage`; Guests hold none | Least privilege: a Guest is there for granted repositories, not the organization's plans | Proposed |
| A branch has at most one issue; the first identifier in its name wins; a hand link or unlink is never undone by the name | "A branch shows its issue", in the product design's words; the person decides over the pattern | Proposed |
| Names link when the push worker processes the branch's push, and when the issue they name is created | Keeps pushes as fast as before; a branch pushed before its issue still links | Proposed |
| Comments are a chat whose subject is the issue; tasks are agents' tasks naming the issue | One message model and one task model, agreed with [ENG-215](https://linear.app/arikkfir/issue/ENG-215) | Agreed |
| An issue's comments take the issues' permissions, with no anchor; a Guest is refused | Who reads an issue reads what is said about it; an issue may span repositories, so no file is its | Proposed |
| Labels are the organization's, in five colours of the design system's tones; `issues.manage` manages them and `issues.write` sets them, at most 20 an issue | One vocabulary across repositories, as issues are the organization's; the tones read in both themes; a shared vocabulary is an Owner's to shape and everyone's to use | Proposed |
| A label's rename or new colour changes each issue that carries it | An issue's ETag then changes, so a client that cached it reads the new name; a description shows on no issue, so its change touches none | Proposed |
| Sub-issues, relations, estimates, cycles and projects, custom states, notifications and search come later | Keeps this slice to issues, their branches, labels and agents | Proposed |

## Rollout

**Four pull requests, each with its tests and its Console pages**

![Four pull requests, each with its tests and its Console pages](issues/rollout.svg)

Pull requests 1 to 3 need nothing but `main`. The fourth comes in two: comments on the branch chats `main` has, and
agents once [ENG-215](https://linear.app/arikkfir/issue/ENG-215)’s tasks merge; the last says Closes
[ENG-217](https://linear.app/arikkfir/issue/ENG-217).

Nothing changes for an operator: no workload, no chart value. An organization's existing members get the new permissions
through the built-in roles; custom roles get them only when someone adds them. Demo data seeds Acme's issues in every
state, priority and repository, a few of them changed since, five labels on them, and one of Globex's with a label of
its own, so a preview shows the pages at once.

## Open questions

Taken on their recommendations on 2026-11-02, while the owner was away, so the work could go on; either can be
revisited.

1. **Should Guests read issues (`issues.read` in the Guest role)?** Taken: no. A Guest is there for the repositories
   granted to them, not the organization's plans; an organization that wants Guests to read issues gives them a custom
   role that holds `issues.read`.
2. **Is the automation right: a link moves an issue to In progress, a merge to Done?** Taken: yes, with no switch in
   this slice. It is Linear's default; a switch per organization can follow when someone needs one.

Cobranch is a Kfirs project, created by Arik Kfir.
