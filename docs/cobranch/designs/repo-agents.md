# Cobranch organization and repository agents

> **Snapshot.** This design lives in its [organization and repository agents
> page](https://claude.ai/artifact/QcnH4XmDH8BLcGxGkD5U11), linked with Cobranch's other designs; this file is generated
> from it, with its diagrams, as of 2026-11-27, and synced again once Cobranch's shape settles.

**Status**: Agreed 2026-11-27 · slice 1 shipped · [ENG-293](https://linear.app/arikkfir/issue/ENG-293) · Builds on
[agents](agents.md#agents) and [the agent’s pod](agents.md#the-agents-pod) · Adds a key to [jobs’
`/cobranch.yaml`](jobs.md#cobranchyaml) · No new chart value

Agents come in two kinds: an organization’s, defined on its settings pages, and a repository’s, defined in main’s
/cobranch.yaml. Each runs in an image its own definition names, or the installation’s default. Jobs name an agent to run
as its task, and never change its image.

## Scope

Agents today are the organization's alone, and every one of them runs in one image the installation's operator picks
(`agents.image`, Cobranch's own Alpine image without it). A repository whose checks need a toolchain has no way to give
one to its agents: on cobranch.app, the reviewer of `cobranch/website` couldn't run the site's link check, which job
`ci/site` runs in a Python image on every push ([ENG-293](https://linear.app/arikkfir/issue/ENG-293), found dogfooding
in [ENG-270](https://linear.app/arikkfir/issue/ENG-270)).

The owner's four rules, 2026-11-27:

1. Agents split into organization agents and repository agents.
2. An organization agent names its own image on its settings page, or runs in the installation's. Repositories and jobs
   don't override it. Repositories' jobs may use it.
3. A repository agent is defined in the repository's `/cobranch.yaml`, with its image, or runs in the installation's.
4. A job selects an agent. It doesn't override the agent's image.

**An agent’s image comes from where the agent is defined; a job picks an agent, never an image**

![An agent’s image comes from where the agent is defined; a job picks an agent, never an image](repo-agents/images.svg)

A task always runs in its agent’s image, whoever starts it: a person, a review, or a job’s run.

The rest of this page is what those rules take: where each kind of agent lives, what a job that names one does, and the
slices that build it. Choices beyond the four rules are under [Decisions](#decisions), each with what it rejected.

## The image

| Agent | Image from | Empty | Changed by |
| --- | --- | --- | --- |
| Organization agent | Its settings page, field Image | The installation's `agents.image` | Whoever may pick its model (`org.principals.manage`), as a person |
| Repository agent | `agents.<name>.image` in main's `/cobranch.yaml` | The installation's `agents.image` | A push to main |
| The built-in reviewer | Its settings page, as any organization agent | The installation's `agents.image` | As any organization agent |

- **Any image with `sh` and `git`.** The pod copies Cobranch's static binary in, as it does today ([the agent's
  pod](agents.md#the-agents-pod)); the harness clones and pushes with `git`, and its shell tool runs `sh -c`. The
  harness checks both as it starts, and a task whose image lacks one fails at once, naming the missing tool, instead of
  after the model's first turns.
- **A chosen image pulls with the pod's credentials.** Kubernetes keeps image pull secrets on the pod, not on a
  container, and the pod's own containers in Cobranch's image need the installation's: so an image an organization or a
  repository names is pulled with them too. The chart tells operators to let those secrets reach only images every
  organization may run. A private image of the organization's own is a later question ([Open
  questions](#open-questions)).
- **The pod is otherwise the same:** the model proxy and the binary's copy keep running Cobranch's own image, so the
  provider's key never enters the agent's image.

## Repository agents

```yaml
# /cobranch.yaml, read from main
agents:
  site-checker:                       # 1-100 of a-z, 0-9, -, _, ., /; unique in the repository
    description: Checks the site's pages and links
    instructions: |                   # standing instructions, as an organization agent's
      Run `python3 tools/check_links.py` and report every broken link.
    image: ghcr.io/acme/site-tools:1.4  # optional; the installation's agents.image by default
    model: anthropic/claude-opus-5-5  # one of the organization's models
    permissions: [repo.read, messages.post]   # within main's ceiling; these by default
    secrets:
      SITE_TOKEN: 0193a8c2-5c1e-7f20-9d4b-3e1f6a2b7c90
```

**A repository agent is a principal of its repository, held to main’s ceiling**

![A repository agent is a principal of its repository, held to main’s ceiling](repo-agents/principals.svg)

An agent that main’s file no longer declares is retired: it takes no new task and keeps its history. Declared again
under its name, it comes back.

- **Read from main alone**, as `repository` and `branches` are. A repository agent is a principal that holds
  permissions, so a branch may not define or change one; a branch's change to `agents` takes effect when it merges. A
  push that brings a broken `agents` key into main is refused, naming the line, as a broken `jobs` key is.
- **One principal per name**, kind `agent`, owned by the repository (`agents.repo_id`), created when main's file first
  declares it and updated each time main's file is recorded. Its commits carry its own email, as an organization agent's
  do.
- **Its permissions are what it declares, within main's ceiling**, on its own repository alone:
  `repository.jobs.ceiling`, Automation by default, which now caps everything main's file declares. A push into main
  whose file lists a permission beyond it is refused, naming the line. Without `permissions`: read the repository and
  post messages. It declares no artifact or secret permission: those come from grants, as anyone's.
- **Its model is one of the organization's.** `model` names a provider the organization added and one of its models, as
  `provider/model`, or a push into main that names another is refused, naming the line; without `model`, the
  organization's default model. A task of an agent whose model the organization no longer has fails as it starts, as an
  organization agent's without a model does today. The organization's spend caps and quotas count its tasks.
- **Its secrets work as a job's**: an environment variable from a secret's ID, received only once someone with
  `org.grants.manage` grants the agent Secret reader on the repository or on that secret.
- **Who starts its tasks:** a member with `agents.assign`, in its repository alone (its issues and branch chats), and
  runs of the repository's jobs. Each task does no more than both the agent and whoever started it may, as today.
- **Retired, not deleted.** An agent main's file no longer declares takes no new task, holds nothing on its repository,
  and its running tasks are cancelled; its principal keeps its tasks, commits and audit trail. Declared again under the
  same name, it comes back. The API changes no repository agent: its file does.
- **The Console lists them** on the repository's Agents page and in Settings › Agents under their repository, read-only,
  each with a link to its lines of main's file.

## Jobs that name an agent

```yaml
jobs:
  ai/site:
    on:
      push: {}
    agent:
      name: site-checker              # the repository's agent first, then the organization's
      instructions: |
        Check this commit's site. Fail if any page has a broken link.
    permissions: [repo.read, annotations.write]
    timeout: 30m
```

**A job that names an agent runs as one of the agent’s tasks, and ends as the task ends**

![A job that names an agent runs as one of the agent’s tasks, and ends as the task ends](repo-agents/run.svg)

The run page links the task’s transcript in place of a pod’s logs. Cancelling the run cancels the task, and the job’s
timeout is the task’s.

- **`agent` is a fourth kind of work**, beside `pod`, `script` and `webhook`. It takes the agent's name and the task's
  instructions, and nothing about the image, the model or the agent's secrets: those are the agent's.
- **The name resolves to the repository's agent first, then the organization's.** An organization agent must hold
  `repo.read` on the repository; otherwise the run fails as it is queued, naming the agent.
- **A run is one task**, of kind `job`, at the run's commit, detached, assigned by the job's principal. It does no more
  than both the agent and the job may, and pushes nothing. Its instructions are the agent's, then the job's.
- **The run ends as the task ends.** `finish` with succeeded makes the run Successful; failed, a cancellation, the
  timeout or the agent's cap make it Failed. The job's annotation takes the task's summary or reason, and links the
  task, whose transcript the run's page shows in place of a pod's logs.
- **Triggers, re-runs, the outside-push gate, the ceiling and the timeout** are a job's, unchanged
  ([triggers](jobs.md#triggers), [the outside-push gate](jobs.md#the-outside-push-gate)). Cancelling the run cancels the
  task.
- **It works on any branch's file**, since `jobs` comes from where the event happened: a branch can make a job of an
  agent that main's file defines, or of an organization agent, but can't define the agent.

## Data model

| Table | Change |
| --- | --- |
| `agents` | `image text NOT NULL DEFAULT ''` (slice 1); `repo_id uuid` referencing `repositories`, the `permissions text[]` it declares, the `line` of its name in main's file, `retired_at timestamptz`, and a unique name per repository beside the one per organization (slice 2, migration 00075) |
| `task_kinds` | Kind `job` (slice 3) |
| `agent_tasks` | `run_id uuid` referencing `job_runs` (slice 3) |
| `job_runs` | Nothing: an agent job's run has no pod, and keeps its task through `agent_tasks.run_id` |

## Rollout

**Three slices, each deployable alone**

![Three slices, each deployable alone](repo-agents/rollout.svg)

Slice 3 runs organization agents without slice 2, and reaches repository agents once slice 2 is in.

| Slice | Delivers | Unblocks |
| --- | --- | --- |
| 1. Organization agents' images | The agent's Image field and API, the controller using it, the harness's preflight | The website's reviewer, once its image has Python and Git |
| 2. Repository agents | `agents:` in main's `/cobranch.yaml`, their principals, permissions, secrets, retirement, and the Console's lists | Agents a repository owns, assignable in it |
| 3. Jobs that name an agent | `agent:` as a job's work, the task of kind `job`, the run following it | A repository's own AI checks, such as `ai/site` |

## Risks

| Risk | Mitigation |
| --- | --- |
| An image without `sh` or `git` fails its tasks after spending model turns | The harness's preflight fails the task first, naming what's missing |
| A branch grants itself an agent with more power | Agents come from main only, within main's ceiling; a branch's job can only name an agent, and the task stays within the job's permissions too |
| An organization names a private image the installation's registry credentials reach | The chart's `imagePullSecrets` says to let them reach only images every organization may run |
| Every push of a busy repository starts a model's task | A job's triggers and branch filters apply; the agent's cap and the organization's quotas bound the spend |

## Decisions

| Decision | Why | Rejected |
| --- | --- | --- |
| A repository agent comes from main's file alone | It is a principal with permissions and secrets; main is where a repository says what may act in it | From each branch, as jobs are: a branch could define an agent and grant it the branch's secrets |
| Main's `repository.jobs.ceiling` caps repository agents too | One ceiling for everything main's file declares; the owner already sets it | A ceiling of its own: a second knob with the same meaning |
| A job names its agent by name, the repository's first | The common case reads plainly; a repository can't reach another repository's agents | A prefix such as `org/reviewer`: noise for the common case |
| A run is one task that pushes nothing | A run checks a commit, as every job does; pushing is an assigned task's work | Tasks that push from a job: a job that writes needs its own branch rules |
| Chosen images pull with the pod's pull secrets | Kubernetes has no pull secrets per container, and Cobranch's own containers in the pod need them | Dropping them for a chosen image: an installation whose own image is private could then run no such task |
| A preflight for `sh` and `git` | A clear failure before any spend | A copied static `git`: no portable one exists for every image's C library |

## Open questions

- **Private images.** Organizations may want to name images from a private registry. Recommended later: a pull secret
  built from one of the organization's secrets, named beside the image.
- **Reviews by repository agents.** The built-in reviewer stays an organization agent, so a repository's review runs in
  the reviewer's image. Recommended: a later slice lets main's `/cobranch-review.yaml` name a repository agent that
  reviews in the reviewer's place.

Cobranch is a Kfirs project, created by Arik Kfir.
