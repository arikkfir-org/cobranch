# DevHub principals and permissions

> **Snapshot.** This design lives in its [principals and permissions page](https://claude.ai/artifact/J8TVehKS9N6qGSsgjBH7hw),
> linked with DevHub's other designs; this file is a snapshot of 2026-10-27, synced again once DevHub's shape settles.

**Status**: agreed on 2026-10-27, except the choices still marked **Proposed**.

Every actor in DevHub is a principal, and a principal can do something only because a grant says so: a role, which
bundles built-in permissions, given to the principal at the installation, an organization or a repository. Nothing is
ambient, not even for jobs, agents or administrators.

## Scope

This is the identity and access design ([ENG-196](https://linear.app/arikkfir/issue/ENG-196)): the kinds of principal,
how each one signs in or gets a token, the built-in permissions, the built-in and custom roles, and how grants are
scoped and checked. It builds on the [DevHub design](https://claude.ai/artifact/BsCFv8jQ7XunQP2T64Sjoy) and on the
[technical architecture](architecture.md), whose credentials, events and tables it fills in. Sign-in, sign-up and
organizations are built in [ENG-197](https://linear.app/arikkfir/issue/ENG-197).

Choices the product design did not make are marked **Agreed**, or **Proposed** while still open, and collected at the
end.

## Principal kinds

Seven kinds of principal cover every actor. Users belong to the installation, which is what lets one person work in
several organizations; every other kind lives inside one organization, or one repository, and can never be granted
anything outside it.

| Kind | Created by | Lives in | Authenticates with | Can do at most |
| --- | --- | --- | --- | --- |
| User | Themselves, at sign-up; or the push worker, login-less, for an unknown commit author | The installation, a member of one or more organizations | An identity provider (a session), SSH keys, personal tokens | Its own grants plus its groups' grants |
| Group | A member manager of the organization | One organization | Nothing: it only receives grants | Passes its grants to its members |
| Agent | A user, inside one of their organizations | That organization, which owns it; it records the user who created it | A short-lived token for each task it runs | Its grants; on a task, never more than whoever assigned it |
| Job | A job in a repository's `/devhub.yaml`, one principal per job name | One repository | A short-lived token for each run | What it declares, and never more than the repository's job ceiling |
| API key | A principal manager of the organization | One organization | Its secret, as a bearer token | Its grants |
| OAuth app | A principal manager of the organization, in a later slice | One organization | Client credentials, or a user's consent | As itself, its grants; for a user, the consented scopes within that user's permissions |
| Workload identity | A principal manager of the organization | One organization | An outside platform's OpenID Connect token, exchanged for a DevHub token | Its grants |

A **login-less user** is a user principal with no identity. It cannot sign in and holds no grants; it exists so that
every commit has an author principal, until a person proves the email is theirs.

**Agreed:** credentials are not principals. A session, an SSH key or a personal token is a way for a user to prove who
they are, and it acts as that user. An API key, by contrast, is its own principal with its own grants, so a script never
borrows a person's access.

## Users

A user signs in only through an identity provider the installation trusts; DevHub stores no passwords. A user's email
ties them to their commits, but never decides who they are.

### Sign-in and sign-up

- **Identities.** The installation configures its identity providers: Google only in phase 1, then other OpenID Connect
  providers and GitHub's OAuth. An identity is the provider and its stable subject, never the email, because emails
  change hands.
- **Sign-up.** The first sign-in with an unknown identity creates a user, records the identity, and records the
  provider's email when the provider marks it verified. The installation decides who may sign up: anyone, which is the
  default because sign-up is self-serve, or only emails on listed domains, or only invited emails.
- **Linked identities.** A signed-in user can link more identities and then sign in with any of them. DevHub never
  merges two users because their emails match.
- **Sessions.** A session is a server-side row the user, or an installation admin, can revoke. It records the identity
  the user signed in with, and its idle and absolute lifetimes are installation settings, listed under Lifetimes.
- **Organizations' providers.** Decided on 2026-10-27: each organization lists which of the installation's identity
  providers its members may use, all of them until it narrows the list. The list is an organization setting, kept in the
  database. A member's grants in the organization count only while they are signed in with an identity from one of those
  providers, and a personal token counts as the identity of the session that created it. The Console then asks the
  member to sign in with an allowed provider, linking one first if they have none.
- **The first administrators.** **Agreed:** the chart's values name the first installation admins by email, and their
  first sign-in with that email verified grants them the Installation admin role. Nobody becomes an administrator by
  being first.

### Organizations first

A new user belongs to no organization and can do two things only: create an organization, becoming its owner, or ask to
join one ([ENG-197](https://linear.app/arikkfir/issue/ENG-197)). A member manager approves or declines a request; an
invitation by email skips it.

A user's last membership cannot be left or removed; deleting the account is the way out. When an organization is
deleted, its members who belong nowhere else return to that first choice.

### Emails and login-less users

- **One owner per email.** Each email belongs to exactly one principal. A user's emails are verified by their identity
  provider or by a DevHub verification mail.
- **Unknown authors.** The push worker resolves each commit's author and committer by email. An email nobody holds gets
  a new login-less user, with the email unverified.
- **Claiming.** **Agreed:** when a user verifies an email a login-less user holds, the login-less user is claimed. It
  records who claimed it, reads show the claiming user at once, and a background task then rewrites its commits and
  other references to the claiming user. A verified email can never be claimed again.
- **Authorship is a label.** Anyone can write any email into a commit, so a commit's author principal says who the
  commit claims wrote it. What DevHub trusts is the pusher, who authenticated. A signature can prove more, as the next
  item shows.
- **Verified commits.** **Agreed:** users register signing keys, GPG keys or SSH keys marked for signing, the two kinds
  Git signs with. The pre-receive hook adds each new commit's signature to the push's manifest, and the push worker
  checks it in the push's transaction. A commit is *Verified* when the signature is valid and made by a key of the
  principal holding the committer's email, verified; *Unverified* when it is signed but the signature fails, the key is
  unknown, or its owner does not hold that email; and *Unsigned* otherwise. The Console shows the result beside the
  commit, a branch can require verified commits in `/devhub.yaml` like linear history, and commits DevHub writes itself,
  such as merges and settings, are signed with the installation's key. The result is recorded when a commit is first
  pushed and kept, so removing a key later does not change history.

## Organizations and groups

Membership says that a user belongs to an organization; grants say what they may do there. Joining gives a member one
grant, their membership role, and nothing else.

- **Membership role.** Every member holds one role at organization scope, chosen when they are invited or approved.
  **Agreed:** the organization's default is Member, which reads every repository; an organization can make Guest its
  default and grant repositories one by one.
- **Owners.** The user who creates an organization becomes its Owner. An organization always keeps at least one Owner:
  the last one cannot leave, be removed or be demoted.
- **People from elsewhere.** Grants never cross organizations. A person who needs one repository of another organization
  joins it, usually as a Guest, and gets a grant on that repository; their own organizations are untouched.
- **Groups.** A group is a principal of one organization whose members are that organization's users and agents. A grant
  to a group reaches every member. **Agreed:** groups do not nest, so a check never walks a tree.
- **Leaving.** A user who leaves loses every grant they held in the organization, directly or through its groups. Agents
  they created stay, since the organization owns them, as do its API keys and workload identities. Tasks they assigned
  stop, since a task never does more than its assigner.

## Machine principals

Agents, jobs, API keys, OAuth apps and workload identities hold grants the way users do. Each one is capped, so it can
never do more than whoever stands behind it.

| Principal | Can do at most |
| --- | --- |
| User | Its own grants and its groups' grants |
| Agent | Its grants; on an assigned task, intersected with what the assigner can do at that moment |
| Job | The permissions it declares, intersected with the repository's job ceiling |
| API key, workload identity | Its grants |
| OAuth app acting for a user | The scopes the user consented to, intersected with what the user can do |
| Any token | Further narrowed to the permissions and scopes the token lists, when it lists any |

### Agents

**Decided:** agents are created by users and owned by their organization. A user with `agents.create` creates one and
grants it roles within their own permissions; the agent stays when its creator leaves, and `org.principals.manage`
manages it like any other principal of the organization.

**Agreed:** a task runs as the agent but never does more than the principal who assigned it can do at that moment, and
assigning one takes `agents.assign`. Otherwise anyone allowed to assign tasks could reach, through the agent, whatever
the agent may do. Tasks DevHub starts on its own, such as a review on every push, have no assigner and run on the
agent's grants alone.

Each task runs in its own long-running agent pod with a short-lived token, revoked when the task ends or is stopped. In
branch chats an agent posts as itself. **Agreed:** the AI reviewer is a built-in agent created with every organization
and granted the Automation role there, so it can read, annotate, review and post on every repository. Agents' tools and
outside credentials belong to the agents design.

### Jobs

Each job name in a repository is one principal, created when a commit first declares it and kept across runs, so
annotations and audit read "job `ci` of this repository". `/devhub.yaml` declares the permissions a job needs.

**Agreed:** a repository's job ceiling caps every job's permissions. It is a repository setting, so it lives in the
default branch's `/devhub.yaml` like the others, and defaults to the built-in Automation role. A branch may rewrite its
own `/devhub.yaml` in phase 1, but its ceiling takes effect only once it merges into the default branch, through that
branch's merge requirements.

The controller mints a token for each run, bound to the run, its commit and its branch, and valid only while the run
lasts. **Agreed:** each run can also get an OpenID Connect ID token signed by DevHub, whose claims name the
organization, repository, branch, commit, job and event. Clouds and other systems can trust it through their own
workload identity federation, so a job reaches them without a stored secret.

### API keys and personal tokens

An API key is a principal of its organization, created with `org.principals.manage` and granted roles like any other.
Its secret is shown once and stored as a hash, may expire, and rotates with two secrets valid during the overlap. Every
DevHub secret carries a fixed prefix so that secret scanners can spot a leaked one.

A personal token is a user's credential, for Git over HTTPS and the command line. It lists the permissions and scopes it
may use, a subset of the user's own, and it must expire; the installation sets the longest lifetime allowed.

### OAuth apps

In a later slice, DevHub becomes an OAuth authorization server. Decided on 2026-10-27: each organization registers its
own apps, and an app belongs to that organization alone. It acts either as itself, with client credentials and its own
grants there, or for one of the organization's members who consents, through the authorization code flow with PKCE, and
never inside another organization. A tool that several organizations use is registered by each of them.

### Workload identities

A workload identity is a principal of its organization plus the rules for trusting an outside token: the issuer, the
audience, and conditions on the claims, such as a CI system's repository and branch or a cluster's ServiceAccount. The
workload sends its platform's token to DevHub's token endpoint (OAuth token exchange) and receives a short-lived DevHub
token for that principal. Neither side stores a secret.

### Acting for someone else

A token always names the principal it acts as. When it acts for another principal, as an OAuth app does for a user,
every event it causes records both.

## Scopes and grants

A grant is one principal, one role and one scope, and it applies at its scope and everything inside it. There are three
scopes, permissions only add up, and nothing can be denied.

![A grant reaches its scope and everything inside it](principals-and-permissions/grant-scopes.svg)

Users live at the installation and join organizations. Every other principal lives in one organization, and jobs in one
repository, so their grants can never reach outside it.

- **Three scopes.** The installation holds organizations, and an organization holds repositories. A Developer grant on
  an organization covers every repository in it, including those created later.
- **Who may receive one.** At installation scope, users only. At organization or repository scope, the organization's
  members and its own principals: groups, agents, API keys, OAuth apps and workload identities. Jobs receive no grants;
  their declarations and the ceiling decide.
- **Visibility.** **Proposed:** a repository is *Private*, *Internal* or *Public*, and starts Private. Internal grants
  Reader on it to *Signed-in users*, a built-in principal that every user of the installation belongs to; Public grants
  Reader to *Anyone*, which also covers callers with no credential. These two are the one exception to grants staying
  inside an organization, and they only ever hold Reader on a repository. Changing a repository's visibility takes
  `org.grants.manage`. An installation can turn Public off, and an organization can cap its repositories at Internal or
  Private.
- **Permissions apply where they fit.** Each permission belongs to a scope. A role granted on a repository gives only
  its repository permissions, and the API warns when a role's wider permissions would have no effect.
- **Additive only.** **Agreed:** there are no deny rules, so a narrower grant never takes away a wider one. To give
  someone less on one repository, give them less at the organization and more on the repositories they need.
- **No branch scope.** **Agreed:** who may change a branch is a matter of its merge requirements and the `repo.bypass`
  permission, not of grants on the branch.
- **Nobody grants more than they hold.** Granting a role at a scope takes `org.grants.manage` there and every permission
  of that role there. The same rule applies to defining or changing a custom role.
- **Administrators do not see content.** **Agreed:** the Installation admin role holds only installation permissions. An
  installation admin who must look inside an organization makes themselves its Owner from the Back-office, and that act
  lands in the organization's audit trail, visible to its Owners.

## Permissions

DevHub ships a fixed list of 23 permissions for the foundations, named `<object>.<action>`. A release may add
permissions but never renames one; issues, delivery, secrets and agents add theirs in their own designs.

| Permission | Scope | Allows |
| --- | --- | --- |
| `installation.manage` | Installation | Change installation settings and identity providers; use the Back-office |
| `installation.users.manage` | Installation | See every user; suspend and restore users; revoke their sessions |
| `orgs.create` | Installation | Create organizations |
| `org.read` | Organization | See the organization, its members, groups and principals |
| `org.manage` | Organization | Rename the organization, change its settings, delete it |
| `org.members.manage` | Organization | Invite, approve and remove members; manage groups |
| `org.grants.manage` | Organization | Define custom roles; grant and revoke roles, within the granter's own permissions |
| `org.principals.manage` | Organization | Create and delete API keys, OAuth apps and workload identities; manage agents |
| `org.audit.read` | Organization | Read the organization's audit trail |
| `agents.create` | Organization | Create agents, which the organization owns |
| `agents.assign` | Organization | Assign tasks to the organization's agents, which then do no more than the assigner can |
| `repos.create` | Organization | Create repositories |
| `repo.read` | Repository | See and clone the repository; read its branches, commits, annotations, reviews, chats, job runs and logs |
| `repo.push` | Repository | Push branches and tags; create and delete branches |
| `repo.merge` | Repository | Ask DevHub to merge a Ready branch |
| `repo.bypass` | Repository | Push into or merge a branch whose merge requirements do not hold |
| `repo.manage` | Repository | Change settings from the Console, which DevHub commits to the devhub/settings branch; rename or delete the repository |
| `annotations.write` | Repository | Set annotations on commits, and change or delete one's own |
| `annotations.delete_any` | Repository | Delete annotations other principals set |
| `reviews.submit` | Repository | Review commits |
| `messages.post` | Repository | Post in branch chats, and edit or delete one's own messages |
| `messages.moderate` | Repository | Delete others' messages; resolve others' threads |
| `jobs.run` | Repository | Start manual jobs; re-run and cancel runs |

## Roles

A role is a named set of permissions. **Agreed:** nine built-in roles cover the usual cases, each repository role
includes the one before it, and an organization defines custom roles from the same permissions for everything else.

| Role | Granted at | Holds |
| --- | --- | --- |
| Installation admin | Installation | `installation.manage`, `installation.users.manage`, `orgs.create` |
| Installation user | Installation, to every user | `orgs.create`, unless the installation turns self-serve organizations off |
| Owner | Organization | Every organization and repository permission |
| Member | Organization | `org.read`, `agents.create`, `agents.assign`, `repos.create`, and Reader on every repository |
| Guest | Organization | `org.read` |
| Reader | Organization or repository | `repo.read` |
| Developer | Organization or repository | Reader, plus `repo.push`, `repo.merge`, `annotations.write`, `reviews.submit`, `messages.post` and `jobs.run` |
| Maintainer | Organization or repository | Developer, plus `repo.manage`, `repo.bypass`, `annotations.delete_any` and `messages.moderate` |
| Automation | Repository, as the default job ceiling; organization, for the AI reviewer | `repo.read`, `annotations.write`, `reviews.submit` and `messages.post` |

Whoever creates a repository becomes its Maintainer.

- **Custom roles.** An organization's custom role has a name, a description and any set of built-in permissions its
  definer holds. It can be granted at the organization or on a repository, like a built-in role.
- **Changes apply at once.** Editing a custom role changes every grant of it on the next check. A role that is still
  granted cannot be deleted.
- **Built-in roles are fixed.** They cannot be edited, and the installation has no custom roles.

## How a check runs

One authorizer in the service layer answers every check, for REST, GraphQL, Git and the workers alike, so no surface can
be more permissive than another.

1. The credential resolves to its principal, and to the permissions it is limited to when it lists any; a request
   without one resolves to *Anyone*. An expired or revoked credential, or a suspended principal, stops here with `401`.
2. The object resolves to its scopes: its repository, that repository's organization, the installation.
3. The authorizer loads the principal's grants at those scopes, plus its groups' grants and those of *Anyone* and, for a
   user, *Signed-in users*, and expands their roles into permissions. A user's grants in an organization count only when
   they signed in with a provider it allows.
4. It applies the cap of the principal's kind: the assigner's permissions for an agent's task, the job ceiling for a
   job, the consent and the user's permissions for an OAuth app, and the token's own list for any token.
5. The check passes if the permission is left. Otherwise the answer is `404` when the principal cannot see the object,
   and `403` when it can.

Where the checks happen:

- **API.** REST handlers and GraphQL resolvers reach the authorizer through the service layer. GraphQL checks every
  object it returns, not only the field asked for first.
- **Lists.** The authorizer gives the organizations and repositories in which the principal holds the permission, and
  the SQL query filters by them, so a page never comes back short and never leaks.
- **Git.** The Git server checks `repo.read` before a fetch and `repo.push` before it reads a pushed pack. The push
  worker checks merge requirements and `repo.bypass` inside the push's transaction.
- **Runs.** A job or agent calling back with its run token goes through the same authorizer as anyone else.

**Agreed:** grants are read once per request and never cached across requests, so a revoked grant stops working on the
very next request. A cache can come later, if checks ever show up in latency.

Every change to grants, roles, memberships, principals or credentials, every claim, every bypassed requirement and every
Back-office entry into an organization is an event in the architecture's `events` table, naming the actor and, when
there is one, whom it acted for. `org.audit.read` reads them as the organization's audit trail.

## Lifetimes

Decided on 2026-10-27: sensible defaults for now. Each lifetime is an installation setting, and these are its defaults.
The hourly maintenance job drops what has expired.

| Credential | Lifetime |
| --- | --- |
| Session | 7 days without use, and 30 days in all; then the user signs in again |
| Personal token | Chosen at creation: 30 days by default, at most 1 year |
| API key secret | Chosen at creation: 1 year by default, or none |
| Job run token | As long as the run, and at most 24 hours |
| Job's OpenID Connect token, for outside systems | 10 minutes |
| Agent task token | 1 hour, renewed by the controller while the task runs |
| Exchanged workload identity token | 1 hour |
| Invitation, join request | 7 days |
| SSH key, signing key | None; its user removes it |

## Data model

These tables fill in the architecture's identity area. Every row an organization owns carries its `org_id`, as
everywhere else in DevHub.

| Table | Key | Holds |
| --- | --- | --- |
| `principals` | `id` | Kind, display name, state (active, suspended, login-less, claimed), organization (none for users), repository and job name (jobs), creator (agents), claiming user (claimed login-less users) |
| `identities` | `provider`, `subject` | The user, the provider's email and whether it is verified, last sign-in |
| `principal_emails` | `email` | The principal holding it, when it was verified |
| `sessions` | `id` | User, the identity it signed in with, created, last seen, expiry, revoked |
| `credentials` | `id`; `hash` unique | Principal, kind (personal token, API key secret, run token), the permissions and scopes it is limited to, the identity a personal token was created under, expiry, last use, revoked |
| `ssh_keys` | `fingerprint` | Principal, public key, last use |
| `signing_keys` | `fingerprint` | Principal, kind (GPG or SSH), public key, revoked |
| `memberships` | `org_id`, `user_id` | When the user joined; their membership role is a grant |
| `join_requests`, `invitations` | `id` | Who asked or was invited, for which role, the state, who decided, expiry |
| `group_members` | `group_id`, `member_id` | Which users and agents a group holds |
| `roles` | `id`; `org_id`, `name` unique | A built-in role (no organization) or a custom one: name, description, permissions |
| `grants` | `id`; principal, role and scope unique | Principal, role, scope kind and ID, who granted it, when |
| `workload_trusts` | `id` | The workload identity, issuer, audience, claim conditions |

The job ceiling, like every repository setting, comes from the default branch's `/devhub.yaml`, which the architecture
copies into PostgreSQL. OAuth apps and their consents add tables in their own slice.

## Decisions

Nineteen choices here went beyond the product design. The decisions of 2026-10-27 changed two of them and made three
more, and the rest were agreed the same day, except repository visibility, which is still proposed.

| Proposal | Why | Decision |
| --- | --- | --- |
| Credentials are not principals: sessions, SSH keys and personal tokens act as their user, while an API key is a principal of its own | Scripts never borrow a person's access | Agreed |
| Agents are created by users and owned by the organization | Decided on 2026-10-27; an agent outlives its creator's membership | Changed |
| An agent's task never does more than its assigner can, and assigning takes `agents.assign` | Otherwise assigning a task to a stronger agent escalates access | Agreed |
| One principal per job name; its declared permissions are capped by a job ceiling, a setting in the default branch's `/devhub.yaml` | Settings live in `/devhub.yaml`, as decided; a branch's own copy counts only once merged | Changed |
| Job runs can get DevHub-signed OpenID Connect tokens for outside systems | Jobs reach clouds without stored secrets | Agreed |
| Grants only add; there are no deny rules | One rule to reason about, and checks that never conflict | Agreed |
| Three scopes and no branch scope; branches answer to merge requirements and `repo.bypass` | Requirements already govern branches | Agreed |
| Grants never cross organizations; outside people join as Guests | An organization sees everyone who can reach it among its members | Agreed |
| Groups hold users and agents and do not nest | A check never walks a tree | Agreed |
| Installation admins see no organization content; entering one makes them Owner, in its audit trail | Operators run the installation, not the organizations | Agreed |
| The chart's values name the first installation admins | Nobody becomes an administrator by being first | Agreed |
| Organizations default new members to Member, which reads every repository | Teams start open inside; Guest is one setting away | Agreed |
| A verified email claims a login-less user; a background task rewrites its references | The commits become the person's without a long transaction | Agreed |
| The AI reviewer is a built-in agent of every organization, holding the Automation role there | It reads, annotates, reviews and posts on every repository, and nothing more | Agreed |
| Commits signed with a GPG or SSH key of the principal holding the committer's verified email show as Verified; a branch can require them | Authorship can be proven, not only claimed | Agreed |
| Repositories are Private, Internal or Public: Reader grants to the built-in principals Signed-in users and Anyone | Decided on 2026-10-27 that repositories can be read more widely; as grants, visibility goes through the same authorizer as every other access | Proposed |
| Each organization chooses which of the installation's identity providers its members use; phase 1 supports Google only | Decided on 2026-10-27; an organization keeps its members on the providers it trusts | Agreed |
| An OAuth app belongs to the organization that registers it and acts only inside it | Decided on 2026-10-27; an organization answers for every app that reaches its data | Agreed |
| Credentials' lifetimes default as listed under Lifetimes, each an installation setting | Decided on 2026-10-27: sensible defaults for now; machines get short tokens, people longer sessions | Agreed |

The nine built-in roles and the 23 permissions above are agreed too.

## Open questions

- [ ] **Member provisioning.** Should an organization be able to provision and remove its members from its identity
      provider (SCIM)?
- [ ] **Agents' outside credentials.** How an agent holds the credentials of the tools it uses beyond DevHub, such as
      mail or Slack. The agents design answers this.
- [ ] **Phase 2 jobs.** How the default branch's pinned job security works with the job ceiling.
