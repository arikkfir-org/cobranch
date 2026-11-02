# Cobranch agents and branch chats

> **Snapshot.** This design lives in its [agents and branch chats
> page](https://claude.ai/artifact/KWNwxfUQ15rK85CNzhYXr4), linked with Cobranch's other designs; this file is generated
> from it, with its diagrams, as of 2026-11-02, and synced again once Cobranch's shape settles.

**Status**: Proposed 2026-11-02 · [ENG-215](https://linear.app/arikkfir/issue/ENG-215) · Builds the [product’s
agents](product.md#agents-and-ai-review) on [jobs](https://claude.ai/artifact/FqmP4JjVLtfXc5q4uAZ68y)’ runtime · Adds
the agent image · Built by [ENG-215](https://linear.app/arikkfir/issue/ENG-215), in five pull requests

Every branch carries a real-time, threaded chat whose threads can point at lines of a file. Users create agents, which
their organization owns and an admin puts on a model from the providers the organization holds keys for; a user assigns
an agent a task, which then does no more than the user could; and the agent works it in a pod of its own, through
Cobranch’s APIs like any principal, posting in chats and pushing branches, while its harness saves the transcript step
by step.

## Scope

How people and agents talk about a branch, and how agents work ([ENG-215](https://linear.app/arikkfir/issue/ENG-215)):
every branch's real-time, threaded chat whose threads can point at lines of a file; agents, principals that users create
and the organization owns; the model providers an organization holds credentials for and the model an admin picks for
each agent; tasks that users assign to agents, capped by the assigner's own permissions; and the long-running pod whose
harness works a task through Cobranch's APIs, saving its transcript as it goes. The [product
design](product.md#agents-and-ai-review) fixed agents as principals with a model each; the
[architecture](architecture.md#settings-secrets-and-models) fixed the providers, the controller and the agent image;
[principals and permissions](principals-and-permissions.md#agents) fixed `agents.create`, `agents.assign`,
`messages.post` and `messages.moderate`, the assigner cap and the task token. Pods run on
[jobs](https://claude.ai/artifact/FqmP4JjVLtfXc5q4uAZ68y)' runtime: its controller, its job namespaces and its secrets.

**This slice builds chats, agents, tasks and the harness; it runs pods on jobs’ runtime**

![This slice builds chats, agents, tasks and the harness; it runs pods on jobs’ runtime](agents/scope.svg)

Chats come first and need nothing else; agents and tasks need jobs’ secrets; pods need its controller.

Choices beyond the agreed designs are under [Decisions](#decisions), and the owner's answers to three questions under
[Open questions](#open-questions). Branch chats, live, are built (cobranch's first pull request for
[ENG-215](https://linear.app/arikkfir/issue/ENG-215)); the rest follows in four more.

## Branch chats

A branch is the request to merge into `main`, so its chat is where people review it. A chat holds threads; a thread
holds messages, and may point at lines of a file at one commit, which is how a review comment points at code.

**A branch’s chat is threads of messages; a thread may point at lines of a file at one commit**

![A branch’s chat is threads of messages; a thread may point at lines of a file at one commit](agents/chat-model.svg)

A deleted branch keeps its row, so its chat stays readable. [ENG-216](https://linear.app/arikkfir/issue/ENG-216)’s
review comments are anchored threads, and [ENG-217](https://linear.app/arikkfir/issue/ENG-217)’s issues hold threads of
their own.

- **Every branch has one,** from its creation, kept with its `branches` row after the branch is deleted, so the
  conversation outlives it, read-only from then on ([ENG-241](https://linear.app/arikkfir/issue/ENG-241) shows deleted
  branches). A thread may sit on the branch as a whole, or point at a file and a range of lines at one of the
  repository's commits.
- **Messages are Markdown,** at most 20,000 characters, rendered as the Console renders READMEs, so a message's HTML is
  the same wherever it shows. A message can be edited by its author, and deleted by its author or a moderator; a deleted
  one leaves its place in the thread, saying who deleted it.
- **Threads resolve.** Its starter resolves or reopens a thread with `messages.post`; anyone else needs
  `messages.moderate`.
- **An anchor stays where it was made.** A thread that points at lines names the commit it read them at. When the branch
  moves on, the Console shows the thread on that commit, marked as on an older commit, rather than guessing where the
  lines went.
- **A message may carry its own ID,** a UUIDv7 its poster picks: posting it again answers the message already there, so
  an agent that resumes, or a client that lost an answer, never posts twice.

| Who | May |
| --- | --- |
| `repo.read` | Read the branch's chat, live |
| `messages.post` | Start threads, reply, edit and delete their own messages, resolve and reopen their own threads |
| `messages.moderate` | Delete anyone's messages, resolve and reopen anyone's threads |

### Live

**Agreed** ([architecture](architecture.md#events-and-background-work)): live updates are GraphQL subscriptions, a
reconnecting client replays from `events`, and `NOTIFY` only wakes listeners sooner.

**A message reaches every client that follows the chat a moment after its transaction commits**

![A message reaches every client that follows the chat a moment after its transaction commits](agents/chat-live.svg)

The REST server’s long poll reads the same events, woken by the same NOTIFY, so the harness, the SDKs and the Console,
whose pages call REST, follow a chat without WebSocket.

- **Each change appends an event** in its transaction, naming the branch as its subject: `chat.thread.started`,
  `chat.message.posted`, `chat.message.edited`, `chat.message.deleted`, `chat.thread.resolved`, `chat.thread.reopened`.
  The event's sequence number is the cursor a client keeps.
- **One listener per server.** Each GraphQL server holds one `LISTEN` connection; a notification names the branch, and
  wakes only that branch's subscribers, who read the events after their cursor and send them. A missed notification
  costs nothing: a subscriber also reads on every reconnect, and once a minute.
- **The REST server waits too.** `GET …/chat/updates?after=<cursor>&wait=15` answers at once when there are updates, or
  after the first notification within `wait` seconds, at most 15, under a server's 20-second shutdown, so the SDKs, the
  agent harness and the Console, whose pages call REST, follow a chat without WebSocket.
- **A WebSocket opens only from the Console's origin,** or from a client that sends no `Origin`, so another site's page
  can't follow a chat with its viewer's cookie, and closes after an hour, when its client connects again and its
  credential is checked again.
- **Grants are checked at every read,** so a revoked `repo.read` ends a subscription at its next update.
- **Changes to one chat are ordered.** Each takes a transaction lock on its branch's chat before it writes its event, so
  the events' sequence numbers follow the order the changes commit in, and a follower that read up to one never misses a
  lower one.

## Agents

**Agreed** ([principals](principals-and-permissions.md#agents)): an agent is a principal that a user with
`agents.create` creates; the organization owns it, so it outlives its creator's membership, and `org.principals.manage`
manages it.

**A member creates an agent; the organization owns it; an admin picks its model**

![A member creates an agent; the organization owns it; an admin picks its model](agents/agent-principal.svg)

The agent outlives its creator’s membership. Its creator keeps editing its name, description and instructions; only an
admin changes its model, its secrets, or deletes it.

- **A principal of kind `agent`,** owned by its organization (`org_id`), with a row in `agents`: its name, unique in the
  organization, a description, its standing instructions, its provider and model, and the secrets its tasks receive.
- **Its first grants come from its creator,** each a role at the organization or one of its repositories whose
  permissions the creator holds there, as every grant must; creating it needs no `org.grants.manage`. Later grants are
  ordinary grants.
- **Its creator and `org.principals.manage`** change its name, description and instructions. **Only
  `org.principals.manage`** picks its model and the secrets its tasks receive, and deletes it.
- **It has an email of its own,** `<agent ID>@agents.<Console host>`, verified, written when it is created: the harness
  commits with it, so the push worker maps the agent's commits to the agent.
- **Built-in agents,** such as the [AI reviewer](product.md#agents-and-ai-review) of
  [ENG-216](https://linear.app/arikkfir/issue/ENG-216), are marked `built_in`: an admin changes their model, never
  deletes them.

### Providers and models

**Agreed** ([architecture](architecture.md#settings-secrets-and-models)): an organization adds a model provider by its
credentials, kept as an organization secret its agents may read, and an admin picks each agent's provider and model.

**An organization adds providers by their keys; each agent runs on one of them**

![An organization adds providers by their keys; each agent runs on one of them](agents/models.svg)

Google and xAI are added the same way. Model names are the provider’s; Cobranch suggests current ones and accepts any.

- **A provider is a row of the organization's settings:** one of Anthropic, Google, xAI, OpenAI or DeepSeek, and the ID
  of the organization secret that holds its API key. `org.manage` adds, changes and removes one. A provider whose secret
  is gone shows as broken.
- **Picking a model** takes `org.principals.manage`: one of the providers the organization has, and the model's name.
  The Console suggests each provider's current models; Cobranch never checks a name against the provider, so a new model
  works the day it ships.
- **The agent must read the key.** The controller gives a task its provider's key only if the agent holds `secrets.read`
  on that secret. The Console's model picker offers to grant Secret reader on it in the same step.
- **Secrets a task receives** are the agent's `secrets` map, each an environment variable named after its key, from a
  secret ID the agent may read; the provider's key is not among them, and the harness keeps it out of every tool's
  environment.

## Tasks

**Agreed** ([principals](principals-and-permissions.md#agents)): a user with `agents.assign` assigns a task to an agent,
and the task does no more than its assigner can; it receives the agent's secrets, whoever assigned it.

**A task does what both its agent and its assigner may, checked at every call**

![A task does what both its agent and its assigner may, checked at every call](agents/cap.svg)

Secrets are the one exception, as agreed: the task receives the agent’s secrets whoever assigned it. A task Cobranch
starts itself has no assigner, so the agent’s grants alone cap it.

- **A task** names its agent, a title and instructions in Markdown, and optionally where it works: a repository, a
  branch, a chat thread. A task assigned from a thread talks in that thread; one that names a branch without a thread
  starts one there.
- **The cap is checked at every call,** not copied: the task token's permissions at a scope are the agent's there,
  intersected with the assigner's there now. So a grant the assigner loses, the task loses at once, and an assigner who
  leaves the organization stops every task they assigned, which the controller then cancels.
- **Assigning** takes `agents.assign` in the organization, and `repo.read` on the repository a task names. Cancelling
  takes being its assigner, or `org.principals.manage`.
- **Cobranch's own tasks,** such as [ENG-216](https://linear.app/arikkfir/issue/ENG-216)'s reviews, have no assigner:
  the agent's own grants are their cap.
- **Each task has a number** in its organization, `#17`, for the Console.

**A task queues, runs, waits for answers, and ends**

![A task queues, runs, waits for answers, and ends](agents/task-life.svg)

A pod that dies while Running is started again, at most three times, and resumes from the transcript; the task stays
Running meanwhile.

| State | When |
| --- | --- |
| Queued | Assigned; waiting for the controller, or for room in the namespace's quota |
| Running | Its pod started; the harness works |
| Waiting | The agent asked a question in its thread and waits for an answer; its pod stops after `agents.idle` and the next message in the thread queues it again |
| Succeeded | The agent finished, with its summary |
| Failed | The agent gave up, the model refused or failed past its retries, the task ran past its timeout, its pod failed three times, or it could not start: no model, a secret it may not read, an installation that runs no pods |
| Cancelled | Its assigner or an admin cancelled it, or its assigner left the organization |

## The agent's pod

**Agreed** ([architecture](architecture.md#job-isolation)): an agent works in a pod of Cobranch's agent image, in its
organization's job namespace, reaching Cobranch through its public URLs like any outside client.

**Assigning a task starts a pod that works through Cobranch’s APIs**

![Assigning a task starts a pod that works through Cobranch’s APIs](agents/task-run.svg)

Every arrow into REST / GraphQL or Git carries the task token, so each is checked as the agent capped by Alice.

- **The controller runs agent tasks** as a queue of its own, beside [jobs'
  runs](https://claude.ai/artifact/FqmP4JjVLtfXc5q4uAZ68y#life): a replica claims a queued task `FOR UPDATE SKIP LOCKED`
  under a lease, makes sure the organization's namespace exists, opens the provider's key and the agent's secrets
  through the same call runs use, and creates the task's Secret and pod, labelled as an agent task.
- **One pod per task, for as long as the task lives.** A task can take hours and many turns of conversation; its pod
  keeps its workspace meanwhile. Two tasks never share a pod, so one assigner's work never leaks into another's.
- **The token is a file.** The task token, `cba_…`, is valid for an hour; the controller renews it every half hour into
  the pod's Secret, which the pod mounts, and the harness reads it before every call. It acts as the agent, capped by
  the assigner, on the task's organization only.
- **The pod** runs `cobranch agent` in the image `cobranch-agent` as a non-root user under Pod Security `restricted`,
  with `/workspace` on an `emptyDir`, the operator's `agents.resources`, and `COBRANCH_URL`, `COBRANCH_GIT_URL`,
  `COBRANCH_TASK` and the token's path in its environment. The provider's key reaches the harness alone.
- **A pod that dies** is started again by the controller, at most three times, and the harness resumes from the
  transcript.

**An agent pod holds its workspace, a token file and its secrets, and reaches only the internet**

![An agent pod holds its workspace, a token file and its secrets, and reaches only the internet](agents/pod.svg)

The harness reads the token file before each call, so a renewed token is picked up without a restart. No Kubernetes
token is mounted.

### The harness

**The harness: one loop, one model interface, a tool registry, and the transcript**

![The harness: one loop, one model interface, a tool registry, and the transcript](agents/harness.svg)

[ENG-216](https://linear.app/arikkfir/issue/ENG-216) registers its review tools for its task kind; the loop and the
adapters stay the same.

- **A loop over one model interface.** The harness asks the agent's model for its next turn, with the conversation so
  far and the tools it offers, runs the tools the model calls, and loops until the model calls `finish`, or the task
  reaches `agents.maxTurns`, its timeout or its cancellation.
- **One adapter per provider:** Anthropic's Messages API; OpenAI's Chat Completions, which xAI and DeepSeek also speak
  at their own addresses; Google's Gemini API. Each is optional code behind the interface, and none is called unless an
  organization added that provider.
- **The tools,** chosen by the task's kind, so [ENG-216](https://linear.app/arikkfir/issue/ENG-216)'s review tasks add
  theirs: files in `/workspace` (list, read, search, write, edit), a shell there with a timeout, Git through the shell
  with a credential helper that reads the token file, the task's chat thread (read, post, ask and wait for an answer),
  and `finish`.
- **The checkout.** When the task names a repository, the harness clones it over HTTPS into `/workspace` at the task's
  branch, or `main`, before its first turn, and commits as the agent.

### The transcript

**A restarted pod rebuilds the conversation from the transcript and carries on**

![A restarted pod rebuilds the conversation from the transcript and carries on](agents/transcript.svg)

An append names its number, so one that repeats answers the entry already there. `post_message` carries its message’s
ID, so re-running it never posts twice.

- **Every step is an entry,** appended through the REST API before the next step starts: the instructions it began with,
  each model turn with the tool calls it asked for, each tool's result, and each change of state. An entry's number is
  the next in its task, so an append that repeats one answers the one already there.
- **Resuming replays it.** A new pod reads the transcript, rebuilds the conversation, and re-runs only the tool calls
  that have no result yet; the tools that change something outside the pod, posting and pushing, are safe to repeat,
  since a message carries its ID and a push names the commit it moves from.
- **Large outputs are cut** to 64 KiB in the transcript, marked as cut; the model sees the same cut.
- **Who reads it.** The assigner, `org.principals.manage`, and anyone who reads the task's repository, live on the
  task's page.

## Agents act as principals

**Agreed** ([architecture](architecture.md#constraints)): agents use the front door, the same APIs and Git server as
people, as principals of their own.

**An agent posts and pushes through the same doors as a person**

![An agent posts and pushes through the same doors as a person](agents/acting.svg)

An agent’s commits carry its own no-reply email, so the push worker maps them to the agent, and the Console shows them
as its work.

- **Posting.** A message an agent posts shows the agent as its author, marked as an agent, and links the task it came
  from.
- **Pushing.** The Git server takes a task token over HTTPS as it takes a personal token: the push is the agent's,
  checked against `repo.push` as capped, and against the merge requirements like anyone's, so an agent brings work into
  `main` only through a Ready branch, unless its cap includes `repo.bypass`.
- **Everything else** an agent may do (annotate, merge, read) is the API's ordinary checks over the capped token.

## The chart

Agents add no workload and no RBAC: the controller and its role over job namespaces come with
[jobs](https://claude.ai/artifact/FqmP4JjVLtfXc5q4uAZ68y#chart).

| Value | Default | What it sets |
| --- | --- | --- |
| `agents.image` | `cobranch-agent` at the chart's version | The agent pods' image |
| `agents.resources` | Requests 250m, 512Mi; memory limit 2Gi | Each agent pod |
| `agents.timeout` | 24h | A task's longest life |
| `agents.idle` | 30m | How long a waiting task keeps its pod |
| `agents.maxTurns` | 200 | A task's most model turns |

An installation with `jobs.pods: false` runs no agent pods: tasks fail with `this installation runs no pods`, and chats
work as ever.

## The Console and APIs

**Chats sit on a branch’s page; agents and tasks get pages of their own**

![Chats sit on a branch’s page; agents and tasks get pages of their own](agents/console.svg)

A message an agent posts links its task’s page, so a reader sees how it got there.

| REST, under `/v1/orgs/{org}/repos/{repo}/branches/{branch}` | GraphQL | Takes |
| --- | --- | --- |
| `GET /chat`, a page of threads with their messages | `Repository.chat(branch)` | `repo.read` |
| `POST /chat/threads`, `POST /chat/threads/{thread}/messages` | `startChatThread`, `postChatMessage` | `messages.post` |
| `PATCH`, `DELETE /chat/threads/{thread}/messages/{message}` | `editChatMessage`, `deleteChatMessage` | `messages.post` on one's own, `messages.moderate` to delete another's |
| `PATCH /chat/threads/{thread}` (`resolved`) | `resolveChatThread` | `messages.post` on one's own, `messages.moderate` on another's |
| `GET /chat/updates` | `chatUpdates` (subscription) | `repo.read` |

| REST, under `/v1/orgs/{org}` | GraphQL | Takes |
| --- | --- | --- |
| `GET`, `POST /agents`; `GET`, `PATCH`, `DELETE /agents/{agent}` | `agents`, `createAgent`, `updateAgent`, `deleteAgent` | `org.read`; `agents.create`; its creator or `org.principals.manage` |
| `PUT /agents/{agent}/model`, `PUT /agents/{agent}/secrets` | `setAgentModel`, `setAgentSecrets` | `org.principals.manage` |
| `GET`, `PUT`, `DELETE /model-providers/{provider}` | `modelProviders`, `setModelProvider`, `removeModelProvider` | `org.read`; `org.manage` |
| `GET`, `POST /tasks`; `GET /tasks/{n}`; `POST /tasks/{n}/cancel` | `tasks`, `assignTask`, `cancelTask` | `org.read`; `agents.assign`; its assigner or `org.principals.manage` |
| `GET`, `POST /tasks/{n}/transcript` | `Task.transcript`, `taskUpdates` (subscription) | Who reads the task; the task's own token appends |

Changes take `If-Match` with the version they read, as every change does; a stale one is refused (412, or
`precondition.failed`). The Console shows a branch's chat on its page and follows it live; on a branch's file, a click
on a line's number, then a shift-click on another, picks lines to start a thread on, and the page lists the chat's
threads on the file.

## Data model

**This slice’s tables: chats, agents, providers, tasks and transcripts**

![This slice’s tables: chats, agents, providers, tasks and transcripts](agents/er.svg)

`chat_threads`, `chat_messages`, `agents`, `model_providers` and `agent_tasks` carry trigger `audit()`; transcripts are
the task’s own record and don’t. Every new row carries `org_id`. A message’s `task_id` names the task an agent posted it
from.

## Rollout

**Five pull requests, each keeping main working**

![Five pull requests, each keeping main working](agents/rollout.svg)

Chats ship first: they need nothing from jobs, and [ENG-216](https://linear.app/arikkfir/issue/ENG-216)’s review
comments build on them.

- **Each pull request keeps `main` working** and is Part of [ENG-215](https://linear.app/arikkfir/issue/ENG-215); the
  fifth closes it. Chats need nothing else; agents and their models need
  [jobs](https://claude.ai/artifact/FqmP4JjVLtfXc5q4uAZ68y#secrets)' secrets; pods need its controller and namespaces.
- **An operator upgrades** with nothing to do for chats, agents and tasks; agent pods need jobs' RBAC, which the
  operator already granted for job pods, and the image `cobranch-agent`, which the chart names.
- **An organization adopts agents** by adding a provider's key as a secret, adding the provider, creating an agent and
  having an admin pick its model.

## Risks

- **An agent holds its secrets,** and an assigner may coax one out of it: a secret goes only to agents trusted with it,
  as the principals design agreed.
- **The model sees what the agent reads.** The code and chats a task reads go to its provider; an organization picks
  providers it trusts with them.
- **Model calls cost money,** charged to the organization's own key; `agents.maxTurns` and the timeout bound one task,
  and [ENG-238](https://linear.app/arikkfir/issue/ENG-238)'s quotas bound the rest.
- **A shell in the pod runs what the model asks.** The pod's bounds are the namespace's: no ingress, egress to the
  internet only, Pod Security `restricted`, no Kubernetes token, and a token capped by the assigner.
- **Prompt injection.** Code, files and messages may carry instructions to the agent; the cap means it can only do what
  its assigner could, and every push still meets the merge requirements.

## Decisions

| Proposal | Why | Decision |
| --- | --- | --- |
| A branch's chat is threads of Markdown messages; a thread may point at a file and line range at one commit | The product's branch chat; review comments point at code | Agreed |
| An anchor stays on the commit it was made at, marked when the branch moves on | Guessing where lines went misleads; the reader sees what the writer saw | Proposed |
| Live updates are GraphQL subscriptions woken by `NOTIFY` and replayed from `events`; REST long-polls the same feed, which the Console follows | The architecture's live updates; agents, SDKs and the Console's REST pages need no WebSocket | Proposed |
| A message may carry its poster's own ID, and a repeat answers the first | Agents resume and clients retry without double posts | Proposed |
| Agents are principals of kind `agent`, owned by the organization, with a row in `agents` | The principals design | Agreed |
| An agent's creator gives its first grants, each within their own permissions, without `org.grants.manage` | Members create agents; nobody grants more than they hold | Proposed |
| Providers are organization settings naming an organization secret; `org.manage` adds them | The architecture's providers as secrets | Proposed |
| `org.principals.manage` picks an agent's model and secrets; Cobranch doesn't validate model names | An admin picks the model; new models work at once | Agreed |
| A task's permissions are the agent's intersected with the assigner's, checked at every call | The assigner cap; revocation applies at once | Agreed |
| A task receives the agent's secrets whoever assigned it | The principals design | Agreed |
| One pod per task, kept while the task lives, stopped when it waits past `agents.idle` | Tasks never share a workspace across assigners; resuming already exists | Agreed |
| Agent tasks are the controller's queue beside runs, in the same namespaces, through the same secrets call | One runtime for jobs and agents | Proposed |
| Task tokens are files the controller renews hourly | The principals design's one-hour token for long tasks | Agreed |
| The harness speaks to providers through one interface, an adapter each; xAI and DeepSeek through OpenAI's protocol | Vendors stay optional adapters | Agreed |
| The transcript is appended entry by entry through the REST API, numbered per task | A restarted pod resumes; the Console shows it live | Agreed |
| Each agent holds a verified no-reply email, so its commits map to it | Agents push like any principal | Proposed |
| Mentions, agents waking on chat messages by themselves, and agents' files in the object store come later | Keeps this slice to its issue | Proposed |

## Open questions

The owner left these to this design's recommendations (2026-11-02), so each is decided as recommended.

1. **One pod per task, not per agent?** Decided: per task. The product design said a long-running pod per agent; a pod
   per task keeps each assigner's workspace and cap apart, and a task's pod still lives as long as the task.
2. **Stop a waiting task's pod after half an hour?** Decided: yes, and queue it again on the next message in its thread,
   resuming from the transcript. The alternative keeps idle pods holding memory for as long as nobody answers.
3. **May an agent's creator pick its model?** Decided: no, as agreed: only `org.principals.manage`, since the model
   decides where the organization's code goes and what it pays. A creator who holds it picks one while creating the
   agent.

Cobranch is a Kfirs project, created by Arik Kfir.
