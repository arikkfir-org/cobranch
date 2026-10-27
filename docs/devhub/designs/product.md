# The DevHub product design

> **Snapshot.** This design lives in its [product design page](https://claude.ai/artifact/BsCFv8jQ7XunQP2T64Sjoy),
> linked with DevHub's other designs; this file is generated from it, with its diagrams, as of 2026-10-27, and synced
> again once DevHub's shape settles.

**Status**: Decided 2026-10-27 · Revised after review, 2026-10-27 · No questions open at this level · Source:
[discussion #5](https://github.com/orgs/arikkfir-org/discussions/5)

An all-in-one, opinionated development platform for individuals and teams, with AI central to the development process
from every angle. It hosts the code, runs the jobs, delivers the result and holds the conversation around each change,
so a team needs nothing else to ship software.

## Purpose

DevHub is a product, not an installation: anyone installs it on their own Kubernetes cluster with a Helm chart and gets
a working hub, with sign-in, organizations, Git hosting, CI, GitOps delivery, APIs and SDKs. It is a Kfirs project,
created by Arik Kfir.

The Kfirs hub (`infra`, `delivery`, `octomaton`, `tooling`) is not DevHub. It is one hub assembled by hand, the
precursor that showed which capabilities a hub needs; DevHub packages those lessons as one product, and the Kfirs hub is
later recreated on it ([below](#relation-to-the-kfirs-hub)).

## The designs

Each domain has a design of its own, drafted here as a page and linked from this one.

| Design | Covers | State |
| --- | --- | --- |
| [Architecture](architecture.md) | Workloads, stores, the push path, events, the API and the stack. | Agreed |
| [Principals and permissions](principals-and-permissions.md) | Who acts, how each signs in, and what grants let them do. | Agreed |
| [Helm chart](https://claude.ai/artifact/LyMHXgSoNQNgHtHNfEjvA8) | How one chart installs and upgrades DevHub on any cluster. | In progress |
| [Repository](repository.md) | The monorepo, its continuous integration and its docs. | Decided |
| [Design system](design-system.md) | Tokens, type, components and the core screens. | Approved |

## Principles

DevHub is opinionated: it picks one way to do each thing and makes that way complete.

- **AI-first.** AI reviews every push; agents work on tasks and talk in branch chats, through the APIs people use.
- **All-in-one.** Identity, Git, CI, delivery, review, issues and conversation: one product, one model, one API.
- **Organizations own everything.** Repositories and agents belong to organizations; a user belongs to one or more.
- **Branches are the unit of work.** No pull requests: a branch is the request to merge, with its status, chat and
  reviews.
- **Everything is a principal.** Users, groups, agents, keys, apps, workload identities and jobs, granted the same
  permissions.
- **Merging is explicit.** A branch never merges itself; whoever moves the base is the merging principal.
- **Pushes are immediate.** A push returns only once DevHub processed it, so the Console already shows it.
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

Clients cannot push under the reserved `devhub/` prefix; only DevHub writes there.

The Console shows the settings in effect and every pending change: each branch whose `/devhub.yaml` differs from the
default branch's, with its diff ([architecture](architecture.md#settings-secrets-and-models)). Organization and
repository settings also hold secrets, which `/devhub.yaml` names by ID and grants hand to the agents and jobs that use
them ([secrets](principals-and-permissions.md#secrets)).

### Jobs

Jobs are inspired by Octomaton's `.octomaton.yaml`.

**A job runs when an event fires, as a principal with the permissions it declares**

![A job runs when an event fires, as a principal with the permissions it declares](product/jobs.svg)

Phase 1 lets any branch add jobs or override the default branch’s. Phase 2 pins job security: the default branch limits
what may be overridden, as Octomaton trusts only the default branch’s definitions.

### Branches

A branch is the request to merge into its base: there is no separate pull request. Each branch carries a real-time,
threaded chat whose messages can point at a file and line range, which is how review comments point at code.

**A branch’s status is computed; the first rule that matches wins**

![A branch’s status is computed; the first rule that matches wins](product/branch-status.svg)

Merge requirements are a policy of the branch: the commit annotations it requires and, optionally, linear history, which
is off by default.

**Merging is explicit: whoever moves the base is the merging principal**

![Merging is explicit: whoever moves the base is the merging principal](product/merge.svg)

A branch never merges itself. Every ref, however it moves, goes through the [push
worker](architecture.md#merges-and-settings-take-the-same-road).

### Agents and AI review

**AI review runs on every push; agents work on the tasks users give them**

![AI review runs on every push; agents work on the tasks users give them](product/agents.svg)

Tasks are such as “implement issue X”, “review branch Y” or “research change Z into an issue”. The agent’s harness saves
its transcript as it goes, so a restarted pod resumes where it stopped.

An admin picks each agent's model from the providers the organization holds credentials for: Anthropic's Claude,
Google's Gemini, xAI's Grok, OpenAI's GPT and DeepSeek. One organization can run its reviewer on one model, a
refactoring agent on another and an agent for small tasks on a third
([models](architecture.md#settings-secrets-and-models)).

### Delivery

An Argo CD-like GitOps controller, built in.

**Every branch becomes an environment**

![Every branch becomes an environment](product/delivery.svg)

`/devhub.yaml` maps branch patterns to target clusters; `main` is one environment like any other.

### Issues and integrations

Issues are Linear-like and belong to an organization. A branch is linked to an issue by its name, or by hand, so an
issue shows the branches that work on it and a branch shows its issue; agents take issues as tasks. Identity providers
are the first integration, Slack comes in phase 2 with several use cases still to design, and more follow.

## Architecture

**One Helm chart installs DevHub; the operator brings the cluster around it**

![One Helm chart installs DevHub; the operator brings the cluster around it](product/deploy.svg)

The [architecture](architecture.md#components) details each workload, and the [Helm
chart](https://claude.ai/artifact/LyMHXgSoNQNgHtHNfEjvA8) how the chart installs and upgrades them.

A push is synchronous: the Git server does not return to the client until the push worker has finalized the push, so the
Console already shows it.

## Relation to the Kfirs hub

**Each hand-assembled part of the Kfirs hub becomes a built-in part of DevHub**

![Each hand-assembled part of the Kfirs hub becomes a built-in part of DevHub](product/hub.svg)

Recreating the Kfirs hub on DevHub is the first real installation, not the product itself. Octomaton’s run definitions,
its default-branch trust boundary and its organization pipelines are the model for DevHub jobs.

## Decisions

| Question | Decision |
| --- | --- |
| Agents | Principals created by users and owned by the organization, working on assigned tasks with a toolset in DevHub and beyond |
| Agent runtime | A long-running pod per agent, from DevHub's agent image, whose harness saves the transcript as it goes; simple first |
| AI review | A built-in agent, configured by the repository's `/devbot.yaml` |
| Settings | Repository and branch settings in `/devhub.yaml`, Console edits through `devhub/settings`, in effect on merge; organization settings in the database; the Console shows the settings in effect and every pending change |
| Secrets | Kept encrypted in organization and repository settings, named by ID in `/devhub.yaml`, and received only by agents and jobs granted `secrets.read` |
| Models | Each agent runs on the provider and model its admin picks: Anthropic, Google, xAI, OpenAI or DeepSeek |
| Repository visibility | Inferred from grants: private to its creator by default, Internal once more members can reach it, Public once Anonymous can |
| Job override policy | Phase 1 allows adding or overriding any job; phase 2 pins job security |
| Branch events | Create, push, merge, delete, tag, review submitted, annotated |
| Where jobs run | Pod specs in DevHub's own cluster; webhooks for anything elsewhere |
| APIs | Auth, GraphQL, REST and Admin servers, each a workload the operator routes |
| Git serving | A workload of its own beside the APIs; exposing SSH is the operator's |
| Delivery | GitOps roots, an environment per branch, target clusters by branch pattern in `/devhub.yaml` |
| Issues | Linked to branches by branch name, or by hand |
| Organizations | Repositories belong to organizations; one installation serves many, and a user may belong to several |
| Operator surface | The Back-office starts small and evolves |
| Phase 2 | Slack, Git LFS, search, and members provisioned from an identity provider |
| Credit | The Kfirs entity and Arik Kfir, anywhere it fits |

No questions remain open at this level; the permission list, the agent harness and the delivery controller's
reconciliation are worked out in the subsystem designs.

## Next steps

Each domain gets its own design, drafted as a page like this one and linked under [The designs](#the-designs); they are
synced into the repository's docs once DevHub's shape settles. The work is tracked in Linear's [DevHub
project](https://linear.app/arikkfir/project/devhub-56ee907dd1af).

**The foundations, in order, as of 2026-10-27**

![The foundations, in order, as of 2026-10-27](product/roadmap.svg)

DevHub is a Kfirs project, created by Arik Kfir.
