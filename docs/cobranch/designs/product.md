# The Cobranch product design

> **Snapshot.** This design lives in its [product design page](https://claude.ai/artifact/BsCFv8jQ7XunQP2T64Sjoy),
> linked with Cobranch's other designs; this file is generated from it, with its diagrams, as of 2026-11-02, and synced
> again once Cobranch's shape settles.

**Status**: Decided 2026-10-27 · Revised after review, 2026-10-27 · No questions open at this level · Source:
[discussion #5](https://github.com/orgs/arikkfir-org/discussions/5)

An all-in-one, opinionated development platform for individuals and teams, with AI central to the development process
from every angle. It hosts the code, runs the jobs, delivers the result and holds the conversation around each change,
so a team needs nothing else to ship software.

## Purpose

Cobranch is a product, not an installation: anyone installs it on their own Kubernetes cluster with a Helm chart and
gets a working hub, with sign-in, organizations, Git hosting, CI, GitOps delivery, APIs and SDKs. It is a Kfirs project,
created by Arik Kfir.

The Kfirs hub (`infra`, `delivery`, `octomaton`, `tooling`) is not Cobranch. It is one hub assembled by hand, the
precursor that showed which capabilities a hub needs; Cobranch packages those lessons as one product, and the Kfirs hub
is later recreated on it ([below](#relation-to-the-kfirs-hub)).

### Two faces

**Two faces: the open-source product, and our own installation of it, sold as a service**

![Two faces: the open-source product, and our own installation of it, sold as a service](product/faces.svg)

Nothing in the product assumes cobranch.app, as nothing assumes the hub: it is one more installation, run by us.

Cobranch has two faces. **cobranch.dev** is the open-source product this design describes: anyone downloads it and runs
it as their own installation. **cobranch.app** is our own installation of it, sold as a paid self-serve service, the way
github.com and gitlab.com are. Running cobranch.app (plans, billing, its installation) is work of its own, after the
product's foundations; the product never assumes it. Cobranch was called DevHub until
[ENG-221](https://linear.app/arikkfir/issue/ENG-221) renamed it.

## The designs

Each domain has a design of its own, drafted here as a page and linked from this one.

| Design | Covers | State |
| --- | --- | --- |
| [Architecture](architecture.md) | Workloads, stores, the push path, events, the API and the stack. | Agreed |
| [Principals and permissions](principals-and-permissions.md) | Who acts, how each signs in, and what grants let them do. | Agreed |
| [Sign-in and organizations](sign-in-and-organizations.md) | Sign-in, sign-up, organizations, their members and API keys. | Agreed |
| [Back-office](back-office.md) | The Admin server and the Back-office: organizations, users, settings and providers. | Agreed |
| [Git hosting](git-hosting.md) | Repositories, the Git server and the push worker, personal tokens and login-less authors. | Agreed |
| [Branch status](branch-status.md) | Commit annotations, merge requirements, how a branch’s status is computed, and merges. | Proposed |
| [Code browsing](code-browsing.md) | Files, trees and history in the Console, with Markdown and READMEs rendered. | Proposed |
| [Git object storage](git-object-storage.md) | Where Git objects live: PostgreSQL by default, a bucket or local files by choice, and moving between them. | Proposed |
| [Jobs](https://claude.ai/artifact/FqmP4JjVLtfXc5q4uAZ68y) | Jobs from /cobranch.yaml: triggers, runs in per-organization job namespaces, annotations, secrets. | Proposed |
| [Delivery](https://claude.ai/artifact/Hhr4cGRUk6JtUDWRAUgWXd) | The built-in GitOps controller: delivery roots in /cobranch.yaml, an environment per branch, apply, prune and health. | Proposed |
| [Monitoring](https://claude.ai/artifact/1merS4FjzGF2DNSwr4uSUU) | Metrics for page-view times, code reads and the pack cache, exported over OpenTelemetry. | Proposed |
| [Agents](https://claude.ai/artifact/KWNwxfUQ15rK85CNzhYXr4) | Agents as principals, tasks, the agent pod and its harness, and branch chats. | Proposed |
| [Reviews](reviews.md) | Reviews on commits, required approvals, and the built-in AI reviewer. | Proposed |
| [Quotas](https://claude.ai/artifact/Uy8pkcdmvn5kp41xTTmne6) | Limits on organizations, repositories and Git data, set in the Back-office and checked on push. | Proposed |
| [Issues](https://claude.ai/artifact/QM3VmRAtnSvttwT9bNaN8n) | Issues, their links to branches, and agents taking them as tasks. | Proposed |
| [Helm chart](https://claude.ai/artifact/LyMHXgSoNQNgHtHNfEjvA8) | How one chart installs and upgrades Cobranch on any cluster. | Decided |
| [Repository](repository.md) | The monorepo, its continuous integration and its docs. | Decided |
| [Design system](design-system.md) | Tokens, type, components and the core screens. | Approved |

## Principles

Cobranch is opinionated: it picks one way to do each thing and makes that way complete.

- **AI-first.** AI reviews every push; agents work on tasks and talk in branch chats, through the APIs people use.
- **All-in-one.** Identity, Git, CI, delivery, review, issues and conversation: one product, one model, one API.
- **Organizations own everything.** Repositories and agents belong to organizations; a user belongs to one or more.
- **Branches are the unit of work.** No pull requests: a branch is the request to merge, with its status, chat and
  reviews.
- **Everything is a principal.** Users, groups, agents, keys, apps, workload identities and jobs, granted the same
  permissions.
- **Merging is explicit.** A branch never merges itself; whoever moves the base is the merging principal.
- **Pushes are immediate.** A push returns only once Cobranch recorded it, so the Console already shows it.
- **Kubernetes-native, cloud-agnostic.** Any cluster, one Helm chart; the operator brings the domain, ingress and
  scaling.
- **The default branch is the boundary.** Phase 1 lets a branch add or override jobs; phase 2 lets the default branch
  limit that.

## Capabilities

**Ten services over one model, one permission model and one API**

![Ten services over one model, one permission model and one API](product/map.svg)

## Model

Eleven kinds of object make up an installation, and every actor among them is a principal.

**Organizations own repositories and issues; everything else hangs off a branch or a commit**

![Organizations own repositories and issues; everything else hangs off a branch or a commit](product/model.svg)

The [architecture’s data model](architecture.md#data-model) turns these objects into tables.

### Settings

**Repository settings are code and take effect on merge; organization settings are data**

![Repository settings are code and take effect on merge; organization settings are data](product/settings-flow.svg)

Clients cannot push under the reserved `cobranch/` prefix; only Cobranch writes there.

The Console shows the settings in effect and every pending change: each branch whose `/cobranch.yaml` differs from the
default branch's, with its diff ([architecture](architecture.md#settings-secrets-and-models)). Organization and
repository settings also hold secrets, which `/cobranch.yaml` names by ID and grants hand to the agents and jobs that
use them ([secrets](principals-and-permissions.md#secrets)).

### Jobs

Jobs are inspired by Octomaton's `.octomaton.yaml`. How they are declared, triggered, run and report, with their
secrets, is in [Jobs](https://claude.ai/artifact/FqmP4JjVLtfXc5q4uAZ68y).

**A job runs when an event fires, as a principal with the permissions it declares**

![A job runs when an event fires, as a principal with the permissions it declares](product/jobs.svg)

Phase 1 lets any branch add jobs or override the default branch’s. Phase 2 pins job security: the default branch limits
what may be overridden, as Octomaton trusts only the default branch’s definitions.

### Branches

A branch is the request to merge into its base: there is no separate pull request. Each branch carries a real-time,
threaded chat whose messages can point at a file and line range, which is how review comments point at code.
Annotations, merge requirements, how a branch's status is computed and how a merge is recorded are in [Branch status and
merges](branch-status.md). Deleting a merged branch from the Console and the APIs is in progress
([ENG-241](https://linear.app/arikkfir/issue/ENG-241), [design](branch-status.md#deleting-a-branch)).

**A branch’s status is computed; the first rule that matches wins**

![A branch’s status is computed; the first rule that matches wins](product/branch-status.svg)

Merge requirements are a policy of the branch: the commit annotations it requires, optionally linear history, which is
off by default, and [path rules](architecture.md#settings-secrets-and-models), which apply only to branches that change
their paths.

**Merging is explicit: whoever moves the base is the merging principal**

![Merging is explicit: whoever moves the base is the merging principal](product/merge.svg)

A branch never merges itself. Every ref change, however it starts, goes through the [Git server’s push
transaction](architecture.md#merges-and-settings-take-the-same-road).

### Agents and AI review

**AI review runs on every push; agents work on the tasks users give them**

![AI review runs on every push; agents work on the tasks users give them](product/agents.svg)

Tasks are such as “implement issue X”, “review branch Y” or “research change Z into an issue”. The agent’s harness saves
its transcript as it goes, so a restarted pod resumes where it stopped.

An admin picks each agent's model from the providers the organization holds credentials for: Anthropic's Claude,
Google's Gemini, xAI's Grok, OpenAI's GPT and DeepSeek. One organization can run its reviewer on one model, a
refactoring agent on another and an agent for small tasks on a third
([models](architecture.md#settings-secrets-and-models)). Agents, their tasks, pods and branch chats are in
[Agents](https://claude.ai/artifact/KWNwxfUQ15rK85CNzhYXr4); reviews, required approvals and the built-in reviewer in
[Reviews and AI review](reviews.md).

### Delivery

An Argo CD-like GitOps controller, built in. Delivery roots, environments per branch, apply, prune and health are in
[Delivery](https://claude.ai/artifact/Hhr4cGRUk6JtUDWRAUgWXd).

**Every branch becomes an environment**

![Every branch becomes an environment](product/delivery.svg)

`/cobranch.yaml` maps branch patterns to target clusters; `main` is one environment like any other.

### Issues and integrations

Issues are Linear-like and belong to an organization. A branch is linked to an issue by its name, or by hand, so an
issue shows the branches that work on it and a branch shows its issue; agents take issues as tasks. The details are in
[Issues](https://claude.ai/artifact/QM3VmRAtnSvttwT9bNaN8n). Identity providers are the first integration, Slack comes
in phase 2 with several use cases still to design, and more follow.

## Architecture

**One Helm chart installs Cobranch; the operator brings the cluster around it**

![One Helm chart installs Cobranch; the operator brings the cluster around it](product/deploy.svg)

The [architecture](architecture.md#components) details each workload, the [Helm
chart](https://claude.ai/artifact/LyMHXgSoNQNgHtHNfEjvA8) how the chart installs and upgrades them, and [Git object
storage](git-object-storage.md) where Git objects live.

A push returns once the Git server has recorded it in one PostgreSQL transaction (its refs, pack, commits and branches),
so a fetch and the Console already show it. The push worker, which nobody calls and which may scale to zero, picks the
push up a moment later and derives the rest: its authors' principals, and later branch status, merges and jobs ([Git
hosting](git-hosting.md#a-push)).

Only the Git server reads Git objects: the Console's files, trees, history and rendered Markdown come from it through
the API servers ([Code browsing](code-browsing.md)).

## Relation to the Kfirs hub

**Each hand-assembled part of the Kfirs hub becomes a built-in part of Cobranch**

![Each hand-assembled part of the Kfirs hub becomes a built-in part of Cobranch](product/hub.svg)

Recreating the Kfirs hub on Cobranch is the first real installation, not the product itself. Octomaton’s run
definitions, its default-branch trust boundary and its organization pipelines are the model for Cobranch jobs.

## Decisions

| Question | Decision |
| --- | --- |
| Agents | Principals created by users and owned by the organization, working on assigned tasks with a toolset in Cobranch and beyond |
| Agent runtime | A long-running pod per agent, from Cobranch's agent image, whose harness saves the transcript as it goes; simple first |
| AI review | A built-in agent, configured by the repository's `/cobranch-review.yaml` |
| Settings | Repository and branch settings in `/cobranch.yaml`, Console edits through `cobranch/settings`, in effect on merge; organization settings in the database; the Console shows the settings in effect and every pending change |
| Secrets | Kept encrypted in organization and repository settings, named by ID in `/cobranch.yaml`, and received only by agents and jobs granted `secrets.read` ([secrets](principals-and-permissions.md#secrets)) |
| Models | Each agent runs on the provider and model its admin picks: Anthropic, Google, xAI, OpenAI or DeepSeek |
| Repository visibility | Inferred from grants, never counting the Owners: private to its creator by default, Internal once another member can reach it, Public once Anonymous can |
| Job override policy | Phase 1 allows adding or overriding any job; phase 2 pins job security |
| Branch events | Create, push, merge, delete, tag, review submitted, annotated |
| Where jobs run | Pod specs in Cobranch's own cluster; webhooks for anything elsewhere |
| APIs | Auth, GraphQL, REST and Admin servers, each a workload the operator routes |
| Git serving | A workload of its own beside the APIs; exposing SSH is the operator's |
| Delivery | GitOps roots, an environment per branch, target clusters by branch pattern in `/cobranch.yaml` |
| Issues | Linked to branches by branch name, or by hand |
| Organizations | Repositories belong to organizations; one installation serves many, and a user may belong to several |
| Operator surface | The Back-office starts small and evolves |
| Infrastructure as code | A Terraform provider for Cobranch's resources, a client of the REST API like any other, in [phase 2](#phase-2), once the roadmap's slices have landed |
| Phase 2 | [The phase 2 list](#phase-2): pinned job security, search with semantic search, Git LFS, Slack, members provisioned from an identity provider, the Terraform provider, Continuous Intelligence, repository hooks, an MCP server with a Claude Code integration, continuous documentation, an AI side panel, and quotas |
| Credit | The Kfirs entity and Arik Kfir, anywhere it fits |

No questions remain open at this level; the permission list, the agent harness and the delivery controller's
reconciliation are worked out in the subsystem designs.

## Next steps

Each domain gets its own design, drafted as a page like this one and linked under [The designs](#the-designs); they are
synced into the repository's docs once Cobranch's shape settles. The work is tracked in Linear's [Cobranch
project](https://linear.app/arikkfir/project/cobranch-56ee907dd1af), in six milestones of one issue per slice, each
slice after the ones it waits for. Where it stands:

- **Done:** the design, the installable foundations, identity and organizations (sign-in, organizations and the
  Back-office), Git hosting, branch status and merges ([ENG-199](https://linear.app/arikkfir/issue/ENG-199)), browsing
  code and rendering Markdown in the Console ([ENG-228](https://linear.app/arikkfir/issue/ENG-228),
  [design](code-browsing.md)), and Git objects in PostgreSQL by default
  ([ENG-237](https://linear.app/arikkfir/issue/ENG-237), [design](git-object-storage.md)).
- **In progress, in parallel:** jobs from `/cobranch.yaml` ([ENG-214](https://linear.app/arikkfir/issue/ENG-214),
  [design](https://claude.ai/artifact/FqmP4JjVLtfXc5q4uAZ68y)); operators' metrics for page views, code reads and caches
  ([ENG-235](https://linear.app/arikkfir/issue/ENG-235), [design](https://claude.ai/artifact/1merS4FjzGF2DNSwr4uSUU));
  agents and branch chats ([ENG-215](https://linear.app/arikkfir/issue/ENG-215),
  [design](https://claude.ai/artifact/KWNwxfUQ15rK85CNzhYXr4)); reviews and the built-in AI reviewer
  ([ENG-216](https://linear.app/arikkfir/issue/ENG-216), [design](reviews.md)); issues linked to branches
  ([ENG-217](https://linear.app/arikkfir/issue/ENG-217), [design](https://claude.ai/artifact/QM3VmRAtnSvttwT9bNaN8n));
  delivery through the built-in GitOps controller ([ENG-218](https://linear.app/arikkfir/issue/ENG-218),
  [design](https://claude.ai/artifact/Hhr4cGRUk6JtUDWRAUgWXd)); deleting a branch from the Console and the APIs once it
  has merged ([ENG-241](https://linear.app/arikkfir/issue/ENG-241), [design](branch-status.md#deleting-a-branch)); and
  storage and push quotas ([ENG-238](https://linear.app/arikkfir/issue/ENG-238),
  [design](https://claude.ai/artifact/Uy8pkcdmvn5kp41xTTmne6)), brought forward from phase 2 because an installation
  open to sign-up needs them. The designs are Proposed and each slice builds on them while they are reviewed; each
  builds first on what is already on `main` and on the others' agreed seams.
- **Next:** the rest of [phase 2](#phase-2).

**The roadmap by milestone, as of 2026-11-02**

![The roadmap by milestone, as of 2026-11-02](product/roadmap.svg)

### Phase 2

Planned after the foundations, each with a design of its own when its turn comes:

- **Pinned job security.** The default branch limits what a branch's jobs may override and which secrets they may read
  ([jobs](#jobs)).
- **Search,** full-text and semantic: embeddings of code, issues, branches and chats kept in PostgreSQL with pgvector,
  so people and agents find related work by meaning, with no search cluster to run.
- **Git LFS.**
- **Slack,** with several use cases still to design.
- **Members provisioned from an identity provider** (SCIM), so an organization imports and removes its people there.
- **A Terraform provider,** so an operator or an organization declares its Cobranch resources in code and applies them:
  organizations, their settings, members and roles, repositories, API keys and agents (identity providers stay in the
  chart's values), as teams provision their code host's repositories and teams with Terraform today. The provider is a
  client of the REST API with no privileges of its own, authenticating as an API key or a personal token, so it can do
  exactly what its principal may; a new resource kind reaches it once the API serves it. It waits for the roadmap's
  slices to land: the API's shape has to settle before a provider pins it.
- **Continuous Intelligence:** AI built into CI/CD that scans code continuously, beyond what each push changes, and
  offers insights, suggestions and ready-made fixes, as part of the AI-first agenda. Its scope needs discussion first
  ([ENG-229](https://linear.app/arikkfir/issue/ENG-229)).
- **Repository hooks:** a repository declares hooks that run on every push and refuse commits that fail them, like local
  commit hooks and ideally sharing their code, so a team shares and enforces its house rules and tests in the
  repository. They run inside the push, before it is recorded, with the isolation jobs have. Their scope needs
  discussion first ([ENG-230](https://linear.app/arikkfir/issue/ENG-230)).
- **An MCP server and a Claude Code integration:** Cobranch serves its tools over MCP, so AI clients such as Claude Code
  work with an installation directly, and a Claude Code integration adds that connection with skills for Cobranch's
  workflows and mods that bring Cobranch into its interface. Like the Terraform provider, the MCP server is a client of
  the APIs with no privileges of its own, and waits for their shape to settle
  ([ENG-231](https://linear.app/arikkfir/issue/ENG-231)).
- **Continuous documentation:** AI keeps scanning a repository's code and documents it in virtual READMEs, one for the
  repository and one for every directory, covering its contents, the product or system it builds, its design and its
  architecture, which the Console shows where it shows a README. The repository's settings give it context, guidelines
  and instructions. A push regenerates only the docs it affects, one package's or several levels' when a change
  underpins a rewrite, and the rest come from cache ([ENG-233](https://linear.app/arikkfir/issue/ENG-233)).
- **An AI side panel:** whoever views a repository in the Console asks it questions about the repository and has it act
  on it, with the viewer's own permissions and never more; it builds on [agents](#agents-and-ai-review)
  ([ENG-234](https://linear.app/arikkfir/issue/ENG-234)).
- **Quotas:** administrators cap what each user and organization may store and push, so an installation open to anyone's
  sign-up keeps its storage in check ([ENG-238](https://linear.app/arikkfir/issue/ENG-238),
  [design](https://claude.ai/artifact/Uy8pkcdmvn5kp41xTTmne6)).

Cobranch is a Kfirs project, created by Arik Kfir.
