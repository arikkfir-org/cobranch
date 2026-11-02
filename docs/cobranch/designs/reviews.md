# Cobranch reviews and AI review

> **Snapshot.** This design lives in its [reviews and AI review
> page](https://claude.ai/artifact/3xuNVy6ZnQhE4UF11v5jys), linked with Cobranch's other designs; this file is generated
> from it, with its diagrams, as of 2026-11-02, and synced again once Cobranch's shape settles.

**Status**: Proposed 2026-11-02 · [ENG-216](https://linear.app/arikkfir/issue/ENG-216) · Builds the [product’s AI
review](product.md#agents-and-ai-review) on [branch status](branch-status.md) and the agents of
[ENG-215](https://linear.app/arikkfir/issue/ENG-215) · Adds no workload and no chart value · Built by
[ENG-216](https://linear.app/arikkfir/issue/ENG-216), in three pull requests

People and agents review commits, and `main`’s `/cobranch.yaml` can require approvals before a branch merges. Every push
of a branch gets a review from the organization’s built-in AI reviewer, an agent whose model an admin picks and whose
settings come from `main`’s `/cobranch-review.yaml`; its comments open threads in the branch’s chat that point at the
code, and each submitted review triggers the jobs that listen for one.

## Scope

How commits get reviewed ([ENG-216](https://linear.app/arikkfir/issue/ENG-216)): reviews that people and agents submit
on commits, the reviews a branch's merge requirements can name, diffs to review, and the built-in AI reviewer that
reviews every push, configured by `main`'s `/cobranch-review.yaml`. The [product
design](product.md#agents-and-ai-review) fixed that the reviewer is a built-in agent configured by that file, and the
[principals design](principals-and-permissions.md#agents) that it holds Automation in every organization; the
[architecture](architecture.md#decisions) agreed that it reads the default branch's file, and that `review.submitted` is
a day-one event. This page builds them on [branch status](branch-status.md), and names what it adds.

**Reviews and the reviewer are this slice’s; agents, chats and jobs come from the slices beside it**

![Reviews and the reviewer are this slice’s; agents, chats and jobs come from the slices beside it](reviews/scope.svg)

Where this slice meets another, the seam is named in the section that uses it, as agreed with the
[ENG-214](https://linear.app/arikkfir/issue/ENG-214) and [ENG-215](https://linear.app/arikkfir/issue/ENG-215) threads on
2026-11-02.

Choices beyond the agreed designs are under [Decisions](#decisions), and the three questions this page raised, taken as
recommended, under [Open questions](#open-questions).

## Reviews

A review is a principal's verdict on a commit: Approved, Changes requested or Neutral, with a body and comments. It
belongs to the commit, as an [annotation](branch-status.md#annotations) does: every branch at that commit shows it.

**A reviewer’s latest review of a commit is the one that counts**

![A reviewer’s latest review of a commit is the one that counts](reviews/review-life.svg)

Reviews are records: nobody edits or deletes one, and `audit()` keeps every one on the log. A reviewer changes their
verdict by submitting again.

- **Three states.** Approved says the reviewer would merge it; Changes requested says it must change first; Neutral says
  neither, and only comments. Changes requested and Neutral need a body or a comment; Approved may have neither.
- **The latest counts.** A reviewer may review a commit as often as they like; each submission is a new review, and
  their latest on the commit is their verdict there. Nobody edits or deletes a review, and the table carries `audit()`.
- **Who.** `reviews.submit` submits, on any commit the repository holds, one's own included; whether a review counts
  toward a requirement is the requirement's business. `repo.read` reads them. Developers, Maintainers and Automation
  review; the AI reviewer holds Automation.
- **What it says.** A body of at most 65,536 characters of Markdown, rendered as the Console renders files; and up to
  100 comments, each on one file's lines of the reviewed commit ([Comments](#comments)).
- **One transaction** stores the review and its comments, appends the
  [architecture](architecture.md#events-and-background-work)'s `review.submitted`, recomputes the status of every live
  branch whose head is the commit, and queues the jobs its review triggers ([Jobs on reviews](#jobs-on-reviews)).

## Required reviews

A merge requirement may name reviews, beside the annotations and linear history that [branch
status](branch-status.md#merge-requirements) already reads from `main`'s `/cobranch.yaml`:

```yaml
# /cobranch.yaml on main
branches:
  main:
    requires:
      annotations: [ci/build]
      reviews:
        approvals: 1   # people's approvals of the head, not counting who pushed or authored it; 0 by default
        ai: true       # the AI reviewer's approval of the head; false by default
```

**What the base requires decides what a branch’s reviews do**

![What the base requires decides what a branch’s reviews do](reviews/check.svg)

These checks join the annotations, linear history and being up to date in [the branch’s
status](branch-status.md#branch-status), in the same order of rules: Processing first, then Blocked.

- **Of the head only.** A review is of a commit, so a branch's new head starts with none: people approve again, and the
  AI reviewer reviews the new head on its own. A person's approval doesn't survive a push, for now ([question
  1](#open-questions)).
- **`approvals` counts people.** Users whose latest review of the head approves it, except the user who pushed the head
  first and the user who authored it. Agents' and jobs' reviews show, but count only where a requirement names them, as
  `ai` names the reviewer. At most 10.
- **Changes requested blocks.** Where the base requires reviews, any reviewer whose latest review of the head requests
  changes blocks the branch until they review it again or the branch moves; where it requires none, reviews only show.
- **`ai` waits for the reviewer.** While the AI reviewer's task for the head is waiting, queued or running, the branch
  is Processing, as it is while a required annotation runs. Then its latest review of the head must approve. A task that
  failed, or a reviewer that is off for the repository or has no model, blocks the branch and says why.
- **Checked as strictly as the rest.** An unknown key under `reviews`, `approvals` outside 0 to 10 or `ai` not a boolean
  refuses the push into `main`, naming the line.

| A Blocked branch says | When |
| --- | --- |
| `main requires 2 approvals of its head, and it has 1` | Fewer people approved the head than `approvals` |
| `Dana requested changes on its head` | A reviewer's latest review of the head requests changes |
| `The AI reviewer requested changes` | `ai` is required and its latest review of the head doesn't approve |
| `The AI review failed: the model's provider refused the credentials` | `ai` is required and its task for the head failed |
| `The AI reviewer has no model` | `ai` is required and no admin gave the organization's reviewer a model |

## The AI reviewer

**Agreed** ([principals](principals-and-permissions.md#agents)): the AI reviewer is a built-in agent of every
organization, holding Automation there.

- **One per organization,** created with it, and for every organization already there when this slice lands. It is an
  agent of [ENG-215](https://linear.app/arikkfir/issue/ENG-215) marked built in: nobody deletes it, and
  `org.principals.manage` picks its provider and model, or suspends it, as for any agent
  ([models](architecture.md#settings-secrets-and-models)). Until it has a model, it reviews nothing.
- **It reviews every push of a branch,** but `main`, to a new head, in a repository it reviews: the push worker's
  transaction queues a review task for it at that head, the way it queues [job
  runs](https://claude.ai/artifact/FqmP4JjVLtfXc5q4uAZ68y#triggers). A newer push of the branch, or its deletion,
  cancels the branch's open review task.
- **It reads** the branch's diff against `main` and the files at the head through the same APIs as anyone, and its
  instructions: its built-in ones, then `main`'s `/cobranch-review.yaml` and the files it names. Never the branch's own
  copy.
- **It writes one review** of the head, with its comments: Approved when it finds nothing that must change, Changes
  requested when it does, Neutral when it reviewed nothing it was asked to, such as a diff past `maxDiffLines` or one
  whose every path is ignored, saying why.
- **It can do no more than Automation:** read, annotate, review and post. It never pushes or merges, so code that talks
  it into a wrong verdict wins only that verdict.

**A push queues a review task; the reviewer reads the change and submits one review**

![A push queues a review task; the reviewer reads the change and submits one review](reviews/push-review.svg)

The pod, the harness and its loop are [ENG-215](https://linear.app/arikkfir/issue/ENG-215)’s agent runtime on
[ENG-214](https://linear.app/arikkfir/issue/ENG-214)’s controller; this slice adds the task’s kind, its queueing and its
two tools.

### A review task

**A review task ends in a review, a failure, or a newer task**

![A review task ends in a review, a failure, or a newer task](reviews/task-life.svg)

States are [ENG-215](https://linear.app/arikkfir/issue/ENG-215)’s for every agent task; a task is a review task by its
kind, `review`, and names the repository, the branch and the head.

- **A task of kind `review`,** in [ENG-215](https://linear.app/arikkfir/issue/ENG-215)'s `agent_tasks`, naming the
  repository, the branch and the head, with no assigner: a task Cobranch starts runs on its agent's own grants. Its
  harness gets two tools of this slice beside its own: read the branch's diff, and submit the review; its comments go
  with the review.
- **Outside pushes wait.** A head pushed from outside the organization doesn't spend the organization's model on its
  own: when `main`'s `repository.jobs.outsidePushes` keeps [jobs
  waiting](https://claude.ai/artifact/FqmP4JjVLtfXc5q4uAZ68y#outside), the review task waits too, until a member with
  `repo.push` starts it.
- **Review again.** A member with `repo.push` asks for another review of the head at any time, which supersedes the open
  one: after a failure, or once a discussion in the chat settled a comment.
- **Its timeout** is 30 minutes. A task that fails or times out leaves no review, and the reason on the branch.

## `/cobranch-review.yaml`

The reviewer's settings for one repository, from `main` alone:

```yaml
# /cobranch-review.yaml on main
enabled: true                     # false: the reviewer reviews none of this repository's pushes
branches: ["**"]                  # whose pushes it reviews; every branch but main by default
paths:
  ignore: ["**/*.gen.go", "vendor/**", "**/*.lock"]   # changes it leaves out of the diff it reads
instructions: |                   # added to its built-in instructions
  Handlers answer errors as RFC 9457 problems; flag any that write them another way.
include: [CONTRIBUTING.md, "docs/conventions/*.md"]   # files of main it reads as instructions
maxDiffLines: 5000                # a larger change gets a Neutral review that says so
```

**The reviewer’s settings come from `main` alone; its model from the organization**

![The reviewer’s settings come from main alone; its model from the organization](reviews/file.svg)

The [architecture](architecture.md#decisions) agreed that the reviewer reads the default branch’s file, so a branch
cannot rewrite its own reviewer’s instructions; keeping the model out of the file keeps a repository from choosing what
its organization pays for.

- **No file is the defaults:** every branch, nothing ignored, no instructions of the repository's own. Whether the
  reviewer reviews a repository without one is [open question 2](#open-questions); recommended, yes, once its
  organization gave it a model.
- **Read as the requirements are.** A push that moves `main` reads the file at the new tip and replaces the repository's
  parsed copy in its transaction; a file that doesn't parse refuses the push, naming the line. `include` names at most
  20 paths or globs, read at review time from `main`'s tip, 256 KiB in all.
- **No model in the file.** The organization's admin picks the reviewer's model, so a repository can't choose what its
  organization pays for.

## Comments

**A review’s comments are anchored threads of the branch’s chat**

![A review’s comments are anchored threads of the branch’s chat](reviews/comments.svg)

Threads, messages, their anchors and live delivery are [ENG-215](https://linear.app/arikkfir/issue/ENG-215)’s; a thread
gains the review it belongs to.

- **A comment is a chat thread.** Submitting a review takes its comments, each a path, a line range on the reviewed
  commit's side or its parent's, and a body; the review's transaction opens one thread per comment in the branch's chat,
  anchored there, carrying the review, its first message the comment. People and agents answer in the thread like any
  other ([ENG-215](https://linear.app/arikkfir/issue/ENG-215)).
- **Anchored to the reviewed commit,** so the thread still points at what the reviewer saw after the branch moves on;
  the Console marks it outdated once the head changes those lines.
- **Sent whole.** The Console keeps comments drafted on the diff in the browser until the reviewer submits, so a review
  never shows half its comments.
- **Change requests stay whole for now.** A review that requests changes does so as a whole; resolving its threads one
  by one is the chat's, and doesn't change the review.

## Diffs

Reviewing needs the change, which [code browsing](code-browsing.md) left for later; this slice adds it.

**Only the Git server reads objects, so it builds the diffs**

![Only the Git server reads objects, so it builds the diffs](reviews/diff.svg)

[Code browsing](code-browsing.md) built the internal call and left diffs for later; a review needs them, so this slice
adds them to it.

- **A branch's diff** is from the merge base of `main` and its head, so commits `main` gained since don't show; **a
  commit's** is against its first parent, or the empty tree for a root commit.
- **Built by the Git server** on code browsing's internal call, in a throwaway repository of one snapshot: `git diff`
  with renames found, each file's status, its old and new paths, and its hunks.
- **Up to limits:** 3,000 files, and 20,000 lines in all, past which the answer says what it left out; a file past 5,000
  lines, or binary, is listed without hunks. Answers are cached by the pair of commits, which never change.

## Jobs on reviews

[Jobs](https://claude.ai/artifact/FqmP4JjVLtfXc5q4uAZ68y#triggers) declares the trigger, `review: {states, branches}`: a
review submitted of the head of a live branch matching `branches`, in one of `states` (all three by default), queues the
job, with the definitions of that head, at that head.

**Submitting is one transaction: the review, its threads, its event, the status and the jobs**

![Submitting is one transaction: the review, its threads, its event, the status and the jobs](reviews/submit.svg)

A failed submission changes nothing, so a branch never shows half a review, and no job runs for a review that didn’t
happen.

- **The review's own transaction** queues the runs, through [ENG-214](https://linear.app/arikkfir/issue/ENG-214)'s
  `queueRuns`, with the review's state, so a job never runs for a review that didn't happen, and a branch whose required
  job was just queued stays Processing.
- **Outside pushes** gate these runs as they gate the review task, by [the jobs'
  rule](https://claude.ai/artifact/FqmP4JjVLtfXc5q4uAZ68y#outside).
- **A review task ends** when its review is submitted, in the same transaction.

## The Console

**The Console reviews where it shows the change**

![The Console reviews where it shows the change](reviews/console.svg)

Comments drafted on lines stay in the browser until the review is submitted, so a review arrives whole. The status
words, icons and tones are [the design system’s](design-system.md#status-language).

- **A branch's page** shows each reviewer's latest verdict on the head, earlier heads' reviews folded, and the AI
  review's state: Waiting, Queued, Running, Failed with its reason, or its review. **Review** opens the Changes tab
  ready to review; **Review again** asks the reviewer for another.
- **The Changes tab** shows the branch's diff against `main`, file by file; a reviewer drafts comments on lines, then
  submits Approve, Request changes or Comment with a body.
- **A commit's page** lists its reviews and shows its diff against its parent.
- **The requirements panel** names `main`'s required approvals and `ai`, and a Blocked branch says which failed, as
  [branch status](branch-status.md#the-console) lists its other checks.

## APIs

| REST, under `/v1/orgs/{org}/repos/{repo}` | GraphQL | Takes |
| --- | --- | --- |
| `GET /commits/{sha}/reviews` | `Commit.reviews` | `repo.read` |
| `POST /commits/{sha}/reviews` | `submitReview` | `reviews.submit` |
| `GET /reviews/{id}` | `Repository.review` | `repo.read` |
| `GET /branches/{branch}/diff` | `Branch.diff` | `repo.read` |
| `GET /commits/{sha}/diff` | `Commit.diff` | `repo.read` |
| `POST /branches/{branch}/review` | `requestReview` | `repo.push` |

`POST …/reviews` answers 201 with the review and its `Location`; a review is never changed, so it takes no `If-Match`. A
branch carries its reviews checks with its others, and its AI review's task.

## Data model

**This slice’s tables: reviews and the reviewer’s settings, and what four others gain**

![This slice’s tables: reviews and the reviewer’s settings, and what four others gain](reviews/er.svg)

`reviews` carries trigger `audit()`; `review_settings`, like `merge_requirements`, is a parsed copy of a file whose
history is Git’s. Every new row carries `org_id`.

## Rollout

**Three pull requests, each with its tests and its Console pages**

![Three pull requests, each with its tests and its Console pages](reviews/rollout.svg)

Nothing changes for an operator: no workload and no chart value is added. A repository opts into required reviews
through its own `/cobranch.yaml`, and an organization into AI review by giving its reviewer a model.

- **Each pull request keeps `main` working** and is Part of [ENG-216](https://linear.app/arikkfir/issue/ENG-216); the
  third closes it. The first and second need nothing of the slices beside it; the third waits for
  [ENG-215](https://linear.app/arikkfir/issue/ENG-215)'s agents, tasks, harness and chats to merge.
- **Upgrading needs nothing of the operator.** Every organization gets its reviewer with the third pull request's
  migration, and it reviews nothing until an admin gives it a model.
- **A repository adopts required reviews** by pushing a `reviews` key under `branches` into `main`.
- **Bodies show as written until the third,** which renders them as Markdown, as the Console renders files, beside its
  comments.

## Risks

- **Every push costs a review.** Superseding, `branches`, `paths.ignore`, `maxDiffLines`, the outside-push gate and the
  organization's choice of model bound it; quotas ([ENG-238](https://linear.app/arikkfir/issue/ENG-238)) bound the rest.
- **Code under review can steer the reviewer.** It holds Automation alone and reads instructions only from `main`, so
  the worst a crafted change wins is a wrong verdict, which `approvals` beside `ai` catches.
- **A reviewer that never answers** holds a branch that requires `ai` Processing until its task times out, then Blocked
  with the reason and Review again.
- **Approving again after every push** is the price of reviews of the head only, until approvals that survive a push
  come ([question 1](#open-questions)).

## Decisions

| Proposal | Why | Decision |
| --- | --- | --- |
| The AI reviewer is a built-in agent of every organization, holding Automation; it reads `main`'s `/cobranch-review.yaml` | The principals design and the architecture | Agreed |
| A review is of a commit, in one of three states, never edited; a reviewer's latest review of a commit is their verdict there | Reviews and annotations are verdicts on commits, as the product's model has them; a record nobody rewrites | Proposed |
| `requires.reviews` names `approvals`, counting people's approvals of the head but its pusher's and author's, and `ai`, the reviewer's | The two requirements the issue asks for; people's eyes and the reviewer's are different requirements | Proposed |
| Changes requested by anyone blocks where the base requires reviews; elsewhere reviews only show | A requested change is a veto only where reviews matter | Proposed |
| The AI reviewer's task for the head keeps a branch that requires `ai` Processing | It is still running, as a required annotation is | Proposed |
| The push worker's transaction queues the review task, superseding the branch's open one | The architecture's "the changing transaction queues the reactions"; the jobs' supersession rule | Proposed |
| Comments are submitted with their review and open anchored chat threads in its transaction | The product's "review comments are chat messages that point at code"; a review arrives whole | Proposed |
| The model is the organization's choice, not the file's | The organization pays; the architecture's models are agents' settings | Proposed |
| The review task waits on outside pushes as jobs do | A stranger's push must not spend the organization's model | Proposed |
| Diffs come with this slice, built by the Git server | Reviewing needs the change; only the Git server reads objects | Proposed |
| The `review` trigger's matching is [ENG-214](https://linear.app/arikkfir/issue/ENG-214)'s; the review's transaction calls it | The jobs design owns triggers; the changing transaction queues runs | Agreed |
| Change requests resolved one by one, approvals that survive a push, reviews of `main`'s own pushes and path rules naming reviewers come later | Keeps this slice to its issue | Proposed |
| A person's approval doesn't survive a push | A review is of a commit; question 1 | Proposed |
| The reviewer reviews every repository once its organization gave it a model, unless the repository's file says `enabled: false` | The model is the organization's decision to pay; question 2 | Proposed |
| Only people's approvals count toward `approvals` | Automation can't stand in for people's eyes; question 3 | Proposed |

## Open questions

None open. The three this page raised were taken as recommended on 2026-11-02, while the owner was away, and stand as
Proposed among the [decisions](#decisions):

1. **A person's approval doesn't survive a push,** for now. A review is of a commit; carrying approvals across pushes
   that change nothing the reviewer saw can come as a setting once people use reviews.
2. **The reviewer reviews every repository once its organization gave it a model,** and a repository turns it off with
   `enabled: false`. The model choice is the organization's decision to pay for reviews.
3. **Only people's approvals count toward `approvals`.** An agent's verdict counts only where a requirement names it, as
   `ai` names the reviewer, so automation can't stand in for people's eyes.

Cobranch is a Kfirs project, created by Arik Kfir.
