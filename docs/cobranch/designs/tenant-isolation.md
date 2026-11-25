# Cobranch tenant isolation

> **Snapshot.** This design lives in its [tenant isolation page](https://claude.ai/artifact/UAzVu9fZgwdA55TA2rFVHJ),
> linked with Cobranch's other designs; this file is generated from it, with its diagrams, as of 2026-11-25, and synced
> again once Cobranch's shape settles.

**Status**: Proposed 2026-11-25 · [ENG-268](https://linear.app/arikkfir/issue/ENG-268) · Replaces “a later hardening” in
the [architecture’s data model](architecture.md#data-model) · Built on the stores’ `transact` and the [audit
trail’s](sign-in-and-organizations.md#data-model) session variable · No change to the [Helm
chart](https://claude.ai/artifact/LyMHXgSoNQNgHtHNfEjvA8)

PostgreSQL itself keeps organizations apart. Every query runs in a scope; a scope that names an organization runs as a
role whose row-level security policies show it that organization’s rows alone, whatever the query asks for. Work that
spans organizations says so where it runs.

## Scope

How PostgreSQL keeps one organization's rows from another's ([ENG-268](https://linear.app/arikkfir/issue/ENG-268)).
Until now only each query's own `org_id` filter did: a query that forgot it, or took the wrong organization, read or
wrote another organization's rows and nothing caught it. The [architecture](architecture.md#data-model) called row-level
security a later hardening; this is it.

**Two layers keep organizations apart: the query’s own filter, and under it PostgreSQL’s policy**

![Two layers keep organizations apart: the query’s own filter, and under it PostgreSQL’s policy](tenant-isolation/layers.svg)

The queries keep their `org_id` filters; the policy holds whatever a query forgets or gets wrong.

- **Two layers.** The queries keep their filters. Under them, every table that carries `org_id` has a row-level security
  policy, so PostgreSQL refuses what a filter misses.
- **Every query runs in a scope.** A store reaches the database only through `DB`, whose scopes say whose rows the work
  may reach. No store holds the pool or a bare query handle; a test fails one that does.
- **Work that spans organizations says so.** The controller's queues, the push worker, maintenance, the Admin API and
  sign-in name scope `Everywhere` with their reason, right where they run.
- **Nothing for the operator to do.** No second credential, no chart value: Cobranch connects as it does today.

Choices are collected under [Decisions](#decisions).

## A pinned query

**A pinned query is one round trip: the pin and the query travel together**

![A pinned query is one round trip: the pin and the query travel together](tenant-isolation/read.svg)

`Transact` begins the same way for several queries: `BEGIN` and the pin in one statement, then the queries, then
`COMMIT`.

- **The pin is one function.** `pin_organization(org)` sets session variable `cobranch.org_id` and switches the
  transaction to role `cobranch_tenant`, both local to the transaction, as the audit trail's `cobranch.principal_id` is.
- **The policy reads it.** `current_org()` returns the pinned organization; each policy is
  `org_id = (SELECT current_org())`, for reading and for writing. A transaction that pinned nothing has none, and
  matches no row.
- **One round trip.** Outside `Transact`, a scope sends the pin and the query as one batch, which PostgreSQL runs as one
  transaction. Measured against a local PostgreSQL 18: a pinned read of one row takes about 0.29 ms where the unpinned
  one takes 0.19 ms.
- **Nothing lingers on the connection.** Role and organization end with the transaction, so the next borrower of a
  pooled connection starts as the connecting role with no organization. A test reads every connection after pinned
  queries to check.

## Scopes

**Which scope a store method names**

![Which scope a store method names](tenant-isolation/scopes.svg)

A transaction pinned to one organization refuses work in another, and work that spans them: it would find less than it
asked for.

| Scope | Runs as | Reaches | Used by |
| --- | --- | --- | --- |
| `In(orgID)` | `cobranch_tenant` | That organization's rows | Store methods given their organization |
| `Of(table, id)` | `cobranch_tenant` | The rows of the organization that owns the row | Methods given a repository, an issue, a task, a secret, an artifact |
| `Deferred()` | The first work's | What the first work within it pins | `Transact` of the issues' and chats' stores, which join other stores' writes |
| `Everywhere(why)` | The connecting role | Every organization's rows | The controller's queues, the push worker, maintenance, the Admin API, sign-in and credentials, a person's own things, the object store |

- **`Of` looks the organization up first.** `pin_organization_of(table, id)` reads the row's `org_id` and pins it. A row
  that isn't there pins no organization, so the work finds nothing and answers "not found", as it did.
- **Transactions nest as before.** A store method called inside another's transaction joins it, in a savepoint. A
  transaction pinned to an organization admits work in that organization or in a row's, which the policies hold to it;
  it refuses work in another organization and work that spans them, since either would silently find less than it asked
  for.
- **A person's own things span organizations.** Listing my organizations, my invitations, my tokens and my artifacts
  reads several organizations' rows for one person, so it runs `Everywhere`, filtered by the person.
- **Finding an organization comes before pinning it.** A slug, a repository's path or a token's hash is looked up
  `Everywhere`; what follows is pinned.

## Roles and policies

**One credential, two roles: the role Cobranch connects as lends `cobranch_tenant` its place a transaction at a time**

![One credential, two roles: the role Cobranch connects as lends cobranch_tenant its place a transaction at a time](tenant-isolation/roles.svg)

The chart, its values and the operator’s Secret stay as they are. An operator whose role may not create roles creates
`cobranch_tenant` once by hand, as the migration’s error says.

| Table | Policy `organization` |
| --- | --- |
| The 39 tables whose rows always have an organization | Reads and writes rows where `org_id = current_org()` |
| `principals`, `roles`, `events`, whose rows may have none | Also reads rows without an organization (a person, a built-in role); writes only its own |
| `organizations` | Reads and writes the row whose `id = current_org()` |

- **The connecting role isn't bound.** It owns the tables, and the policies name `cobranch_tenant` alone, so migrations,
  seeds and `Everywhere` work as before. It may be a superuser, as the chart's own PostgreSQL makes it.
- **`cobranch_tenant` can't log in.** It holds `SELECT`, `INSERT`, `UPDATE` and `DELETE` on the tables, now and by
  default on later ones, and nothing else. The connecting role is a member of it, or a superuser.
- **Foreign keys still cascade.** PostgreSQL runs cascades and referential checks outside row-level security, so
  deleting an organization's row inside its own scope takes its rows with it.
- **A new table gets its policy in its migration:** `SELECT isolate_organization('<table>');`. A test migrates a fresh
  database and fails for any table with `org_id` whose policy is missing or differs.
- **Tables without `org_id` have no policy:** `grants`, `credentials`, and the rows that hang off a parent
  (`issue_labels`, `agent_task_entries`, `job_run_steps`, the tokens and counters). They are reached through a parent
  that has one.

## Decisions

| Decision | Why | Rejected |
| --- | --- | --- |
| The store names the scope | 319 store methods, 72 given their organization; stores, their tests and the services' fakes stay as they are | A scope carried in the request's context: GraphQL resolves several organizations in one request, and every by-ID lookup runs before any is known |
| One credential, `SET LOCAL ROLE` | The chart's PostgreSQL makes Cobranch's role a superuser, which no policy binds; switching role inside the transaction binds it with nothing for the operator to change | A second login role for the servers: a new credential in every installation. `FORCE ROW LEVEL SECURITY` on one role: still bypassed by a superuser |
| Spanning work runs as the owner | It is explicit, greppable and reviewed where it runs | A session flag the policies honour: one more thing every policy must get right |
| Pin and query in one batch | A read stays one round trip | `BEGIN`, pin, query, `COMMIT` for every read: four |
| Policies on `org_id` tables only | The issue's bound; children are reached through parents | A policy on each child through its parent: a subquery on every row read, for later if a leak there shows up |
| The filters stay for now | Queries that also run `Everywhere` need them; the rest drop them once the policies are live (owner, 2026-11-25) | Dropping them with the policies: one layer from day one |

## Security and failure modes

| What goes wrong | What happens |
| --- | --- |
| A query forgets its `org_id` filter, or a join strays | Pinned: it reaches only the pinned organization's rows. `Everywhere`: nothing catches it; those call sites are the ones to review |
| A store pins the wrong organization | The work finds nothing of the organization it meant, and fails as "not found" |
| A write names another organization | PostgreSQL refuses the row (`42501`) and the transaction rolls back |
| A new table lacks its policy | The build fails in CI |
| Role `cobranch_tenant` is missing | The servers aren't ready until the migration that creates it has run; a role that may not create roles gets an error naming the two statements to run |
| An attacker runs SQL of their own | Out of scope: the connecting role can leave the policies. This guards against Cobranch's own mistakes |

## Rollout

**Three pull requests: the scopes, the policies, then the filters the policies made redundant**

![Three pull requests: the scopes, the policies, then the filters the policies made redundant](tenant-isolation/rollout.svg)

Going back is the second migration’s Down: the policies go, and the scopes keep running as `cobranch_tenant` with
nothing refused.

- **No manual step** on an installation whose role may create roles, which the chart's own PostgreSQL and the managed
  services' administrators may.
- **Rolling upgrades hold.** The first migration only adds a role and grants, which the previous release's pods ignore.
  The second adds policies that bind only `cobranch_tenant`, which only pods of the first pull request and later use.
- **Tests run bound.** The stores' tests run every pinned query as `cobranch_tenant`, so a scope that pins the wrong
  organization fails the test of the method that names it.

## Open questions

- **Which queries drop their filters** in the third pull request: those no `Everywhere` caller uses. The list is drawn
  when the policies are live.
- **Children without `org_id`:** whether any deserves a policy through its parent.
- **The trace in the session**, which the house rules ask the wrapper to set too: nothing reads it yet, so it waits for
  the audit trail to want it.

Cobranch is a Kfirs project, created by Arik Kfir.
