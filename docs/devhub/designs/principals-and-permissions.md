# DevHub principals and permissions

> **Snapshot.** This design lives in its [principals and permissions
> page](https://claude.ai/artifact/J8TVehKS9N6qGSsgjBH7hw), linked with DevHub's other designs; this file is generated
> from it, with its diagrams, as of 2026-10-27, and synced again once DevHub's shape settles.

**Status**: Agreed 2026-10-27 · [ENG-196](https://linear.app/arikkfir/issue/ENG-196) · Repository visibility still
proposed · Fills in the [architecture](architecture.md#authentication) · Built by
[ENG-197](https://linear.app/arikkfir/issue/ENG-197)

Every actor in DevHub is a principal, and a principal can do something only because a grant says so: a role given at the
installation, an organization or a repository. Nothing is ambient, not even for jobs, agents or administrators.

## Scope

The identity and access design ([ENG-196](https://linear.app/arikkfir/issue/ENG-196)): the kinds of principal, how each
signs in or gets a token, the built-in permissions and roles, and how grants are scoped and checked. It builds on the
[product design](product.md) and fills in the [architecture](architecture.md)'s
[credentials](architecture.md#authentication) and [tables](architecture.md#data-model). Sign-in, sign-up and
organizations are built in [ENG-197](https://linear.app/arikkfir/issue/ENG-197).

Choices the product design did not make are marked **Agreed**, or **Proposed** while still open, and collected under
[Decisions](#decisions).

## Principal kinds

Seven kinds of principal cover every actor.

**Users live at the installation; every other principal lives inside one organization**

![Users live at the installation; every other principal lives inside one organization](principals-and-permissions/kinds.svg)

One person can work in several organizations because users belong to the installation. Nothing granted inside an
organization reaches outside it.

| Kind | Created by | Authenticates with |
| --- | --- | --- |
| User | Themselves, at sign-up; or the push worker, login-less, for an unknown commit author | An identity provider (a session), SSH keys, personal tokens |
| Group | A member manager of the organization | Nothing: it only receives grants |
| Agent | A user, inside one of their organizations, which owns it | A short-lived token for each task |
| Job | A job in a repository's `/devhub.yaml`, one principal per job name | A short-lived token for each run |
| API key | A principal manager of the organization | Its secret, as a bearer token |
| OAuth app | A principal manager of the organization, in a later slice | Client credentials, or a member's consent |
| Workload identity | A principal manager of the organization | An outside platform's OpenID Connect token, exchanged |

> **Agreed:** credentials are not principals. A session, an SSH key or a personal token proves who a user is and acts as
> that user. An API key is a principal of its own, with its own grants, so a script never borrows a person's access.

## Users

A user signs in only through an identity provider the installation trusts; DevHub stores no passwords. A user's email
ties them to their commits, but never decides who they are.

### Sign-in and sign-up

**Sign-in goes through an identity provider; the identity, never the email, names the user**

![Sign-in goes through an identity provider; the identity, never the email, names the user](principals-and-permissions/sign-in.svg)

The sign-up policy is anyone (the default), listed email domains, or invited emails only. A user can link more
identities and sign in with any of them; DevHub never merges users because their emails match.

- **Identity providers.** Google only in phase 1, then other OpenID Connect providers and GitHub's OAuth.
- **Organizations' providers.** Decided on 2026-10-27: each organization lists which of the installation's providers its
  members may use, all of them until it narrows the list. A member's grants there count only while they are signed in
  through one of them; a personal token counts as the identity of the session that created it.
- **The first administrators.** **Agreed:** the chart's values name them by email. Nobody becomes an administrator by
  being first.
- **Sessions** are rows the user, or an installation admin, can revoke; their lifetimes are under
  [Lifetimes](#lifetimes).

### Organizations first

**A new user belongs nowhere until they create or join an organization**

![A new user belongs nowhere until they create or join an organization](principals-and-permissions/onboarding.svg)

Every member holds one membership role. A user’s last membership cannot be left; deleting the account is the way out.
When an organization is deleted, its members who belong nowhere else return to No organization.

### Emails and login-less users

Each email belongs to exactly one principal, verified by its identity provider or by a DevHub mail. The push worker
resolves every commit's author and committer by email, and an email nobody holds gets a new **login-less user**.

**A login-less user stands in for an unknown author until someone proves the email**

![A login-less user stands in for an unknown author until someone proves the email](principals-and-permissions/claim.svg)

Authorship is a label: anyone can write any email into a commit. What DevHub trusts is the pusher, who authenticated,
and a signature, below.

**Agreed:** users register signing keys, GPG keys or SSH keys marked for signing, and the push worker checks every new
commit's signature.

**A commit is Verified only when a key of its committer signed it**

![A commit is Verified only when a key of its committer signed it](principals-and-permissions/verified.svg)

The push worker checks each signature in the push’s transaction and keeps the result, so removing a key later never
changes history. A branch can require Verified commits in `/devhub.yaml`; commits DevHub writes itself are signed with
the installation’s key.

## Organizations and groups

Membership says that a user belongs to an organization; grants say what they may do there. Joining gives one grant, the
membership role, and nothing else.

- **Membership role.** **Agreed:** Member by default, which reads every repository; an organization can make Guest its
  default and grant repositories one by one.
- **Owners.** The creator is the first Owner, and the last Owner cannot leave, be removed or be demoted.
- **People from elsewhere** join the organization, usually as Guests, and get grants on the repositories they need.
  Grants never cross organizations.
- **Groups** hold the organization's users and agents. **Agreed:** groups do not nest, so a check never walks a tree.
- **Leaving** drops every grant held in the organization, directly or through its groups. Agents the user created stay,
  since the organization owns them; tasks they assigned stop.

## Machine principals

Agents, jobs, API keys, OAuth apps and workload identities hold grants the way users do, each capped by whoever stands
behind it.

**Every principal is capped by whoever stands behind it**

![Every principal is capped by whoever stands behind it](principals-and-permissions/caps.svg)

A token always names the principal it acts as. When it acts for another, as an OAuth app does for a user, every event it
causes records both.

### Agents

**Decided:** a user with `agents.create` creates an agent and grants it roles within their own permissions; the
organization owns it, and `org.principals.manage` manages it. **Agreed:** the AI reviewer is a built-in agent of every
organization, holding Automation there. Its tools and outside credentials belong to the agents design.

**A task runs as the agent, and never beyond what its assigner can do**

![A task runs as the agent, and never beyond what its assigner can do](principals-and-permissions/agent-task.svg)

### Jobs

Each job name in a repository is one principal, kept across runs, so audit reads "job `ci` of this repository".
**Agreed:** the job ceiling, a setting in the default branch's `/devhub.yaml`, caps what any job declares, and defaults
to Automation. A branch's own `/devhub.yaml` counts only once it merges.

**A job run reaches DevHub with its run token, and other clouds with an ID token**

![A job run reaches DevHub with its run token, and other clouds with an ID token](principals-and-permissions/job-tokens.svg)

The run token lasts as long as the run, at most 24 hours. Neither DevHub nor the cloud stores a secret.

### API keys and personal tokens

An API key's secret is shown once, stored hashed, may expire, and rotates with two secrets valid during the overlap. A
personal token is a user's credential for Git over HTTPS and the command line: it lists the permissions and scopes it
may use, a subset of the user's, and must expire. Every DevHub secret carries a fixed prefix so scanners spot a leaked
one.

### OAuth apps

In a later slice DevHub becomes an OAuth authorization server. Decided on 2026-10-27: an app belongs to the organization
that registers it and acts only there, as itself with client credentials, or for a consenting member through the
authorization code flow with PKCE.

### Workload identities

A workload identity is a principal plus the rules for trusting an outside token: issuer, audience, and conditions on the
claims, such as a CI system's repository and branch or a cluster's ServiceAccount.

**An outside workload trades its platform’s token for a short-lived DevHub token**

![An outside workload trades its platform’s token for a short-lived DevHub token](principals-and-permissions/workload.svg)

## Scopes and grants

A grant is one principal, one role and one scope, and it applies at its scope and everything inside it.

**A grant reaches its scope and everything inside it**

![A grant reaches its scope and everything inside it](principals-and-permissions/scopes.svg)

There is no branch scope: merge requirements and `repo.bypass` decide who changes a branch.

- **Who may receive one.** At the installation, users only. At an organization or repository, its members and its own
  principals. Jobs receive none: their declarations and the ceiling decide.
- **Visibility.** **Proposed:** a repository is *Private* (the default), *Internal* or *Public*. Internal grants Reader
  to *Signed-in users*, Public to *Anyone*, the one exception to grants staying inside an organization. Changing it
  takes `org.grants.manage`; an installation can turn Public off, and an organization can cap its repositories.
- **Permissions apply where they fit.** A role granted on a repository gives only its repository permissions.
- **Additive only.** **Agreed:** no deny rules. To give someone less on one repository, give less at the organization
  and more where needed.
- **Nobody grants more than they hold.** Granting a role takes `org.grants.manage` and every permission of the role, at
  that scope; defining a custom role, the same.
- **Administrators do not see content.** **Agreed:** an installation admin who must look inside an organization makes
  themselves its Owner from the Back-office, and that lands in the organization's audit trail.

## Permissions

**Twenty-three permissions, each at one scope**

![Twenty-three permissions, each at one scope](principals-and-permissions/permissions.svg)

Names are `<object>.<action>`. A release may add permissions but never renames one; issues, delivery, secrets and agents
add theirs in their own designs.

| Permission | Allows |
| --- | --- |
| `installation.manage` | Change installation settings and identity providers; use the Back-office |
| `installation.users.manage` | See every user; suspend and restore users; revoke their sessions |
| `orgs.create` | Create organizations |
| `org.read` | See the organization, its members, groups and principals |
| `org.manage` | Rename the organization, change its settings, delete it |
| `org.members.manage` | Invite, approve and remove members; manage groups |
| `org.grants.manage` | Define custom roles; grant and revoke roles, within the granter's own permissions |
| `org.principals.manage` | Create and delete API keys, OAuth apps and workload identities; manage agents |
| `org.audit.read` | Read the organization's audit trail |
| `agents.create` | Create agents, which the organization owns |
| `agents.assign` | Assign tasks to agents, which then do no more than the assigner can |
| `repos.create` | Create repositories |
| `repo.read` | See and clone the repository; read its branches, commits, annotations, reviews, chats, job runs and logs |
| `repo.push` | Push branches and tags; create and delete branches |
| `repo.merge` | Ask DevHub to merge a Ready branch |
| `repo.bypass` | Push into or merge a branch whose merge requirements do not hold |
| `repo.manage` | Change settings from the Console, committed to `devhub/settings`; rename or delete the repository |
| `annotations.write` | Set annotations on commits, and change or delete one's own |
| `annotations.delete_any` | Delete annotations other principals set |
| `reviews.submit` | Review commits |
| `messages.post` | Post in branch chats, and edit or delete one's own messages |
| `messages.moderate` | Delete others' messages; resolve others' threads |
| `jobs.run` | Start manual jobs; re-run and cancel runs |

## Roles

**Nine built-in roles; each repository role includes the one before**

![Nine built-in roles; each repository role includes the one before](principals-and-permissions/roles.svg)

Whoever creates a repository becomes its Maintainer. Built-in roles are fixed; an organization defines custom roles from
the same permissions.

- **Custom roles** have a name, a description and any permissions their definer holds, and are granted at an
  organization or on a repository like built-in ones. Editing one changes every grant of it on the next check; one that
  is still granted cannot be deleted.
- **Built-in roles are fixed**, and the installation has no custom roles.

## How a check runs

**One authorizer answers every check, the same way for every surface**

![One authorizer answers every check, the same way for every surface](principals-and-permissions/check.svg)

A caller that cannot see an object gets `404`, never `403`, so it never learns the object exists.

**REST, GraphQL, Git and the workers all ask the same authorizer**

![REST, GraphQL, Git and the workers all ask the same authorizer](principals-and-permissions/where.svg)

A revoked grant stops working on the very next request. A page of a list never comes back short and never leaks.

Every change to grants, roles, memberships, principals or credentials, every claim, every bypassed requirement and every
Back-office entry into an organization is an event in the [architecture](architecture.md#events-and-background-work)'s
`events` table, naming the actor and whom it acted for. `org.audit.read` reads them as the organization's audit trail.

## Lifetimes

Decided on 2026-10-27: sensible defaults, each an installation setting. The hourly maintenance job drops what expired.

| Credential | Lifetime |
| --- | --- |
| Job's OpenID Connect token, for outside systems | 10 minutes |
| Agent task token | 1 hour, renewed by the controller while the task runs |
| Exchanged workload identity token | 1 hour |
| Job run token | As long as the run, at most 24 hours |
| Invitation, join request | 7 days |
| Session | 7 days without use, 30 days in all |
| Personal token | 30 days by default, at most 1 year |
| API key secret | 1 year by default, or none |
| SSH key, signing key | None; its user removes it |

## Data model

These tables fill in the [architecture's data model](architecture.md#data-model). The job ceiling, like every repository
setting, comes from the default branch's `/devhub.yaml`.

**The identity tables: who a principal is, how it proves it, and what it is granted**

![The identity tables: who a principal is, how it proves it, and what it is granted](principals-and-permissions/er.svg)

Every row an organization owns carries its `org_id`. A grant’s principal, role and scope are unique together. OAuth apps
and their consents add tables in their own slice.

## Decisions

| Proposal | Why | Decision |
| --- | --- | --- |
| Credentials are not principals; an API key is a principal of its own | Scripts never borrow a person's access | Agreed |
| Agents are created by users and owned by the organization | Decided on 2026-10-27; an agent outlives its creator's membership | Changed |
| An agent's task never does more than its assigner can; assigning takes `agents.assign` | Otherwise assigning a task to a stronger agent escalates access | Agreed |
| One principal per job name, capped by a job ceiling in the default branch's `/devhub.yaml` | Settings live in `/devhub.yaml`; a branch's own copy counts once merged | Changed |
| Job runs can get DevHub-signed OpenID Connect tokens | Jobs reach clouds without stored secrets | Agreed |
| Grants only add; there are no deny rules | One rule to reason about; checks never conflict | Agreed |
| Three scopes and no branch scope | Merge requirements already govern branches | Agreed |
| Grants never cross organizations; outside people join as Guests | An organization sees everyone who can reach it | Agreed |
| Groups hold users and agents and do not nest | A check never walks a tree | Agreed |
| Installation admins see no organization content; entering one makes them Owner, audited | Operators run the installation, not the organizations | Agreed |
| The chart's values name the first installation admins | Nobody becomes an administrator by being first | Agreed |
| New members default to Member, which reads every repository | Teams start open inside; Guest is one setting away | Agreed |
| A verified email claims a login-less user; a background task rewrites its references | The commits become the person's without a long transaction | Agreed |
| The AI reviewer is a built-in agent holding Automation in every organization | It reads, annotates, reviews and posts, and nothing more | Agreed |
| Commits signed by a key of the committer's verified email show as Verified; a branch can require them | Authorship can be proven, not only claimed | Agreed |
| Repositories are Private, Internal or Public, through Reader grants to Signed-in users and Anyone | Decided on 2026-10-27 that repositories can be read more widely; visibility goes through the same authorizer | Proposed |
| Each organization chooses its identity providers; Google only in phase 1 | Decided on 2026-10-27; an organization keeps its members on providers it trusts | Agreed |
| An OAuth app belongs to the organization that registers it | Decided on 2026-10-27; an organization answers for every app that reaches its data | Agreed |
| Credentials' lifetimes default as listed, each an installation setting | Decided on 2026-10-27; machines get short tokens, people longer sessions | Agreed |

The nine built-in roles and the 23 permissions are agreed too.

## Open questions

- [ ] **Member provisioning.** Should an organization provision and remove its members from its identity provider
  (SCIM)?
- [ ] **Agents' outside credentials.** How an agent holds the credentials of tools beyond DevHub, such as mail or Slack.
  The agents design answers this.
- [ ] **Phase 2 jobs.** How the default branch's pinned job security works with the job ceiling.

DevHub is a Kfirs project, created by Arik Kfir.
