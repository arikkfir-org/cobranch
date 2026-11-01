# Cobranch Git hosting

> **Snapshot.** This design lives in its [Git hosting page](https://claude.ai/artifact/Jv3DXEt77hRvrUe1R92QB2), linked
> with Cobranch's other designs; this file is generated from it, with its diagrams, as of 2026-11-01, and synced again
> once Cobranch's shape settles.

**Status**: Agreed 2026-11-01 · [ENG-198](https://linear.app/arikkfir/issue/ENG-198) · Builds the [architecture’s Git
storage and pushes](architecture.md#git-storage-and-pushes) and [principals and
permissions](principals-and-permissions.md) · Adds the Git server and the push worker to the chart · Built by
[ENG-198](https://linear.app/arikkfir/issue/ENG-198), in six pull requests

Organizations keep their code in Cobranch: a repository is packs in the bucket and refs in PostgreSQL, the Git server
serves clone, fetch and push over HTTPS and then SSH, and a push returns only once the Git server has recorded its refs,
commits and branches, so the Console shows it the moment Git returns; the push worker adds the principals behind them
after.

## Scope

How Cobranch hosts Git ([ENG-198](https://linear.app/arikkfir/issue/ENG-198)): repositories in organizations, the Git
server that serves clone, fetch and push over HTTPS and then SSH and records every push before the client returns, the
push worker that does what follows, the principals behind commits and pushes, and the Console's first view of code. The
[architecture](architecture.md#git-storage-and-pushes) already fixed the storage and the push path; this page builds
them, and names what it adds and changes.

**This slice hosts repositories and records every push; branch status, jobs and settings build on it**

![This slice hosts repositories and records every push; branch status, jobs and settings build on it](git-hosting/scope.svg)

Every later slice reads what a push records here: its commits and branches, written in the push’s one transaction, and
their principals, which the push worker adds after.

Choices beyond the agreed designs are collected under [Decisions](#decisions).

## Components

**Two workloads join: the Git server, which clients reach, and the push worker, which nobody calls**

![Two workloads join: the Git server, which clients reach, and the push worker, which nobody calls](git-hosting/components.svg)

Every workload runs image `cobranch` at the chart’s version, which already holds `git`. Neither new workload has a
Kubernetes token, and the push worker has no Service: the two share only PostgreSQL.

- **The Git server** (`cobranch git-server`) serves Git's smart HTTP protocol at `/<org>/<repo>.git`, and SSH in the
  last pull request. It reads PostgreSQL, keeps a pack cache on an ephemeral volume, reads and writes packs in the
  bucket, and runs the `git` CLI, which the `cobranch` image already holds. Two replicas, no Kubernetes token.
- **The push worker** (`cobranch push-worker`) claims each push the Git server recorded, from PostgreSQL, and does what
  can wait: in this slice, the principals behind its commits. Nobody calls it: no Service, no route, no bucket, no
  Kubernetes token. Two replicas, or none while no push waits under an operator's autoscaler.
- **No internal call.** The two share only PostgreSQL: a push's row is the push worker's task, and the push's
  transaction notifies the push workers, so the Git server never waits on one, and neither holds a secret of the
  other's. Changed from the architecture, whose push worker wrote every ref behind an internal call.
- **The Git server serves as if it were alone,** at `/`; the operator mounts it at a host or a path prefix, which
  `urls.git` names. Git clients carry no cookie, so the Git server's URL needn't share the others' parent domain and
  doesn't take part in naming the session cookie.

## Repositories

A repository belongs to one organization, which owns its rows, packs and refs. It has a slug for its URLs, a name and a
description. Its default branch is `main`, in every repository, and nobody changes it.

**A repository starts empty and private; its first push gives it refs**

![A repository starts empty and private; its first push gives it refs](git-hosting/lifecycle.svg)

A rename, in either state, keeps the old slug as a redirect until another repository of the organization takes it.

- **Creating one** takes `repos.create` at the organization. Its creator becomes its Maintainer, so it starts
  [Private](principals-and-permissions.md#visibility): only the creator and the organization's Owners reach it.
- **Slugs** are 1 to 100 lowercase letters, digits, hyphens, dots and underscores, starting with a letter or digit and
  not ending in `.git`, and may not be one of the Console's pages under an organization (`members`, `roles`, `api-keys`,
  `settings`, `repos`, `new`). A rename keeps the old slug as a redirect, in the API, the Console and Git's URLs alike,
  until another repository of the organization takes it.
- **Empty until pushed.** A new repository has no refs and no packs; cloning it gives an empty repository. Git's `HEAD`
  names `refs/heads/main` from the start, whatever the first push creates.
- **SHA-1 for now.** The schema keeps SHAs as `bytea` and each repository's object format, so SHA-256 repositories
  arrive without a migration of the data.
- **Deleting one** comes later, with its packs' clean-up.

### Who reaches a repository

**A repository check reads its own grants and the viewer’s organization roles, narrowed by a personal token**

![A repository check reads its own grants and the viewer’s organization roles, narrowed by a personal token](git-hosting/access.svg)

The same check serves REST, GraphQL, Git over HTTPS and SSH. A list of repositories asks which of an organization’s the
principal reads, in one query too.

- **A grant reaches its scope and everything inside it**
  ([principals](principals-and-permissions.md#scopes-and-grants)): a repository's permissions are those its own grants
  give, and the repository permissions of the viewer's roles at its organization. The authorizer reads both in one
  query, so an Owner reaches every repository and a Member only those granted to them.
- **Repository grants** give a built-in repository role (Reader, Developer, Maintainer, Automation) or one of the
  organization's custom roles, whose repository permissions apply there. Granting takes `org.grants.manage` and every
  permission of the role ([grants](sign-in-and-organizations.md#roles-and-grants)); members and the organization's API
  keys receive them.
- **Visibility is a label** the API infers from grants: Private, or Internal once a user besides the creator and the
  Owners holds a grant that reaches it. Public needs grants to Anonymous, which come later with the settings that refuse
  them.
- **Lists show only what the viewer reaches.** The authorizer answers which of an organization's repositories a
  principal holds `repo.read` on, all of them when an organization role gives it.

## Personal tokens

A Git client over HTTPS authenticates with a password, and Cobranch stores none: a user makes a **personal token**,
`cbp_…`, which acts as them ([API keys and personal
tokens](principals-and-permissions.md#api-keys-and-personal-tokens)).

**A personal token is made from a session, shown once, and stands in for a password**

![A personal token is made from a session, shown once, and stands in for a password](git-hosting/token.svg)

The token’s page lists each token’s name, hint, permissions, last use and expiry, and revokes it. REST and GraphQL take
it as a bearer token too.

- **Made from a session.** Only a signed-in user makes one, in the Console, never another token or an API key; the token
  keeps the identity of that session, for the organizations'
  [providers](principals-and-permissions.md#sign-in-and-sign-up).
- **Narrowed to permissions.** A token lists the permissions it may use, `repo.read` and `repo.push` by default; a check
  under the token holds only what both the user's grants and the token's list give. Narrowing to organizations or
  repositories comes later.
- **Must expire.** 30 days by default, a year at most, as the [lifetimes](principals-and-permissions.md#lifetimes) say;
  revoking one stops it on the next request.
- **The same shape** as an API key's secret: the prefix, 32 random bytes in base62 and a CRC32 checksum, kept as its
  SHA-256 and last four characters ([API keys](sign-in-and-organizations.md#api-keys)). REST and GraphQL take it as a
  bearer token too.

## Git over HTTPS

- **Basic or bearer.** A token as the password, with any user name, or as `Authorization: Bearer`. A personal token acts
  as its user, an API key's secret as the key. The Git server never reads the session cookie, so no page can push for
  its visitor.
- **401, 404, 403.** A request without a credential gets `401` and a Basic challenge for every repository, so Git asks
  for one and nobody learns what exists; a credential that can't see the repository gets `404`; one that reads it but
  may not push gets `403`.
- **Protocol v2 and v0.** `Git-Protocol` passes through to `git upload-pack`; smart HTTP only, no dumb protocol.
- **Limits.** A push's pack is capped by `gitServer.maxPushSize` (2 GiB by default), and `git` checks every object it
  receives.

### A fetch

**A fetch serves a throwaway repository built from one snapshot over the pod’s pack cache**

![A fetch serves a throwaway repository built from one snapshot over the pod’s pack cache](git-hosting/fetch.svg)

The two requests may reach two pods and two snapshots: any SHA may be wanted, and packs outlive their last listing by a
day, so the second serves what the first advertised.

- **One snapshot.** The refs and the pack list are read in one statement, so a fetch never sees a ref without the pack
  that holds it ([a fetch](architecture.md#a-fetch)).
- **Hard links, not alternates.** The throwaway repository links the snapshot's packs from the cache by hard link,
  rather than pointing `objects/info/alternates` at the cache. Changed from the architecture: the cache can then evict
  any file at any time, and a fetch in flight keeps the packs it linked.
- **Any SHA in a want.** Smart HTTP's ref advertisement and its fetch are two requests, which may reach two pods and two
  snapshots; `uploadpack.allowAnySHA1InWant` lets the second serve what the first advertised, as packs stay a day after
  nobody lists them.

### A push

**A push returns once the Git server has committed it; the push worker does the rest later**

![A push returns once the Git server has committed it; the push worker does the rest later](git-hosting/push.svg)

The advertisement before the push, a `GET …/info/refs?service=git-receive-pack`, runs as a fetch’s does. A stale ref or
a failure makes the hook exit 1 with a `remote:` line saying why, and nothing changes. A store call that failed, maybe
after it committed, is asked again with the same push ID, which records nothing twice.

- **Permission first.** `repo.push` is checked before a byte of the pack is read.
- **Git quarantines and indexes,** with `receive.unpackLimit` 1 so every push keeps its objects as one pack,
  `index-pack --fix-thin` completing a thin one, and `receive.autogc` off.
- **The hook calls home.** The `pre-receive` hook is `cobranch git-hook`, which posts its standard input to the Git
  server process that started it, on a loopback port with a token made for this push, and streams back the answer as
  `remote:` lines. The manifest, the upload, the push's transaction and its trace all stay in the Git server, whose
  configuration and credentials the hook never holds.
- **The manifest** holds the ref updates and every new commit: `git rev-list` of the new tips not reachable from any
  existing ref, each commit read with `git cat-file` and parsed in Go, so authors, committers, times with their zones,
  messages and signatures arrive whole. Annotated tags are refs to tag objects; their commits come in like any other.
- **The Git server's one transaction** locks the repository's row, so pushes into one repository apply one at a time;
  checks every ref's old SHA; lists the pack; inserts the push, waiting for the push worker, and its new commits; moves
  the refs; creates, moves and deletes branches; appends event `repo.pushed`; and notifies `cobranch_pushes`. A push is
  recorded once by its ID: a store call that failed, maybe after it committed, is asked again with the same ID, up to
  four times, and a push already recorded is accepted as it was ([failure modes](architecture.md#failure-modes)).
- **Then the push worker,** in a transaction of its own: it claims the push that has waited longest,
  `FOR UPDATE SKIP LOCKED`, so two replicas never take one push; maps its commits' authors and committers to principals;
  and marks it processed. A push it fails keeps its error and waits for another try, a second after the first failure
  and twice as long after each, at most an hour. `LISTEN` wakes it, and a poll every 30 seconds catches a lost
  notification; its span links the push's trace, whose `traceparent` the push's row keeps. Until then a commit names its
  author's email but no principal.

### Which pushes are accepted

**A push is accepted whole, or refused whole with the reason**

![A push is accepted whole, or refused whole with the reason](git-hosting/accept.svg)

Merge requirements and path rules join this flow with branch status
([ENG-199](https://linear.app/arikkfir/issue/ENG-199)) and settings.

- **Branches and tags only.** A push may update `refs/heads/*` and `refs/tags/*` and nothing else, and none under
  `cobranch/`, which [only Cobranch writes](architecture.md#which-pushes-are-accepted).
- **Atomic.** One hook decides for the whole push, so every ref moves or none does, as `git push --atomic` would.
- **Merge requirements and path rules** come with branch status ([ENG-199](https://linear.app/arikkfir/issue/ENG-199))
  and settings; until then any principal with `repo.push` moves any branch, `repo.bypass` included or not.

| The client sees | When |
| --- | --- |
| `refs/heads/main moved since you fetched: fetch, merge or rebase, then push again` | Another push moved a ref first |
| `Cobranch writes refs under cobranch/ itself` | A ref under `refs/heads/cobranch/` or `refs/tags/cobranch/` |
| `Cobranch keeps branches and tags only: refs/notes/commits refused` | Any other ref |
| `Cobranch could not record the push; nothing changed, so try again` | The bucket or the database failed |

## Principals behind a push

**Every author and committer becomes a principal, a login-less user when nobody holds the email**

![Every author and committer becomes a principal, a login-less user when nobody holds the email](git-hosting/mapping.svg)

The commit keeps the name and email as written beside its principals. The pusher is never inferred: it is whoever
authenticated.

- **The pusher** is whoever authenticated: a user through their personal token, or an API key. Every push row names it.
- **Authors and committers by email.** The push worker resolves each commit's author and committer by lowercased email,
  after the push: a user who holds it, verified, or else the login-less user who holds it, or else a new **login-less
  user**, named after the commit, holding the email unverified
  ([emails](principals-and-permissions.md#emails-and-login-less-users)). A login-less user is a user with no identity:
  it can't sign in and holds no grants. Commits keep the names and emails as written too.
- **Sign-up adopts a login-less user.** **Proposed:** a new identity whose verified email a login-less user holds signs
  in as that user, the email now verified, so their commits are theirs at once and nothing is rewritten. A user who
  already exists and later verifies such an email claims it through the background task the principals design describes,
  once users hold more than one email.
- **Signatures are kept, not checked yet.** A commit's signature is stored as written; Verified comes with signing keys.

## The Console

**The Console gains a repository’s pages under its organization, and the viewer’s personal tokens**

![The Console gains a repository’s pages under its organization, and the viewer’s personal tokens](git-hosting/console.svg)

An organization’s own pages (members, roles, API keys, settings) keep their paths, which no repository’s slug may take.

- **An organization's home** lists the repositories the viewer reaches, and offers a new one to members with
  `repos.create`.
- **A repository's page** shows its clone URL with how to make a personal token, its branches, and the default branch's
  history: first-parent, from PostgreSQL, a page at a time. Its **Access** page lists and changes its grants; its
  **Settings** page renames it.
- **A commit's page** shows its message, author and committer with the principals behind them, its parents, and the push
  that first brought it, with its pusher.
- **As soon as the push returns.** The push's transaction committed before Git returned, so the next page load shows it;
  its commits' principals follow once the push worker has processed it. Live updates of an open page come with the web
  app's build-out, over the NOTIFY the architecture describes.
- **Personal tokens** have a page under the viewer's own settings: make, list and revoke.

## Storage and clean-up

**A pack is written once; compaction supersedes many with one, and maintenance deletes what nobody lists**

![A pack is written once; compaction supersedes many with one, and maintenance deletes what nobody lists](git-hosting/packs.svg)

Compaction builds its pack from one snapshot and supersedes only that snapshot’s packs, so packs pushed meanwhile stay
active and every listed ref keeps its objects.

- **Compaction.** The hourly maintenance job repacks a repository whose active packs pass a threshold (8 by default): it
  fetches them into a throwaway repository with the snapshot's refs, runs `git repack -a -d`, uploads the new pack, and
  in one transaction lists it and marks the snapshot's packs superseded. Packs pushed meanwhile stay active, untouched.
- **Deletion a day later.** Maintenance deletes a superseded pack's files a day after it was superseded, and files under
  `git/<repo-id>/` that no row lists a day after they were written: the packs of failed pushes.
- **The cache** is an `emptyDir` of `gitServer.cache.size`, evicting the least recently used repositories' packs first.

## SSH

**SSH reaches the same pipeline: a key names its user, and the operator exposes the port**

![SSH reaches the same pipeline: a key names its user, and the operator exposes the port](git-hosting/ssh.svg)

A user adds SSH keys in the Console; a key acts as its user, as a session would.

- **The same pipeline.** The Git server also listens for SSH, as user `git`, and runs `git-upload-pack` and
  `git-receive-pack` for `<org>/<repo>.git` through the same snapshot, throwaway repository, hook and transaction.
- **Keys are a user's.** Users add SSH public keys in the Console; a key acts as its user, unnarrowed, as a session
  would; a fingerprint belongs to one user.
- **Exposure is the operator's.** The chart renders a Service port for SSH and keeps the host key in a Secret it
  generates, or the operator's; a load balancer, a node port or a TCP route, and the host name, are the operator's
  choice.

## Configuration

| Value | What it does |
| --- | --- |
| `urls.git` | Where Git clients reach the Git server, a host or a path prefix; the Console shows clone URLs under it |
| `gitServer.replicas`, `pushWorker.replicas` | Two each; `pushWorker.replicas` null leaves the push worker's count to an autoscaler, which may scale it to zero while no push waits |
| `gitServer.cache.size` | The pack cache's `emptyDir` size limit, 10 GiB by default |
| `gitServer.maxPushSize` | The largest pack a push may send, 2 GiB by default |
| `gitServer.ssh.enabled`, `.port`, `.service`, `.hostKey.existingSecret` | SSH, from its pull request: off by default, then the Service the operator exposes and the host key |
| `maintenance.compaction.packs` | The active packs that make a repository due for compaction, 8 by default |

## Previews and tests

- **Seeds** add a repository to the demo organization, its Maintainer a seeded user, so a demo installation shows one at
  once; pushing into it is the demo's own act. Seeded users make personal tokens like anyone.
- **End to end with the real `git`.** Go tests start the Git server and the push worker in process over PostgreSQL and a
  `file://` bucket, and drive `git clone`, `fetch` and `push` against them: an empty clone, a first push, a stale push
  refused, two concurrent pushes into one repository, a lost answer asked again, a refused ref, and a fetch across a
  compaction.
- **The chart in CI** installs both workloads in kind, as it does the others, and its test pod clones an empty
  repository, pushes to it, and finds the push in a fresh clone.

## Data model

**This slice’s tables: repositories and their Git data, and personal tokens among credentials**

![This slice’s tables: repositories and their Git data, and personal tokens among credentials](git-hosting/er.svg)

Every new table carries `org_id`. Repositories and their slugs carry trigger `audit()`; a push’s rows are their own
record, named by the push and its pusher.

## Rollout

**Six pull requests in Cobranch, each one usable on its own**

![Six pull requests in Cobranch, each one usable on its own](git-hosting/rollout.svg)

Nothing of an installation is in the product: an operator routes the Git server and exposes SSH through the chart alone.

- **Each pull request keeps `main` working,** with its tests and its Console pages, and is Part of
  [ENG-198](https://linear.app/arikkfir/issue/ENG-198); the last closes it.
- **An installation is the operator's,** configured through the chart alone: `urls.git` and a route to the Git server
  from the third pull request, an autoscaler for the push worker if it likes, and the SSH Service's exposure from the
  sixth.

## Risks

- **A large first push is one transaction.** Importing a long history inserts every commit at once; push limits beyond
  the pack's size come later, as the architecture deferred them.
- **The Git server runs `git` on what clients send.** It checks every object, caps the pack, runs as a non-root user
  with no Kubernetes token, and cleans each throwaway repository when its request ends.
- **First-parent history** hides a merge's second line in the Console's list until the full graph view arrives.
- **Login-less users accumulate,** one per unknown email; they hold nothing and sign in nowhere, so they cost only rows.
- **Principals lag a push** while no push worker runs, or while its pushes fail; the commits and branches are there, and
  each push's error and attempts say why. An operator's autoscaler counts
  `SELECT count(*) FROM pushes WHERE processed_at IS NULL AND retry_at <= now()`.

## Decisions

| Proposal | Why | Decision |
| --- | --- | --- |
| Two workloads join: the Git server, two replicas with a pack cache, and the push worker, which nobody calls | Only the Git server is exposed, and only it is on a push's path | Agreed |
| Every repository's default branch is `main`, and nobody changes it | One name for the branch whose settings, triggers and reviewer instructions count | Agreed |
| A repository's permissions are its grants' and the repository permissions of the viewer's organization roles, read in one query | A grant reaches everything inside its scope; Owners reach every repository | Agreed |
| Repository slugs keep old ones as redirects, in Git's URLs too, and avoid the Console's organization pages | Renames break no clone; one URL space for the Console | Agreed |
| Personal tokens `cbp_` list permissions, expire within a year, and are made only from a session | HTTPS needs a password; a token holds no more than it must | Agreed |
| Git over HTTPS takes a token as a Basic password or a bearer, never the cookie; `401` without a credential, `404` without read | Git asks for credentials; no page pushes for its visitor; nobody learns what exists | Agreed |
| The pre-receive hook posts to its own Git server process on loopback, which builds the manifest and records the push | One process holds the credentials, the configuration and the trace | Agreed |
| Throwaway repositories hard-link the snapshot's packs from the cache instead of using alternates | The cache evicts any time without counting readers | Changed |
| Pushes may write `refs/heads/*` and `refs/tags/*` only, none under `cobranch/`, all or nothing | Branches and tags are what Cobranch models | Agreed |
| The Git server commits each push in one transaction that locks the repository's row and moves refs by compare-and-swap, recording it once by its ID; the push worker claims it later from PostgreSQL, woken by NOTIFY | The push worker leaves the push's path and may scale to zero; a lost answer is asked again; no message broker | Changed |
| Unknown authors become login-less users holding their email unverified; a new identity with that email verified adopts the login-less user | Commits have principals at once, and become their author's without rewriting | Agreed |
| The Console shows first-parent history from PostgreSQL; live updates come with the web app | The push is recorded before Git returns; the graph view can wait | Agreed |
| Maintenance compacts past 8 active packs and deletes unlisted and superseded files a day later | Every push adds a pack; failed pushes leave files | Agreed |
| SSH keys act as their user unnarrowed; the operator exposes the SSH Service | A key is a session's equal; exposure differs per cluster | Agreed |
| Deleting repositories, public repositories, signature checks, merge requirements, path rules and `/cobranch.yaml` come later | Keeps this slice to its issue; [ENG-199](https://linear.app/arikkfir/issue/ENG-199) and settings follow | Agreed |

## Open questions

1. **Personal tokens narrowed to permissions only, for now?** Settled on 2026-11-01: yes, with organization and
   repository scopes later, since a Git token's common case is `repo.read` and `repo.push` everywhere its user reaches.
2. **First-parent history in the Console for now?** Settled on 2026-11-01: yes; the full graph needs the Git server to
   walk history, which belongs with the code browsing that follows.
3. **Does the push worker stay on the push's path?** Settled on 2026-11-01: no. A synchronous call kept the push worker
   from scaling to zero and failed every push while it was down, so the Git server commits the push's refs, pack and
   commits itself, and the push worker claims the push later from PostgreSQL. Only what can wait (authors' principals,
   then branch status, events and jobs) waits for it.

Cobranch is a Kfirs project, created by Arik Kfir.
