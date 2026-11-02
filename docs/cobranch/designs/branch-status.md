# Cobranch branch status and merges

> **Snapshot.** This design lives in its [branch status and merges
> page](https://claude.ai/artifact/2NuiCJYpPVGGqX78b47dMH), linked with Cobranch's other designs; this file is generated
> from it, with its diagrams, as of 2026-11-02, and synced again once Cobranch's shape settles.

**Status**: Proposed 2026-11-01, deletion 2026-11-02 · [ENG-199](https://linear.app/arikkfir/issue/ENG-199) ·
[ENG-241](https://linear.app/arikkfir/issue/ENG-241) · Builds the [product’s branches](product.md#branches) on [Git
hosting](git-hosting.md) · Adds no workload and no chart value · Built by
[ENG-199](https://linear.app/arikkfir/issue/ENG-199), in four pull requests; deleting branches by
[ENG-241](https://linear.app/arikkfir/issue/ENG-241)

A branch is a request to merge into `main`: principals set annotations on its commits, `main`’s `/cobranch.yaml` names
the annotations a merge needs and whether history stays linear, the push worker computes each branch’s status after
every push, a principal merges a Ready branch by pushing it into `main` or by asking Cobranch, which records who did,
and a merged branch is deleted the same two ways.

## Scope

How a branch becomes a request to merge ([ENG-199](https://linear.app/arikkfir/issue/ENG-199)): annotations that
principals set on commits, merge requirements that name the annotations a merge needs and whether history stays linear,
a status the push worker computes for every branch, and an explicit merge, by a push into the base or by asking
Cobranch; and how a merged branch is deleted ([ENG-241](https://linear.app/arikkfir/issue/ENG-241)), the same two ways.
The [product design](product.md#branches) fixed the five statuses and their order, and the
[architecture](architecture.md#merges-and-settings-take-the-same-road) that every ref change goes through the Git
server's push transaction; this page builds them on [Git hosting](git-hosting.md#a-push), and names what it adds and
changes.

**This slice turns branches into requests to merge; reviews, jobs and settings edits build on it**

![This slice turns branches into requests to merge; reviews, jobs and settings edits build on it](branch-status/scope.svg)

Every later slice reads what this one computes: a branch’s status, the checks behind it, and who merged it.

Choices beyond the agreed designs are collected under [Decisions](#decisions), and three questions for the owner under
[Open questions](#open-questions).

## Annotations

An annotation is a principal's verdict on a commit, such as `ci/build` passing or `ai-review` asking for changes. It
belongs to the commit, not to a branch: every branch at that commit shows it, and deleting the branch keeps it.

**An annotation’s principal sets its status at will; it or a moderator deletes it**

![An annotation’s principal sets its status at will; it or a moderator deletes it](branch-status/annotation-life.svg)

A principal may set Failed or Successful at once, with no Pending before; the Console counts how long it ran from when
it was set to when it last reached either.

- **One per name on a commit.** A commit holds at most one annotation of each name. Its principal sets it and changes it
  at will, in any order of the four statuses: Pending, In progress, Failed and Successful. Another principal setting the
  same name is refused, naming whose it is. Of two principals setting a name at once, one sets it and the other is
  refused; a set that meets a deletion of the name waits for it, then sets its own.
- **What it says.** A name of 1 to 100 lowercase letters, digits and `-`, `_`, `.`, `/`, starting with a letter or
  digit, such as `ci/build`; a status; a summary of at most 500 characters; and a link to its details, an absolute
  `https` or `http` URL, optional. Cobranch keeps who set it, when, when its run started (when it was set, or last sent
  back from Failed or Successful to Pending or In progress) and when it reached Failed or Successful, so the Console
  shows how long it ran.
- **Who may do what.** `repo.read` reads them, `annotations.write` sets them and changes or deletes one's own, and
  `annotations.delete_any` deletes another principal's ([permissions](principals-and-permissions.md#permissions)).
  Developers and Automation set them; Maintainers also delete others'. A deleted annotation frees its name on the
  commit.
- **On any commit the repository holds,** pushed before or after; an unknown commit is not found. Each change appends
  the [architecture](architecture.md#events-and-background-work)'s event `annotation.changed`, its details saying
  whether it was set or deleted, and the table carries trigger `audit()`, so a deletion of someone else's annotation is
  on record; setting it again as it is changes nothing.
- **A version, its ETag.** Each change bumps an annotation's version. REST's `PUT` and `DELETE` name the version they
  were made from in `If-Match`, which refuses one made meanwhile, or one deleted meanwhile, with 412; `If-Match: *`
  names whichever is there, and a first set names `If-None-Match: *`, which refuses one already there. Without either,
  the answer is 428, so a CI run that retries late can't overwrite a newer run's verdict unawares. GraphQL's
  `expectedVersion` does the same, answering CONFLICT `precondition.failed`, and is optional there.

**Every role reads annotations; Developers and Automation set them; Maintainers also delete others’**

![Every role reads annotations; Developers and Automation set them; Maintainers also delete others’](branch-status/annotation-who.svg)

A name another principal set is refused to everyone else, Maintainers included, until it is deleted; then anyone with
`annotations.write` may set it.

## Merge requirements

A branch's merge requirements are what a merge into it needs: the annotations its incoming head must carry, Successful,
and optionally linear history, which is off by default. They are settings, so they live in the default branch's
`/cobranch.yaml`, as the [architecture](architecture.md#data-model) agreed, and PostgreSQL keeps a parsed copy:

```yaml
# /cobranch.yaml on main
branches:
  main:
    requires:
      annotations: [ci/build, ci/test]
      linearHistory: true
```

**A push that moves main replaces the requirements with those of its /cobranch.yaml**

![A push that moves main replaces the requirements with those of its /cobranch.yaml](branch-status/requirements.svg)

No `/cobranch.yaml`, or one without `branches`, leaves every branch without requirements.

- **Read where the push is.** When a push moves `main`, the Git server reads `/cobranch.yaml` at `main`'s new tip, and
  the push's transaction replaces the repository's parsed copy. The requirements in effect for a push are those before
  it, so a push that drops a requirement must meet it once more.
- **A file that doesn't parse refuses the push** into `main`, naming the line, so the requirements in effect always
  match the file. No file, or no `branches` key, means no requirements.
- **A branch named there has merge requirements,** even an empty list: [a push into it](#by-a-push) must merge Ready
  branches. A branch not named has none, and anyone with `repo.push` moves it, as today. Only `/cobranch.yaml`'s
  `branches` key counts in this slice; jobs ([ENG-214](https://linear.app/arikkfir/issue/ENG-214)), path rules and the
  Console's editing through `cobranch/settings` add to the same file later.

## Branch status

Every live branch other than `main` is a request to merge into its base, `main` in this slice. Its status is the first
rule that matches.

**A branch’s status is the first rule that matches**

![A branch’s status is the first rule that matches](branch-status/status.svg)

The order is the [product design’s](product.md#branches). Reviews join Blocked’s requirements with
[ENG-216](https://linear.app/arikkfir/issue/ENG-216), and jobs join Processing with
[ENG-214](https://linear.app/arikkfir/issue/ENG-214).

| Status | When |
| --- | --- |
| Processing | The push worker hasn't processed the push that last changed the branch, or a required annotation on its head is Pending or In progress |
| Blocked | It has commits to merge, and a required annotation on its head is missing or Failed, linear history is required and a merge commit is among its commits, or `main`'s tip isn't in it |
| Empty | Nothing to merge: its head is in `main`, and no merge took it there |
| Merged | Its head is in `main`, and a merge took it there, by the principal it names |
| Ready | Anything else: it has commits to merge and nothing blocks them |

- **Blocked says why.** A Blocked branch lists each requirement and how it stands, as the [design system's branch
  screen](design-system.md#screens) draws it: each required annotation on the head, linear history, and how far behind
  `main` it is. Reviews join the list with [ENG-216](https://linear.app/arikkfir/issue/ENG-216).
- **Merged is recorded, not inferred.** Git can't tell a branch merged by a fast-forward, `main` having moved on since,
  from a branch created at an old commit of `main` with nothing of its own; both are in `main`. So a branch is Merged
  only when a merge was recorded for its head; the other is Empty. A new commit on a Merged branch makes it a request
  again.

### Graph facts

The status needs facts only Git's objects hold: how many commits a branch has that `main` lacks (ahead), how many `main`
has that it lacks (behind), and how many of its commits are merges. The Git server works them out on every push, while
it holds the objects, and the push's transaction stores them on the branches: the [architecture's
push](architecture.md#a-push) put these graph facts in the manifest.

**The Git server counts every branch against main while it holds the push’s objects**

![The Git server counts every branch against main while it holds the push’s objects](branch-status/facts.svg)

A merge Cobranch builds goes through the same Git server and the same counting, since it is recorded as a push.

- **Every live branch, every push.** One `git for-each-ref` with `%(ahead-behind:<main>)` counts every branch against
  `main`'s new tip in a single walk, and `git rev-list --count --merges` counts merge commits only where `main` requires
  linear history.
- **Counted from the push's snapshot, checked as it records.** Another push may record first, between this one's
  snapshot and its record. The push's transaction keeps only the facts of branches still where they were counted, since
  the other push stored its own against the same `main`. A `main` another push moved, or any branch that moved under a
  push into `main`, would leave facts nobody counted: the push is refused with
  `refs/heads/<branch> moved while Cobranch took your push; nothing changed, so push again`.
- **Merged by this push.** When a push moves `main`, `git for-each-ref --merged=<new> --no-merged=<old>` lists the
  branches whose heads it brings into `main`; the push keeps them for the push worker, which records the merges.
- **A branch whose facts change turns Processing** in the push's transaction, and names the push. The rest keep their
  status.

### When it changes

**A push’s branches wait for the push worker; an annotation recomputes its branches at once**

![A push’s branches wait for the push worker; an annotation recomputes its branches at once](branch-status/recompute.svg)

A branch that a later push changed waits for that push’s processing instead, so a push that failed and waits for a retry
never computes a status from facts newer than it.

- **After a push,** the push worker claims it, records the merges it made, and computes the status of every branch that
  names it. A branch that a later push changed waits for that one instead.
- **After an annotation changes,** the API's own transaction recomputes the status of every live branch whose head is
  that commit, unless it waits for the push worker, so a CI system's verdict shows at once.
- **The status is stored,** with the requirement checks behind it, so lists filter by status and a push's transaction
  checks Ready without recomputing.

## Merging

A branch never merges itself: a principal moves its base, by pushing or by asking Cobranch, and the push worker records
that principal as the one who merged it ([product](product.md#branches)).

### By a push

**A push into a branch with merge requirements brings only Ready branches’ commits**

![A push into a branch with merge requirements brings only Ready branches’ commits](branch-status/accept.svg)

Deleting a branch with merge requirements needs `repo.bypass` too. The earlier checks of [Git
hosting](git-hosting.md#which-pushes-are-accepted) (branches and tags only, nothing under `cobranch/`, every old SHA
current) still come first.

- **Into a branch with merge requirements,** every commit a push adds, merges aside, must be in a Ready branch whose
  head the push brings into it, and each such branch must be Ready at that head. The Git server lists the commits no
  merged branch brings; the push's transaction checks the branches' status. A pusher holding `repo.bypass` skips both,
  and the push appends event `branch.bypassed`, naming what didn't hold.
- **Its own merge commits merge cleanly.** A merge commit the push adds that no brought branch holds must have two
  parents and the tree `git merge-tree` writes for them, so a conflict resolved by hand or a change slipped into a merge
  comes from no Ready branch and is refused. Where the branch requires linear history, the push adds no merge commit at
  all, not even one a Ready branch holds, except those already in `main`: Ready branches merge by fast-forward.
- **Creating such a branch** adds the commits `main` lacks, so a branch created at `main`'s tip brings nothing, and one
  created at a Ready branch's head brings that branch. Commits already in `main` never count against a push into another
  branch.
- **Deleting such a branch** needs `repo.bypass` too.
- **Into any other branch,** nothing is checked, as today; a branch it brings into `main` is still recorded Merged.

| The client sees | When |
| --- | --- |
| `main requires its commits to come from Ready branches: 3 commits come from none` | The push adds commits no merged branch holds |
| `main requires feature/login to be Ready: ci/test failed on 9f3c2e1` | A branch it merges isn't Ready |
| `main requires merge commits to merge cleanly: 9f3c2e1 holds changes of its own; make them in a Ready branch` | A merge commit of its own resolves a conflict, changes more than its parents merge, or has more than two parents |
| `main requires linear history: the push adds 1 merge commit` | `main` requires linear history and the push adds a merge commit, even one a Ready branch holds |
| `/cobranch.yaml: line 4: annotations must be a list of names` | `main`'s new `/cobranch.yaml` doesn't parse |

### By asking Cobranch

**A merge asked of Cobranch is a push the Git server builds for the requester**

![A merge asked of Cobranch is a push the Git server builds for the requester](branch-status/api-merge.svg)

If `main` moved after the snapshot, the transaction’s compare-and-swap refuses the push and the request answers 409, as
a client’s stale push is refused; nothing changed, so asking again is safe.

- **`repo.merge` merges a Ready branch;** `repo.bypass` also merges one that isn't. The request may name the head the
  requester saw, and is refused if the branch has moved since.
- **A merge commit by default,** `Merge branch 'feature/login'`, authored and committed by the requester; or a
  fast-forward, which writes nothing. Where `main` requires linear history only a fast-forward is offered, and a Ready
  branch can always fast-forward, since `main`'s tip is in it. A branch merged past its requirements that is behind
  `main` takes a merge commit; with linear history required it can't be merged until it is brought up to date.
- **The Git server builds it** on an [internal call](architecture.md#components) from the REST or GraphQL server, which
  sends on the requester's own token: the Git server checks `repo.merge` itself, and `repo.bypass` for a merge past
  `main`'s requirements, and merges only for that requester. It builds the merge in a throwaway repository of one
  snapshot, `git merge-tree --write-tree` and `git commit-tree`, then a pack of the new commit, uploaded as a push's
  would be. It records a push of `main` for the requester, transport `api`, through the same transaction and the same
  checks as a client's push, which also hold the branch to being Ready at the head it merges, even where `main` requires
  nothing, unless the requester holds `repo.bypass`; it answers once the push is committed.
- **Then as any push:** the push worker records the merge, and the branch turns Merged. Code-browsing
  ([ENG-228](https://linear.app/arikkfir/issue/ENG-228)) builds the internal call; this slice adds the merge to it.

| The requester sees | When |
| --- | --- |
| 403 `auth.forbidden` | The requester lacks `repo.merge` |
| 409 `branch.moved` | The branch moved from the head the request names, or it or `main` moved while the Git server built the merge |
| 409 `branch.not_ready` | The branch isn't Ready, and the requester lacks `repo.bypass` |
| 409 `branch.nothing_to_merge` | `main` holds every commit of the branch |
| 409 `merge.not_fast_forward` | A fast-forward of a branch that lacks some of `main`'s commits |
| 409 `merge.conflict` | A merge commit of changes Git can't merge, naming up to five paths |
| 409 `merge.refused` | The push the merge records breaks a rule, such as a `/cobranch.yaml` that doesn't parse, or a branch that turned Blocked while the Git server built the merge |
| 503 `git.unavailable` | The installation runs no Git server |

## Deleting a branch

A merged branch has done its job, and left on the list it crowds the branches still in work. Deleting it takes it off
the repository's branch list and keeps everything it brought: its commits, their annotations and its merge stay in
`main`'s history ([ENG-241](https://linear.app/arikkfir/issue/ENG-241)). Cobranch deletes a branch as Git does, by a
push that deletes its ref, so a deletion asked of Cobranch and a `git push --delete` are one kind of event, recorded and
followed the same way.

**Deleting a branch through Cobranch is a push of its ref to nothing, which the Git server records for the requester**

![Deleting a branch through Cobranch is a push of its ref to nothing, which the Git server records for the requester](branch-status/api-delete.svg)

No snapshot, no objects: a deletion brings nothing and changes no other branch’s facts. A branch that moved since the
page read it fails the compare-and-swap, and the request answers 412; nothing changed.

- **`repo.push` deletes a branch,** as it does through `git push --delete`; a branch that `/cobranch.yaml` names under
  `branches` needs `repo.bypass` too, as [a push](#by-a-push) does. `main` is never deleted through Cobranch's APIs.
  Deleting someone else's branch takes nothing more: Git never asked more, and the record names who did it.
- **Only at the head the requester saw.** REST's `DELETE` names that head in `If-Match`, quoted, and answers 412 once
  the branch moved from it, 428 without the header; `If-Match: *` deletes it wherever it is. GraphQL's `expectedHead`
  does the same, refusing with CONFLICT `branch.moved`, and is optional there, as `expectedVersion` is.
- **The Git server records it,** on an internal call beside the merge's (`POST …/delete`), for the requester alone: a
  push of one ref update, from the head to nothing, transport `api`, through the same transaction and checks as a
  client's push, which compares the head and the requirements again. It reads no snapshot: a deletion brings no objects
  and changes no other branch's facts.
- **The branch keeps its row:** its name, head, status and merge, with when it was deleted and by whom. The push records
  the same, and event `repo.pushed` names the deleted ref.
- **Then as any push.** The push worker processes it as it processes a client's, and whatever follows a push that
  deletes a branch, such as a job that runs on its deletion ([ENG-214](https://linear.app/arikkfir/issue/ENG-214)) or
  the teardown of its environments ([ENG-218](https://linear.app/arikkfir/issue/ENG-218)), follows this one too.
- **A merge's record outlives its branch.** A merge and a deletion asked in the same moment leave the push worker to
  process the merge after the branch is gone; the push keeps the IDs of the branches it merged, not only their names, so
  the merge is still recorded on the deleted branch's row.
- **Any branch but `main` may be deleted,** merged or not, as with Git. Its commits stay in the repository's objects,
  which maintenance keeps reachable or not, but once no ref reaches them no clone fetches them, so the Console asks
  harder before it deletes a branch with commits `main` lacks.

| The requester sees | When |
| --- | --- |
| 204 No Content | The push that deletes it is committed |
| 403 `auth.forbidden` | The requester lacks `repo.push`, or `repo.bypass` for a branch `/cobranch.yaml` names |
| 404 `branch.not_found` | No live branch has that name |
| 409 `branch.default` | The branch is `main` |
| 409 `branch.refused` | The push that deletes it breaks a rule, such as a branch under `cobranch/`, which only Cobranch writes |
| 412 `precondition.failed` | The branch moved from the head `If-Match` names |
| 428 `precondition.required` | No `If-Match` |
| 503 `git.unavailable` | The installation runs no Git server |

### In the Console

**The Console offers Delete branch to those who may push, and asks harder when commits would be left behind**

![The Console offers Delete branch to those who may push, and asks harder when commits would be left behind](branch-status/delete-console.svg)

A branch of that name pushed again later is a new branch, and its page shows it instead.

- **A branch's page** offers Delete branch to a viewer who may delete it: a plain confirmation on a Merged or Empty
  branch, and on any other a danger button whose confirmation names how many commits `main` lacks. It deletes at the
  head the page shows.
- **Merging offers to delete in the same step:** Delete the branch once merged, checked by default, next to the Merge
  button. The Console merges, then deletes the branch at the head it merged; a branch that moved in between is kept, and
  the merge stands.
- **The deleted branch's page** says who deleted it and when, the head it was at, linked to that commit, and its merge,
  linked to `main`'s history. It reads the latest deletion of a branch of that name, `GET …/deleted-branches/{branch}`,
  since `GET …/branches/{branch}` answers only for a live one.
- **Later:** deleting merged branches automatically, a setting in `/cobranch.yaml` once its parser
  ([ENG-214](https://linear.app/arikkfir/issue/ENG-214)) lands; a list of recently deleted branches; restoring one at
  its head; deleting merged branches in bulk from the branch list.

## The Console

**The Console shows status where branches are listed, and why on each branch’s page**

![The Console shows status where branches are listed, and why on each branch’s page](branch-status/console.svg)

Branch names carry `/`; the Console takes the rest of the path after `branches/` as the name.

- **A repository's branches** show each branch's status badge, and how many commits it is ahead of and behind `main`; a
  panel names `main`'s merge requirements, from `/cobranch.yaml`.
- **A branch's page** says who wants to merge how many commits into `main`, why it is Blocked, requirement by
  requirement, its commits, and the annotations on its head. Its **Merge** button merges it for a principal with
  `repo.merge` once it is Ready, and offers a principal with `repo.bypass` to merge it anyway, by a merge commit or a
  fast-forward, whichever its base allows; it sends the head the page shows, so a branch that moved meanwhile isn't
  merged.
- **A commit's page** lists its annotations, each with its status, summary, link, principal and how long it ran, and
  offers to delete those the viewer may delete. History rows show their commits' annotations as icons.
- **The status language** is the design system's: one word, one icon, one tone for each branch and annotation status, as
  [the design system](design-system.md) lists them.

## APIs

| REST, under `/v1/orgs/{org}/repos/{repo}` | GraphQL | Takes |
| --- | --- | --- |
| `GET /commits/{sha}/annotations` | `Commit.annotations` | `repo.read` |
| `PUT /commits/{sha}/annotations/{name}` | `setAnnotation` | `annotations.write` |
| `DELETE /commits/{sha}/annotations/{name}` | `deleteAnnotation` | `annotations.write`, or `annotations.delete_any` for another's |
| `GET /branches`, `GET /branches/{branch}` | `Repository.branches`, `Repository.branch` | `repo.read` |
| `POST /branches/{branch}/merge` | `mergeBranch` | `repo.merge`, and `repo.bypass` unless Ready |
| `DELETE /branches/{branch}` | `deleteBranch` | `repo.push`, and `repo.bypass` for a branch `/cobranch.yaml` names |
| `GET /deleted-branches/{branch}` | `Repository.deletedBranch` | `repo.read` |

A name or a branch with a `/` travels escaped in a path, as `ci%2Fbuild`. A branch carries its base, status, checks,
ahead and behind counts, and its merge: who, when and which head; `main` carries its merge requirements instead. A
deleted branch carries the same, with when it was deleted and by whom.

## Data model

**This slice’s tables: annotations and merge requirements, and what branches and pushes gain**

![This slice’s tables: annotations and merge requirements, and what branches and pushes gain](branch-status/er.svg)

`annotations` carries trigger `audit()`; `merge_requirements` is a parsed copy of `/cobranch.yaml`, whose history is
Git’s. Every new row carries `org_id`.

## Rollout

**Four pull requests, each with its tests and its Console pages**

![Four pull requests, each with its tests and its Console pages](branch-status/rollout.svg)

Nothing changes for an operator: no chart value is added, and a repository opts into requirements through its own
`/cobranch.yaml`.

- **Each pull request keeps `main` working,** with its tests and its Console pages, and is Part of
  [ENG-199](https://linear.app/arikkfir/issue/ENG-199); the fourth closes it. Merges came apart into two, by a push and
  by asking Cobranch, to keep each one reviewable.
- **Upgrading needs nothing of the operator.** Branches pushed before the upgrade show Processing until their
  repository's next push works out their facts, and branches merged before it show Empty, since no merge of theirs was
  recorded.
- **A repository adopts requirements** by pushing a `/cobranch.yaml` with a `branches` key into `main`.
- **Deleting branches ([ENG-241](https://linear.app/arikkfir/issue/ENG-241)) comes in one pull request,** its APIs, the
  Git server's part and the Console's together. Branches deleted before it show no one as their deleter.

## Risks

- **Every push counts every branch.** `%(ahead-behind)` walks the graph once for all of them, so a repository with
  thousands of branches costs one walk per push, inside the push.
- **Requirements lock `main` against direct pushes.** That is the point; a Maintainer's `repo.bypass` is the way past,
  and every use of it is an event.
- **The push worker gates every status.** While none runs, branches stay Processing and nothing can merge but by bypass;
  an operator's autoscaler must keep it able to start, as for principals today.
- **Racing pushes refuse one of them.** A push into `main` and a push of any branch, or two into `main`, that record in
  the same moments refuse the later one, which pushes again; two pushes of other branches both go through.
- **A CI system that never reports** leaves a required annotation missing, so its branches stay Blocked; that is
  visible, branch by branch.

## Decisions

| Proposal | Why | Decision |
| --- | --- | --- |
| Annotations belong to the commit, one per name, changed only by their principal and deleted by them or `annotations.delete_any` | The catalog's two permissions; a verdict on a commit holds for every branch at it | Proposed |
| An annotation's version is its ETag: REST's `PUT` and `DELETE` require `If-Match`, or `If-None-Match: *` for a first set (428 without, 412 when it changed); GraphQL's `expectedVersion` is optional (CONFLICT) | The house rules' HTTP APIs require `If-Match`; GraphQL has no create-only form without another argument, and none of its mutations takes a version yet | Proposed |
| Setting or deleting an annotation appends the architecture's `annotation.changed`, its details naming which | The day-one event the architecture agreed; one kind for a job to follow | Agreed |
| Merge requirements come from the default branch's `/cobranch.yaml`, `branches.<name>.requires`, read by the Git server when a push moves `main`; a file that doesn't parse refuses the push | Settings live in Git; the requirements in effect always match the file | Proposed |
| `merge_requirements` is keyed by the branch's name, not its ID | A requirement may name a branch before it exists, and outlives one deleted and pushed again | Changed |
| A required annotation that is Pending or In progress makes the branch Processing, not Blocked | It is still running, as a job will be once [ENG-214](https://linear.app/arikkfir/issue/ENG-214) lands | Proposed |
| Being behind `main` blocks only a branch with commits to merge; Merged is recorded for a head when a merge brings it into `main` | Git can't tell a fast-forwarded branch from an empty one; a merged branch is never Blocked | Changed |
| The Git server counts ahead, behind and merge commits for every live branch on every push; the push worker computes the status, an annotation's own transaction recomputes it | The objects are only on the Git server; the agreed push worker owns what follows a push; a verdict shows at once | Proposed |
| A push recorded after another moved a branch it counted keeps only the facts still true, or is refused when `main` moved or it moves `main`; the Git server doesn't count again | The facts it counted against an old tip are wrong, and counting again needs the other push's objects, which the Git server doesn't hold | Changed |
| Every branch's base is `main` in this slice | No issue asks for another base yet; the column is there for one | Proposed |
| An API merge is a merge commit or a fast-forward, never a rebase; linear history merges by fast-forward | A Ready branch contains its base's tip, so a fast-forward always keeps history linear | Changed |
| A push into a branch with merge requirements must bring only Ready branches' commits, merges aside; `repo.bypass` skips it, with event `branch.bypassed` | The architecture's accepted pushes, now with the status to check | Agreed |
| Reviews, path rules, stacked branches and the Console's editing of `/cobranch.yaml` come later | Keeps this slice to its issue | Proposed |
| Deleting a branch through Cobranch is a push of its ref to nothing, which the Git server records for the requester, transport `api` | Whatever follows a deletion sees one kind of event; every ref change goes through the push's transaction | Proposed |
| It takes `repo.push`, and `repo.bypass` for a branch `/cobranch.yaml` names, and nothing more for someone else's branch | What `git push --delete` takes; the record names who did it | Proposed |
| `If-Match` names the branch's head, quoted, and is required; `*` deletes it wherever it is | A branch moves by its head; its status changes under the same head, so a tag of the whole branch would refuse a deletion each time the push worker finished | Proposed |
| Any branch but `main` may be deleted; the Console asks harder for one with commits `main` lacks | Git deletes unmerged branches too, and maintenance keeps their objects | Proposed |
| Merging and deleting stay two requests, which the Console makes in one step | Each answers on its own: a branch that moved after its merge is kept, and the merge stands | Proposed |
| A push keeps the IDs of the branches it merged, and the push worker records a merge on that row, deleted or not | A merge and a deletion asked together would otherwise lose the merge | Proposed |
| The deleted branch's page reads `GET …/deleted-branches/{branch}`, the latest deletion of that name | `GET …/branches/{branch}` keeps answering 404 for a branch that isn't live, as its clients expect | Proposed |
| Deleting merged branches automatically waits for `/cobranch.yaml`'s parser ([ENG-214](https://linear.app/arikkfir/issue/ENG-214)) | One parser for the file | Proposed |

## Open questions

1. **Is `main` every branch's base for now?** Recommended: yes. Choosing another base, for stacked branches, needs its
   own API and Console control; the column and the requirements keyed by name already allow it later.
2. **No rebase in Cobranch's merge?** Recommended: yes. A Ready branch always fast-forwards, so linear history needs no
   rebase; rebasing a branch onto its base can come as an action of its own.
3. **Does merging in the Console delete the branch by default?** Recommended: yes, the checkbox checked. Every commit of
   a merged branch is in `main`, so deleting it loses nothing, and a branch someone means to keep working on is one
   click away from staying.

Cobranch is a Kfirs project, created by Arik Kfir.
