# Cobranch conversations

> **Snapshot.** This design lives in its [conversations page](https://claude.ai/artifact/2zef9Urapj7JwH7tXkUjBn), linked
> with Cobranch's other designs; this file is generated from it, with its diagrams, as of 2026-11-05, and synced again
> once Cobranch's shape settles.

**Status**: Proposed 2026-11-05 · [ENG-234](https://linear.app/arikkfir/issue/ENG-234) · Extends [branch
chats](agents.md#branch-chats) and [issues’ comments](issues.md) · Agents take part through [their tasks](agents.md) ·
Built by [ENG-234](https://linear.app/arikkfir/issue/ENG-234), in three pull requests

Every page of the Console has a conversation beside it, and its subject changes with the page. A branch’s and an issue’s
are the chats everyone who reads them shares; an organization’s, a repository’s and an agent’s are each person’s own,
which only they read. People ask the organization’s agents in any of them, and an agent’s conversation knows its tasks
and its queue.

## Scope

The Console's conversation pane sits beside every page (layout 14D of the [design system](design-system.md)), and what
it talks about changes with the page ([ENG-234](https://linear.app/arikkfir/issue/ENG-234)). Branches have had a chat
since [ENG-215](https://linear.app/arikkfir/issue/ENG-215) ([branch chats](agents.md#branch-chats)) and issues since
[ENG-217](https://linear.app/arikkfir/issue/ENG-217) ([comments](issues.md#comments)), each shared by everyone who reads
its subject. This design adds the rest: each person's own conversation about their organization, about each of its
repositories and with each of its agents, which only they read; agents asked into any of them; and an agent's
conversation that knows its tasks and its queue.

**The pane follows the page: a private conversation, or the chat everyone who reads the subject shares**

![The pane follows the page: a private conversation, or the chat everyone who reads the subject shares](conversations/subjects.svg)

Blue marks a private conversation, one per person per subject; the branch’s and the issue’s chats are the ones
[ENG-215](https://linear.app/arikkfir/issue/ENG-215) and [ENG-217](https://linear.app/arikkfir/issue/ENG-217) built.
Agents take part in all five when someone asks them.

| Page | The pane shows | Who reads it | Who writes in it |
| --- | --- | --- | --- |
| An organization's | The viewer's conversation about the organization | Its owner alone, with `org.read` | Its owner, and the agents they ask |
| A repository's | The viewer's conversation about the repository | Its owner alone, with `repo.read` | Its owner, and the agents they ask |
| A branch's, and its commits', files' and jobs' | The branch's chat | Everyone with `repo.read` | `messages.post`, and the agents anyone asks |
| An issue's | The issue's chat, its comments | Everyone with `issues.read` | `issues.write`, and the agents anyone asks |
| An agent's | The viewer's conversation with the agent | Its owner alone, with `org.read` | Its owner, and the agent |

- **One conversation per person per subject.** Max's conversation about the website repository is one chat, with as many
  threads as he starts; Nia's about the same repository is another, and neither sees the other's.
- **The same chat underneath.** A conversation is threads of Markdown messages, edited, deleted, resolved and followed
  live exactly as a branch's chat is; only who reaches it differs. Its threads point at no file: a repository's
  conversation is about the repository, not a commit of it, and a question about lines belongs in their branch's chat.
- **A commit, a file or a job belongs to a branch's chat** when the page shows it on that branch, so the conversation
  around a change stays in one place; the same commit opened by its SHA alone shows the repository's conversation.

## The data model

**A conversation is a chat thread’s third subject, made by its first thread**

![A conversation is a chat thread’s third subject, made by its first thread](conversations/model.svg)

The audit trigger keeps every row of `conversations`, naming the person whose first thread made it. Deleting the
organization, the owner, the repository or the agent deletes the conversation and its threads.

- **A third subject for a thread.** `chat_threads` gains `conversation_id`, and its check becomes "exactly one of a
  branch, an issue or a conversation". Messages, versions, edits, deletions and resolutions are unchanged, so every path
  the branch's chat takes, the conversation takes too.
- **An ID known in advance.** A conversation's ID is a UUIDv5 of its owner, its organization, its repository and its
  agent, under a namespace of Cobranch's own. A follower subscribes to it, and a page reads it, before anyone wrote
  there; the row appears with the first thread.
- **Made by its first thread.** The transaction that starts the first thread inserts the conversation if it is missing,
  under the chat's advisory lock on the ID, then holds it against deletion until it commits, as a change to an issue's
  chat holds its issue.
- **Events name the conversation.** Each change appends an event whose subject is the conversation's ID and whose
  details name the conversation, the thread and the message, then `NOTIFY` wakes the servers whose clients follow it.
  Updates and their cursors work as they do for a branch.
- **Gone with its subject.** Deleting the organization, the owner, the repository or the agent cascades to the
  conversation and its threads; a request on it answers 404 from then on.

## Starting and following one

**The first thread makes the conversation, in the transaction that starts it**

![The first thread makes the conversation, in the transaction that starts it](conversations/first.svg)

A conversation nobody wrote in costs nothing: reading one, following one and listing its updates need no row. A
repository or an agent deleted between the read and the write answers 404, never a thread without a subject.

- **Reading needs no row.** The service checks the subject's permission, the store answers the subject with its ID, its
  repository's default branch and head for a repository's, its agent's name for an agent's, and an empty page while
  nobody has written.
- **Links resolve as the subject's do.** A message's relative links lead to the default branch's files in a repository's
  conversation, to the organization's pages in the organization's, and under the agent's page in an agent's.

## Privacy

**Only its owner reaches a conversation, while they read what it is about**

![Only its owner reaches a conversation, while they read what it is about](conversations/privacy.svg)

A personal token narrows the owner’s permissions as everywhere else. An administrator, an organization’s Owner included,
reads nobody’s conversation: privacy here is the product’s promise, not a role.

- **The owner check comes first.** Every operation compares the viewer with the conversation's owner before any rule,
  and answers 404 to anyone else, as for a conversation that doesn't exist, so nobody can learn whether another's
  conversation has anything in it.
- **Then the subject's permission,** checked at every read and every change: `org.read` for the organization's and an
  agent's, `repo.read` for a repository's. A person who loses it loses the conversation with it, and finds it again,
  untouched, if they regain it.
- **Nobody else, by role.** No permission reads another's conversation: an organization's Owners manage its members, not
  their conversations. What an agent sees of one is what its task's thread shows it, while it works for the
  conversation's owner.
- **Audited all the same.** The audit log keeps every conversation's row, and the events log every change to it, as for
  every chat.

## Agents in every chat

**Asking an agent in any chat starts a task that talks in that thread**

![Asking an agent in any chat starts a task that talks in that thread](conversations/ask.svg)

A message without `ask` asks nobody. In a conversation with an agent, a thread’s first message asks that agent unless it
says otherwise; asking again in a thread whose task ended starts the next task there.

- **Asking is explicit.** A message or a thread's first message names an agent in `ask`, which the composer sets when
  the person picks an agent after `@`; asking takes `agents.assign` in the organization beside what posting takes. A
  plain mention without `ask` asks nobody.
- **A task in the thread.** The message and the agent's task are one transaction: the task is assigned by the person,
  capped by their permissions, and talks in the message's thread, with the chat's repository and branch when it has
  them, a repository's default branch in a repository's conversation, and the issue in an issue's chat. The agent posts
  its replies there as itself, each linking its task, as in a branch's chat today.
- **Following up.** A task that asks and waits takes the next message in its thread as its answer; asking again in a
  thread whose task ended starts the next task there, which reads the thread from the start.
- **In an agent's conversation** a thread's first message asks that agent unless it names another, since there is nobody
  else to talk to there.
- **Private tasks.** A task asked in a private conversation is private too: its title, instructions and transcript show
  to its assigner alone, and to others only as a place in the agent's queue. Its token reads and posts only in its own
  thread of its assigner's conversation, and only while the assigner still owns and reads it.
- **What the agent may read.** A task asked in an issue's chat reads that chat only if its agent and its assigner both
  hold `issues.read`; one asked in a branch's chat, the branch's only with `repo.read`, as every task call is capped
  today.

## The agent's conversation

**The agent’s conversation knows its tasks and its queue**

![The agent’s conversation knows its tasks and its queue](conversations/agent.svg)

The tool lists only what the person may see: their own tasks, and the others they could see on the agent’s page; another
person’s private task shows as a place in the queue, nothing more.

- **A tasks tool.** A task asked in an agent's conversation gets a `tasks` tool beside its chat tools: the agent's tasks
  its assigner may see, newest first, each with its state, its spend and where it came from, and the agent's queue, with
  the assigner's queued tasks' places in it.
- **The same records as the agent's page,** read through the REST API with the task's token, so the agent never tells a
  person more than the person could read there.

## API

| REST | Does | Takes |
| --- | --- | --- |
| `GET /orgs/{org}/conversation` | A page of the viewer's conversation about the organization, and where its updates stand | `org.read` |
| `GET /orgs/{org}/repos/{repo}/conversation` | The same, about one of its repositories | `repo.read` |
| `GET /orgs/{org}/agents/{agent}/conversation` | The same, with one of its agents | `org.read` |
| `POST …/conversation/threads` | Starts a thread with its first message; an anchor answers 400 | the same |
| `GET`, `PATCH …/conversation/threads/{thread}` | A thread with its messages; resolves it or opens it again, with `If-Match` | the same |
| `POST …/conversation/threads/{thread}/messages` | Posts a message | the same |
| `GET`, `PATCH`, `DELETE …/messages/{message}` | A message; its author edits it, the owner deletes it, with `If-Match` | the same |
| `GET …/conversation/updates` | The changes after a cursor, waiting up to 15 seconds for one | the same, at every read |

- **The branch's and the issue's shapes.** Threads, messages, updates, ETags, cursors and refusals are the chats' own
  (`ChatThread`, `ChatMessage`, `ChatUpdate`), so the pane's code is the same for all five subjects and only its base
  path differs.
- **GraphQL** answers the same: `Organization.conversation`, `Repository.conversation` and `Agent.conversation` read a
  page; mutations `startConversationThread`, `postConversationMessage`, `editConversationMessage`,
  `deleteConversationMessage` and `resolveConversationThread`, and subscription `conversationUpdates`, name the
  conversation by a `ConversationSubject`: the organization, and a repository or an agent, never both.
- **Refusals:** 404 `organization.not_found`, `repository.not_found` or `agent.not_found` for a subject the viewer can't
  read or that isn't there; 404 `thread.not_found` for a thread of another's conversation; 400 `anchor.not_allowed`.
- **Asking** (second pull request) adds `ask`, an agent's ID, to `ChatThreadInput` and `ChatMessageInput` in every chat,
  and `task` to the message it created a task from.

## The Console

- **Each page names its subject.** A page module exports `subject(ctx)`, the app shell's contract: its key, title, kind,
  the chat's base path, what posting and moderating take, the composer's placeholder, and whether the subject is gone.
  The shell's pane reads, posts and follows whatever the page names, and switches when the page does.
- **Private or shared, said once.** The pane's header names the subject and marks a private conversation "Only you", a
  shared chat with who reads it.
- **The composer's @** lists the organization's agents the viewer may assign; picking one sets `ask`. A task's messages
  show its agent, its number and its state, which link to the task's page.
- **Built on the new shell** ([ENG-255](https://linear.app/arikkfir/issue/ENG-255)): the organization's and
  repositories' pages hand their conversations to the pane as they move onto it.

## Decisions

| Decision | Why | Rejected |
| --- | --- | --- |
| A private conversation per person per subject, for an organization, a repository and an agent | The owner's scopes: these pages are where a person thinks aloud and asks agents, not where the team talks | One shared chat per repository: a second, noisier branch chat that nobody owns |
| A conversation is a third subject of the chats' threads | Every message path, live update and permission check is shared, and the pane needs one model | A table of its own: a second chat to keep in step with the first |
| Its ID derives from its owner and subject, and its row comes with its first thread | Pages and followers address it before it exists; empty conversations cost no rows | A row for every person and subject up front: rows for conversations nobody has |
| The owner alone reads it; no role reaches another's | Privacy that an organization's Owner can open isn't privacy | Owners reading members' conversations: makes the pane a place people avoid |
| Its threads point at no file | A question about lines belongs in their branch's chat, where the team sees it | Anchors at the default branch: lines that move without a branch to follow them |
| Asking is an explicit field the composer sets | A mention in prose never starts paid work by accident; the API says what it does | Parsing `@Name` from the body: ambiguous names, and work started by a quote |
| A task asked privately is private | The conversation's privacy would leak through the task list and its transcript otherwise | Public tasks from private conversations: the agent's page would show what was asked |

## Failure modes and security

- **Someone guesses a conversation's ID:** it gives nothing; every operation checks that the viewer owns it before
  anything else, and a task token only reaches its own thread.
- **A person loses access to the subject:** the conversation answers 404 while they lack it, and its rows stay;
  regaining access restores it.
- **The subject is deleted** between a read and a write: holding the conversation in the write's transaction makes the
  write 404, never a thread without a subject.
- **A prompt-injected agent** posts only in its task's thread, as itself, capped by the person who asked it; it can't
  read another's conversation, since its token answers to the owner check like anyone's.
- **Many conversations:** one row per person per subject they wrote in, and threads as small as a branch's; nothing
  grows until someone writes.

## Rollout

**Three pull requests, each deployable on its own**

![Three pull requests, each deployable on its own](conversations/rollout.svg)

The second tells the agents’ slice ([ENG-257](https://linear.app/arikkfir/issue/ENG-257)) before it changes how tasks
are listed; artifacts’ cards ([ENG-259](https://linear.app/arikkfir/issue/ENG-259)) ride on the first. The third waits
for the app shell and its pane ([ENG-255](https://linear.app/arikkfir/issue/ENG-255)).

- **Migration 00048** adds `conversations` and the threads' column and constraints. Rolling it back deletes every
  conversation, its threads and their events; the audit log keeps what they said.
- **Nothing to configure.** No value of the chart changes.
- **The second pull request** changes how tasks are listed and who reads them, after telling the agents' slice; the
  third waits for the app shell.

## Open questions

- **Moving a thread.** Should a person be able to move a thread from their conversation into a branch's or an issue's
  chat once it concerns the team?
- **Retention.** Do private conversations keep forever, like the shared chats, or expire after a time a person or an
  installation sets?
- **Administrators and private tasks.** Should those who manage an organization's agents see what its private tasks
  spend, one by one, rather than in the agent's totals?

Cobranch is a Kfirs project, created by Arik Kfir.
