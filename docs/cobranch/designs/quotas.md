# Cobranch quotas

> **Snapshot.** This design lives in its [quotas page](https://claude.ai/artifact/Uy8pkcdmvn5kp41xTTmne6), linked with
> Cobranch's other designs; this file is generated from it, with its diagrams, as of 2026-11-02, and synced again once
> Cobranch's shape settles.

**Status**: Proposed 2026-11-02 · [ENG-238](https://linear.app/arikkfir/issue/ENG-238) · Extends [the
Back-office](back-office.md#installation-settings), [organizations](sign-in-and-organizations.md) and [Git hosting’s
pushes](git-hosting.md#a-push) · Counts the packs [Git object storage](git-object-storage.md) keeps · Built by
[ENG-238](https://linear.app/arikkfir/issue/ENG-238), in three pull requests

Administrators cap what each user and organization may take: how many organizations a user creates, how many
repositories an organization holds, and how many bytes of Git data it stores. Cobranch checks each limit in the
transaction that would cross it, refuses with a message that names the limit, and stores nothing over it. Every
installation starts capped by built-in defaults, which administrators change in the Back-office, for the installation
and for any organization; the Console shows an organization its usage against its limits.

## Scope

What each user and organization may take of an installation ([ENG-238](https://linear.app/arikkfir/issue/ENG-238)).
Until now an installation open to sign-up, as [the Back-office's](back-office.md#installation-settings) defaults leave
it, lets anyone create organizations and repositories and push up to [the Git server's](git-hosting.md#a-push) per-push
maximum as often as they like, until the database fills and everyone's writes fail; an administrator's only lever was to
close sign-up or self-serve organizations for everyone. This design adds three limits, their checks and their refusals,
their settings in the Back-office, and an organization's usage in the Console.

**Three limits, each counted where it is checked, set by administrators**

![Three limits, each counted where it is checked, set by administrators](quotas/limits.svg)

A limit of 0 caps nothing. Nothing Cobranch already stores is ever deleted to meet a limit: a limit only refuses what
would cross it.

| Limit | Counts | Default | Overridden per organization |
| --- | --- | --- | --- |
| Organizations per user | The organizations a user created for themselves, in the Console or the APIs, that still exist | 5 | No |
| Repositories per organization | The organization's repositories | 100 | Yes |
| Git data per organization | The bytes of the active packs of the organization's repositories | 1 GiB | Yes |

- **A limit of 0 caps nothing,** as Git's own `receive.maxInputSize` reads 0. An administrator who wants no quotas sets
  each default to 0.
- **Every installation starts capped.** Cobranch's defaults open sign-up and self-serve organizations to anyone, so they
  cap what anyone takes, too: an installation that wants more raises the limits, and one that wants none sets them to 0.
- **What already exists stays.** A limit refuses only what would cross it: an organization above a lowered limit keeps
  every repository and byte it has, and pushes that delete still go through.
- **Usage is what Cobranch stores, not what a clone weighs.** Git data counts the packs as the object store keeps them,
  deltas compressed; compaction shrinks them, and a push of objects the repository already has adds little.

## Which limit applies

**Which limit applies: the organization’s own, else the installation’s, else the built-in default**

![Which limit applies: the organization’s own, else the installation’s, else the built-in default](quotas/resolve.svg)

An organization’s own limit wins over the values too: it is an administrator’s decision about that one organization.

- **An organization's own limit wins.** An administrator who gives one organization more, or less, decided about that
  organization; it overrides the installation's default wherever that default comes from.
- **The values lock what they set,** as they lock [the Back-office's other
  settings](back-office.md#installation-settings): `limits.organizationsPerUser`, `limits.repositoriesPerOrganization`
  and `limits.gitDataPerOrganization` in the chart's values, null by default, are shown read-only in the Back-office
  once set.
- **Each limit is resolved when it is checked,** from the organization's row, the values and the installation's
  settings, so a change applies to the next creation or push without a restart.

## A push

**A push is weighed before its pack is stored, and again in its transaction**

![A push is weighed before its pack is stored, and again in its transaction](quotas/push.svg)

A push that only deletes refs brings no pack and is never refused by a limit. Maintenance’s compaction replaces packs
and only ever shrinks Git data, so it is never weighed.

- **Weighed before it is stored.** Git has quarantined and indexed the push's pack on the Git server's disk when the
  pre-receive hook calls back; the Git server adds the pack's size to the organization's Git data and refuses the push
  before uploading anything when the sum crosses the limit.
- **Weighed again in the push's transaction,** under a lock on the organization's Git data that the transaction takes
  after the repository's, so two pushes into an organization's repositories at once can't both pass on the same reading.
  A push refused there leaves an uploaded pack that no row lists, which maintenance deletes a day later, as it deletes
  any failed push's.
- **A pack the repository lists already adds nothing:** a push that brings a pack of the same name keeps the one listed,
  as [Git hosting](git-hosting.md#a-push) records it.
- **Merges asked of Cobranch** are pushes too: the merge commit's pack is weighed in the transaction, and a merge over
  the limit is refused like a push.
- **Never weighed:** a push that only deletes refs, which brings no pack, and maintenance's compaction, which replaces
  packs with fewer, smaller ones.
- **What the client sees,** as `remote:` lines:
  `This organization stores 980 MiB of Git data, and this push adds 64 MiB: over its limit of 1 GiB. Nothing changed; an administrator can raise the limit.`

## Creating organizations and repositories

**Creating an organization or a repository counts under a lock, in the transaction that creates it**

![Creating an organization or a repository counts under a lock, in the transaction that creates it](quotas/create.svg)

An organization a user creates counts against them for as long as it exists. One an administrator creates for a user in
the Back-office counts against nobody and is never refused by the limit.

- **Counted under a lock, in the transaction that creates.** A repository's creation takes its organization's lock for
  the limit, and an organization's creation its creator's, transaction-scoped advisory locks, so two creations at once
  can't both pass on the same count.
- **Refused with 409,** problem codes `limit.repositories` and `limit.organizations`, whose detail names the limit:
  `This organization holds 100 repositories, the most it may hold. An administrator can raise its limit.` and
  `You created 5 organizations, the most a user may create here. An administrator can create more for you.` GraphQL
  refuses with `CONFLICT` and the same codes as reasons. The Console shows the detail where it shows any refusal.
- **The Back-office creates past the limit.** An organization an administrator creates for a user counts against nobody,
  so the limit stays a guard against self-serve sign-ups filling the installation, and an administrator who wants a user
  to have more creates it for them.

## Data model

**The limits are three settings, an organization’s own limits, and who created an organization**

![The limits are three settings, an organization’s own limits, and who created an organization](quotas/tables.svg)

Each limit is a non-negative number, 0 meaning none; null takes the next source down. Every change to a limit is
audited, and an organization’s own limits also land in its audit trail.

| Table | Change | Why |
| --- | --- | --- |
| `installation_settings` | `organizations_per_user`, `repositories_per_organization`, `git_data_per_organization`: `bigint`, null until set, 0 or more | The defaults, versioned and audited with the other settings |
| `organization_limits` | New: `org_id` (key, cascades), `repositories`, `git_data` (null takes the default), `version`, `updated_at`, `updated_by`; triggers `bump_version` and `audit()` | An organization's own limits; no row is no override |
| `organizations` | `created_by`: the user who created it for themselves, null for one an administrator created in the Back-office; filled from the audit log's `org.created` events | What organizations per user counts |
| `packs` | An index on `org_id` over active packs | Summing an organization's Git data in its push's transaction |

- **Counted when asked, never kept as a counter.** An organization's repositories and active packs are a few rows each,
  so a count or a sum is exact and costs an index scan; a counter would need every writer, maintenance included, to keep
  it right.
- **Changing an organization's own limits** is recorded as event `org.limits_changed` in its audit trail, so its Owners
  see what an administrator changed.

## The Back-office and the Admin API

**The Back-office sets the defaults on its settings page and an organization’s own limits from its list**

![The Back-office sets the defaults on its settings page and an organization’s own limits from its list](quotas/backoffice.svg)

Sizes are entered and shown in binary units (MiB, GiB), as the chart’s values write them.

| Operation | What it does | Takes |
| --- | --- | --- |
| `GET`, `PATCH /v1/settings` | Read and change the three defaults with the other settings, under the settings' ETag | `installation.manage` |
| `GET /v1/orgs` | Each organization gains its usage: repositories and Git data, each against the limit that applies | `installation.manage` |
| `GET /v1/orgs/{org}/limits` | The organization's own limits, the defaults, and the limits that apply, with its ETag | `installation.manage` |
| `PATCH /v1/orgs/{org}/limits` | Sets or clears (`null`) the organization's own limits, a JSON merge patch under `If-Match` | `installation.manage` |

## The Console

- **An organization's settings show its usage:** repositories and Git data, each against the limit that applies, with a
  meter that turns to a warning at 90% and to danger at the limit, and each repository's own Git data beside it. Anyone
  with `org.read` there sees it.
- **The APIs carry the same:** `GET /v1/orgs/{org}/usage` in REST and `Organization.usage` in GraphQL, `org.read`.
- **Creating an organization** shows how many the user has created against their limit, and the button gives way to the
  reason once they reach it.

## Rollout

**Three pull requests, the first of which caps every installation with the built-in defaults**

![Three pull requests, the first of which caps every installation with the built-in defaults](quotas/rollout.svg)

From the first pull request on, an organization that already holds more than a default keeps everything it has; only
what would add to it is refused until an administrator raises its limit.

- **The first pull request caps every installation** the moment it upgrades, with the built-in defaults, before the
  Back-office can change them, unless the chart's values set others; an organization already above a default keeps what
  it has, and its next push that adds objects is refused until an administrator raises its limit.
- **The migration fills `created_by`** from the audit log, so organizations users created before the upgrade count
  against them.
- **The chart's values come with the first pull request,** so an operator sees the limits in the values and can lift
  them, or set 0, without waiting for the Back-office; the second pull request shows them there as locked.

## Risks

- **The defaults refuse a large import.** A first push of a long history over 1 GiB, or an organization of more than 100
  repositories, is refused until an administrator raises the limit; the refusal names the limit and who can raise it.
- **Superseded packs aren't counted.** Compaction's old packs stay for a day before maintenance deletes them, so the
  store briefly holds more than the organizations' Git data; size the database, or the bucket, with room for that.
- **Deleting doesn't free Git data at once.** A branch deleted to get back under a limit frees its objects only when
  compaction next repacks the repository; an administrator can raise the limit meanwhile.
- **A refusal in the transaction leaves a pack behind** for a day, uploaded before the second weighing; only two pushes
  into one organization at the same moment, near its limit, get there.
- **No per-user storage limit.** A user's Git data is bounded by their organizations times each one's limit; a per-user
  limit, and limits on jobs, agents and other resources, come with the slices that add them.

## Decisions

| Proposal | Why | Decision |
| --- | --- | --- |
| Three limits: organizations per user, repositories and Git data per organization | [ENG-238](https://linear.app/arikkfir/issue/ENG-238) asks for them; each bounds what one sign-up can take | Proposed |
| Built-in defaults of 5 organizations, 100 repositories and 1 GiB, on from the upgrade | An installation open to anyone by default is capped by default, from the moment it upgrades | Proposed |
| 0 means no limit | As Git's `receive.maxInputSize` and other Git hosts read it; every limit stays one number | Proposed |
| An organization's own limit wins over the values' and the Back-office's defaults | It is an administrator's decision about that organization | Proposed |
| The chart's values lock the three defaults they set | As every other Back-office setting, so an installation kept in Git loses nothing | Proposed |
| Count Git data as the active packs' bytes, summed when asked | Exact, cheap with an index, and nothing else to keep in step | Proposed |
| Weigh a push before storing its pack, and again in its transaction under the organization's lock | Nothing over a limit is stored, and concurrent pushes can't both cross it | Proposed |
| Organizations created in the Back-office count against nobody | The limit guards self-serve; an administrator's creation is deliberate | Proposed |
| Refuse creations with 409 and codes `limit.repositories`, `limit.organizations` | The request conflicts with the organization's current state; the code tells clients which limit | Proposed |

## Open questions

1. **Are 5 organizations, 100 repositories and 1 GiB the right defaults?** Proposed as they are: small enough that one
   sign-up can't fill a default database volume, large enough that a team rarely meets them before an administrator has
   looked.
2. **Should a user's own limit be overridable,** as an organization's is? Proposed: not now; an administrator creates
   more organizations for a user in the Back-office instead.

Cobranch is a Kfirs project, created by Arik Kfir.
