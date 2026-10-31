# Cobranch Back-office

> **Snapshot.** This design lives in its [Back-office page](https://claude.ai/artifact/Eer3q2KsCAMzT2pXWPKzt1), linked
> with Cobranch's other designs; this file is generated from it, with its diagrams, as of 2026-10-31, and synced again
> once Cobranch's shape settles.

**Status**: Proposed 2026-10-31 · [ENG-219](https://linear.app/arikkfir/issue/ENG-219) · Builds on [principals and
permissions](principals-and-permissions.md) and [sign-in and organizations](sign-in-and-organizations.md) · Adds the
Admin server and the Back-office to the [architecture](architecture.md#components) · Built by
[ENG-219](https://linear.app/arikkfir/issue/ENG-219), in three pull requests

An installation’s administrators run it from the Back-office, a web app on the Admin server’s API: they list and create
organizations, list users, change the installation’s settings and identity providers, and enter an organization when
they must, on its record. It starts small and grows with the product, each page asking the same authorizer as everything
else.

## Scope

How an installation's administrators run it ([ENG-219](https://linear.app/arikkfir/issue/ENG-219)): the Admin server and
its API, which the [architecture](architecture.md#api-conventions) set aside for the Back-office, and the Back-office's
first pages. It builds on [principals and permissions](principals-and-permissions.md), which decided that administrators
hold ordinary permissions and see no organization's content, and on [sign-in and
organizations](sign-in-and-organizations.md), whose sign-in, organizations and grants it administers.

**This slice builds the Back-office’s first pages; the rest of an operator’s work waits for the slices that need it**

![This slice builds the Back-office’s first pages; the rest of an operator’s work waits for the slices that need it](back-office/scope.svg)

Each page asks the same authorizer as the Console’s, so later pages add operations, not a new model.

Choices beyond those designs are collected under [Decisions](#decisions), all Proposed but one, agreed on 2026-10-31:
the Back-office adds identity providers beside the chart's values, which settles a conflict between
[ENG-219](https://linear.app/arikkfir/issue/ENG-219) and the agreed sign-in design ([identity
providers](#identity-providers)).

## Components

**Two workloads join: the Admin server and the Back-office, which the operator may keep internal**

![Two workloads join: the Admin server and the Back-office, which the operator may keep internal](back-office/components.svg)

The Console and the GraphQL and REST servers don’t change, except that the session cookie now names the Admin server’s
and the Back-office’s URLs too.

- **The Admin server** (`cobranch admin-server`) serves the Admin API under `/v1/`, from a document of its own,
  `api/admin.yaml`, as the REST server serves `api/openapi.yaml`. It joins the chart as a Deployment of one replica with
  no Kubernetes token ([deployment](architecture.md#deployment)), over the same service layer and authorizer as the
  other APIs.
- **The Back-office** is static pages that Caddy serves from image `cobranch-back-office`, as the Console's are: its own
  origin, and a `config.json` naming where the Admin and Auth servers answer. Until the web apps' build-out it is one
  page of plain JavaScript, as the Console still is.
- **Each serves as if it were alone,** the Admin server's paths starting with `/v1/` and the Back-office's at `/`; the
  operator mounts each at a host or at a path prefix the route strips ([routes](architecture.md#routes)). Both can stay
  on a gateway only operators reach. `urls.admin` and `urls.backOffice` name them, and join the URLs the session cookie
  is named from.

## Sign-in

**The Back-office signs in through the Auth server and returns to itself; one cookie serves both apps**

![The Back-office signs in through the Auth server and returns to itself; one cookie serves both apps](back-office/sign-in.svg)

A seeded installation offers its seeded users here too, as the Console’s sign-in page does.

- **The same sign-in.** The Back-office sends people to the Auth server as the Console does. The return may now be a
  path of the Console's URL, as before, or a URL under the Back-office's; anything else returns to the Console's home.
- **One cookie.** The session cookie is set for the nearest common parent of every Cobranch URL, the two new ones
  included, so one sign-in serves the Console and the Back-office; with every workload on one host it stays a `__Host-`
  cookie ([sessions](sign-in-and-organizations.md#sessions)).
- **Mutations from the Back-office only.** The Admin server answers CORS for the Back-office's origin alone, and takes a
  mutation authenticated by the cookie only as JSON from that origin, as the other APIs do for the Console's. An API
  key's bearer token works there as on the other APIs, with the key's own grants.
- **Seeded users** sign in to the Back-office on a seeded installation as on the Console. None of them holds an
  installation grant, so a demo installation that wants one to administer it lists that user's email in `auth.admins`,
  as any installation names its first administrators ([previews](#previews-and-tests)).

## Permissions

**Every Admin API operation asks the one authorizer for an ordinary permission at its scope**

![Every Admin API operation asks the one authorizer for an ordinary permission at its scope](back-office/check.svg)

Nothing here is new: the Admin API is a third transport over the same service layer, checking the same permissions the
principals design lists.

| Operation | Takes |
| --- | --- |
| Who am I, and what I hold at the installation | Being signed in |
| List and search organizations | `installation.manage` |
| Create an organization for a user | `orgs.create`, and `installation.users.manage` to name its first Owner |
| Enter an organization | `installation.manage` |
| List and search users | `installation.users.manage` |
| Read and change the installation's settings | `installation.manage` |
| List, add, change and remove identity providers | `installation.manage` |

- **Ordinary permissions at their scope.** **Agreed** in the principals design: the Admin API asks the one authorizer,
  as GraphQL and REST do. Every operation in this slice acts at the installation; the operations on one organization
  that come later (renaming or deleting it) check that organization's own permissions, so its Owners use them too
  ([administration at any scope](principals-and-permissions.md#scopes-and-grants)).
- **No content.** An organization's page in the Back-office shows its name, slug, creation and member count, never its
  repositories, chats or members' work. Seeing inside takes [entering it](#entering-one).
- **Administrators still come from the values.** In this slice nobody becomes an administrator in the Back-office:
  `auth.admins` stays the one way to Installation admin, and granting installation roles comes later.
- **Lists page.** The organizations and users lists take `?q=` to search and return pages with an opaque cursor and a
  `Link: rel="next"`, since an installation's lists outgrow one answer.

## Organizations

The Back-office lists every organization by name, with its slug, creation and member count, and searches them by slug or
name. Selecting one shows the same, and offers entering it.

### Creating one

- **For a user.** **Proposed:** an administrator creates an organization with a slug, a name and its first Owner, an
  existing user found by a verified email. The administrator doesn't join it: the organization is the Owner's, as one
  they had created themselves.
- **The same rules** as the Console's creation: the slug's shape and reserved paths, one transaction for the
  organization, its slug, the Owner's membership and grant, and the events, `org.created` naming the administrator as
  the actor and the Owner in its details ([creating one](sign-in-and-organizations.md#creating-one)).
- **Why `installation.users.manage` too.** Naming any user as Owner means finding users by email, which only seeing
  every user allows. An installation with self-serve organizations off gives organizations out this way.

### Entering one

**Entering an organization makes the administrator its Owner, with a reason, on its audit trail**

![Entering an organization makes the administrator its Owner, with a reason, on its audit trail](back-office/enter.svg)

The administrator leaves like any member, from the Console; the organization had an Owner before, so the last-Owner rule
never holds them there.

- **On the record.** **Agreed** in the principals design: an administrator who must look inside an organization makes
  themselves its Owner from the Back-office, and that lands in its audit trail. **Proposed:** entering asks for a
  reason, kept in event `org.entered`, which the organization's Owners read with the rest of its trail.
- **An Owner like any other.** The grant is the ordinary Owner role, marked with source `entry` so the trail and a later
  review can tell it apart. An administrator who is already a member gains the Owner grant; one who is already an Owner
  changes nothing, and the entry is still recorded.
- **Leaving** is the Console's, as for any member. The organization had an Owner before, so the last-Owner rule never
  holds the administrator there.
- **Not time-boxed yet.** An entry lasts until the administrator leaves. Ending it on its own after a while is a later
  refinement.

## Users

The Back-office lists every user: display name, emails and whether each is verified, the providers they sign in with,
their installation roles, how many organizations they belong to, when they signed up and last signed in, and whether
they are suspended. It searches by email or name. Suspending and restoring users and revoking their sessions come later;
`installation.users.manage` already covers them.

## Installation settings

**A setting the chart’s values give is theirs; any other is the Back-office’s**

![A setting the chart’s values give is theirs; any other is the Back-office’s](back-office/settings.svg)

An operator who keeps the whole installation in Git sets every value, and the Back-office then only shows them.
`auth.admins` stays a value alone.

The settings in this slice are the sign-up policy (`anyone`, `domains` or `invited`), its domains, and self-serve
organizations. Lifetimes and mail come with later slices.

- **The values lock what they set.** **Proposed:** a setting the chart's values give stays the values', shown read-only
  in the Back-office with a note saying so; one they leave unset is the Back-office's, kept in PostgreSQL. An operator
  who keeps the whole installation in Git sets every value and loses nothing.
- **The defaults leave the chart.** `auth.signUp` and `organizations.selfServe` lose their defaults in the values, so an
  installation that never set them gets the Back-office's, with the same defaults: anyone signs up, and creates
  organizations.
- **The Auth server reads them at each sign-up,** so a change applies to the next one with no restart. A change is
  audited, and versioned: the Back-office sends the version it read, and a change made meanwhile refuses it (`412`).
- **`auth.admins` stays a value alone.** The first administrators must sign in before the Back-office exists, and the
  list is declarative ([sign-up](sign-in-and-organizations.md#sign-up)).

## Identity providers

**A provider added in the Back-office reaches the next sign-in; its secret is written once, encrypted**

![A provider added in the Back-office reaches the next sign-in; its secret is written once, encrypted](back-office/providers.svg)

Changing or removing a provider takes effect on the next sign-in. Sessions already made keep working until they end.

- **Beside the values', never over them.** **Agreed:** the values' providers stay as the sign-in design agreed, listed
  read-only in the Back-office; the Back-office adds more, kept in PostgreSQL. A provider's ID is unique across both:
  the Back-office refuses one the values use, and a values' provider added later with a Back-office provider's ID wins,
  the other shown as shadowed.
- **A client secret is written once,** encrypted with the chart's secrets key (AES-256-GCM), and never read back: the
  Back-office shows only that one is set, as the architecture's [secrets](architecture.md#settings-secrets-and-models)
  are kept.
- **The secrets key** is new to the chart: 32 characters at least, generated on install and kept on upgrade and
  uninstall, or a Secret of the operator's (`secretsKey.existingSecret`, under `secretsKey.secretKey`), which an
  installation rendered without the cluster, as under Argo CD, must give. Only the Admin and Auth servers read it;
  without it, the Admin API refuses a new client secret (`409`).
- **An issuer must answer.** Adding a provider, or changing its issuer or preset, first checks the issuer's OpenID
  Connect discovery. A change or a removal names the version the administrator read (`If-Match`), and one someone made
  meanwhile is refused (`412`), as the settings' are.
- **A change applies at the next sign-in.** The Auth server reads the database's providers at each sign-in and keeps
  each one's discovery while its version holds. Removing a provider stops its sign-ins; its users keep their accounts,
  and sign in again once a provider with the same issuer is back.

## The Back-office

**The Back-office’s first pages**

![The Back-office’s first pages](back-office/pages.svg)

The pages use the design system’s tokens and components, as the Console’s do; until the web apps’ build-out, the
Back-office is one page of plain JavaScript like the Console.

The [design system](design-system.md#screens) hasn't drawn the Back-office's screens; its pages reuse the Console's
components: tables, forms, status badges and the one-time dialog for a new secret.

## Configuration

| Value | Default | Read by |
| --- | --- | --- |
| `urls.admin` | None | The Admin server's public URL: the Back-office calls it there; every workload names the cookie from it |
| `urls.backOffice` | None | The Back-office's public URL: the Admin server's CORS, the Auth server's returns, the cookie |
| `images.backOffice` | Cobranch's | The Back-office's image, at the chart's version |
| `adminServer`, `backOffice` | 1 replica each | The chart: resources and placement, like every workload's |
| `auth.signUp`, `auth.signUpDomains` | None now | The Auth server, and the Admin server, which shows them locked; set, they lock the Back-office's settings |
| `organizations.selfServe` | None now | The Auth server, and the Admin server, which shows it locked; set, it locks the Back-office's |
| `auth.providers` | None | The Auth server, and the Admin server, which lists them read-only |
| `secretsKey.existingSecret`, `secretsKey.secretKey` | None: generated; `key` | The Admin and Auth servers: the key that encrypts providers' client secrets |

Without `urls.admin` the chart deploys neither new workload, as it deploys no Auth server without `urls.auth`.
**Agreed** in the sign-in design: the chart passes each value as a `COBRANCH_` environment variable.

## Previews and tests

- **A seeded administrator, granted by the values.** The demo data gains a user with no organization and no grant, Ada
  Admin. A demo installation that should show the Back-office lists her email in `auth.admins`, as any installation
  names its administrators, so the demo data itself still holds no installation grant
  ([risks](sign-in-and-organizations.md#risks)).
- **Tests** cover every operation's permission at its scope, entering's transaction and event, the values' locks, and
  providers' encryption, on PostgreSQL as the other stores' tests do. The chart's kind test in CI signs in as a seeded
  Owner, whom the Admin API refuses, and as Ada Admin, whom it lists in `auth.admins`, who lists the organizations,
  reads the installation's settings and lists its identity providers.

## Data model

**This slice’s tables: the installation’s settings and providers; entries are grants and events**

![This slice’s tables: the installation’s settings and providers; entries are grants and events](back-office/er.svg)

The two tables carry trigger `audit()`, the secret’s column kept out of the log. A provider’s ID is unique across the
values’ and the table’s.

## Rollout

**Three pull requests in Cobranch**

![Three pull requests in Cobranch](back-office/rollout.svg)

Nothing of an installation is in the product: an operator routes the workloads and sets the values through the chart
alone.

- **Each pull request keeps `main` working,** with its Back-office pages and tests, and is Part of
  [ENG-219](https://linear.app/arikkfir/issue/ENG-219); the third closes it.
- **An installation is the operator's,** configured through the chart alone: the two new workloads' routes, `urls.admin`
  and `urls.backOffice`, and from the second pull request a secrets key, which an installation rendered without the
  cluster gives before the third, whose chart generates one otherwise.

## Risks

- **An administrator's session reaches every organization they enter.** Entering asks for a reason and lands on the
  organization's trail; an operator can keep the Back-office and the Admin server on an internal gateway.
- **A lost secrets key** loses the Back-office's providers' client secrets: an administrator enters them again. The key
  never leaves the cluster's Secrets.
- **A setting the values lock** can't be changed from the Back-office during an incident; the operator changes the
  values.
- **Removing a provider** strands its users until a provider with the same issuer is back, since linking identities
  comes later.

## Decisions

| Proposal | Why | Decision |
| --- | --- | --- |
| The Admin server serves `api/admin.yaml` under `/v1/`, one replica, no Kubernetes token | The architecture's Admin API, a third transport over the one service layer | Proposed |
| The Back-office is static pages from its own image, signed in through the Auth server like the Console | One sign-in, one cookie; the operator can keep it internal | Proposed |
| The Auth server returns to a path of the Console's URL or a URL under the Back-office's | Both apps sign in through it; nothing else can be a return | Proposed |
| Every operation of this slice acts at the installation; later ones on one organization check its own permissions | Administration at any scope, as the principals design agreed | Proposed |
| An administrator creates an organization for an existing user, who becomes its Owner; the administrator doesn't join | Installations with self-serve off give organizations out; administrators see no content | Proposed |
| Entering asks for a reason, kept in `org.entered` on the organization's trail; the Owner grant's source is `entry` | Owners see why someone entered; the grant can be told apart | Proposed |
| The chart's values lock the settings they set; the rest live in PostgreSQL, audited and versioned | Git-only operators lose nothing; others change settings without a redeploy | Proposed |
| Identity providers can be added in the Back-office beside the values', client secrets encrypted with the chart's secrets key | [ENG-219](https://linear.app/arikkfir/issue/ENG-219) asks for it; the agreed providers in the values stay as they are | Agreed |
| Lists page with an opaque cursor and `Link: rel="next"` | An installation's organizations and users outgrow one answer | Proposed |
| Suspending users, revoking their sessions, installation roles, an organization's own administration, audit trails, lifetimes and mail come later | Keeps this slice to its issue | Proposed |

## Open questions

None. The one this page raised, whether the Back-office edits identity providers or only shows the chart's, was answered
on 2026-10-31: it adds them beside the values', as [above](#identity-providers).

Cobranch is a Kfirs project, created by Arik Kfir.
