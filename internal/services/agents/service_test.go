package agents_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"cobranch.dev/internal/services/agents"
	"cobranch.dev/internal/services/agents/agentstest"
	"cobranch.dev/internal/services/auth"
	"cobranch.dev/internal/services/orgs"
	"cobranch.dev/internal/services/orgs/orgstest"
)

var now = time.Date(2026, 11, 2, 9, 0, 0, 0, time.UTC)

// steward manages principals and holds nothing else, so it deletes only agents that hold no more than it does.
var steward = orgs.Role{ID: uuid.MustParse("00000000-0000-7000-8000-000000000f02"), Name: "Steward", Permissions: []string{"org.read", "org.principals.manage"}}

// fixture is organization acme with repositories website, which Mel develops, and vault, which only owners read; its
// feature branch has a thread, and its gone branch was deleted. Olive owns acme, Mel and Max are members, Gus a guest,
// Stu a steward and Bot an agent acting as itself; Nia is no member.
type fixture struct {
	store                               *agentstest.Store
	grants                              *orgstest.Store
	service                             *agents.Service
	org, website, vault                 uuid.UUID
	feature, gone, thread, other        uuid.UUID
	olive, mel, max, gus, stu, bot, nia agents.Viewer
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	o := orgstest.NewStore()
	o.RoleList = append(o.RoleList, steward)
	viewer := func(name string) agents.Viewer {
		return agents.Viewer{Principal: auth.Principal{ID: uuid.New(), Kind: auth.KindUser, DisplayName: name}}
	}
	f := &fixture{store: agentstest.NewStore(), grants: o, org: uuid.New(), website: uuid.New(), vault: uuid.New(),
		feature: uuid.New(), gone: uuid.New(), thread: uuid.New(), other: uuid.New(),
		olive: viewer("Olive"), mel: viewer("Mel"), max: viewer("Max"), gus: viewer("Gus"), stu: viewer("Stu"), bot: viewer("Bot"), nia: viewer("Nia")}
	f.bot.Kind = auth.KindAgent
	f.store.Now = now
	f.store.OrgRoles = append(f.store.OrgRoles, steward)
	f.store.Repos[f.website] = agentstest.Repository{Org: f.org, Slug: "website"}
	f.store.Repos[f.vault] = agentstest.Repository{Org: f.org, Slug: "vault"}
	f.store.Branches[f.feature] = agentstest.Branch{Repo: f.website, Name: "feature"}
	f.store.Branches[f.gone] = agentstest.Branch{Repo: f.website, Name: "gone", Deleted: true}
	f.store.Branches[f.other] = agentstest.Branch{Repo: f.website, Name: "other"}
	f.store.Threads[f.thread] = f.feature
	o.Repos[f.website], o.Repos[f.vault] = f.org, f.org
	o.AddMember(f.org, f.olive.ID, auth.RoleOwner)
	o.AddMember(f.org, f.mel.ID, auth.RoleMember)
	o.AddMember(f.org, f.max.ID, auth.RoleMember)
	o.AddMember(f.org, f.gus.ID, auth.RoleGuest)
	o.AddMember(f.org, f.stu.ID, steward.ID)
	o.AddMember(f.org, f.bot.ID, auth.RoleMember)
	o.Grant(f.mel.ID, auth.RoleDeveloper, auth.ScopeRepository, f.website)
	o.Grant(f.max.ID, auth.RoleReader, auth.ScopeRepository, f.website)
	for _, v := range []agents.Viewer{f.olive, f.mel, f.max, f.gus, f.stu, f.bot, f.nia} {
		f.store.Names[v.ID] = v.DisplayName
	}
	f.service = agents.NewService(f.store, auth.NewAuthorizer(o))
	return f
}

// agent has v create an agent named name holding grants.
func (f *fixture) agent(t *testing.T, v agents.Viewer, name string, grants ...agents.Grant) agents.Agent {
	t.Helper()
	a, err := f.service.CreateAgent(context.Background(), v, f.org, agents.Input{Name: name, Grants: grants})
	if err != nil {
		t.Fatalf("CreateAgent(%s) = %v", name, err)
	}
	return a
}

// assign has v assign a task titled title to a.
func (f *fixture) assign(t *testing.T, v agents.Viewer, a agents.Agent, title string, repo uuid.UUID) agents.Task {
	t.Helper()
	task, err := f.service.Assign(context.Background(), v, f.org, a.ID, agents.TaskInput{Title: title, RepoID: repo})
	if err != nil {
		t.Fatalf("Assign(%s) = %v", title, err)
	}
	return task
}

func invalid(code string) error { return &orgs.ValidationError{Code: code} }

// is reports whether err is want: a validation error of want's code, or matches it.
func is(err, want error) bool {
	var verr, wantV *orgs.ValidationError
	if errors.As(want, &wantV) {
		return errors.As(err, &verr) && verr.Code == wantV.Code
	}
	return errors.Is(err, want)
}

func TestCreateAgent(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.agent(t, f.olive, "Scout")
	org := func(role uuid.UUID) agents.Grant { return agents.Grant{RoleID: role} }
	repo := func(role, id uuid.UUID) agents.Grant { return agents.Grant{RoleID: role, RepoID: id} }
	tests := []struct {
		name   string
		viewer agents.Viewer
		in     agents.Input
		want   error
		grants []agents.Grant
	}{
		{name: "no grants", viewer: f.mel, in: agents.Input{Name: " Helper ", Description: "Helps"}},
		{name: "a role the creator holds", viewer: f.mel, in: agents.Input{Name: "Member", Grants: []agents.Grant{org(auth.RoleMember)}},
			grants: []agents.Grant{{RoleID: auth.RoleMember, RoleName: "Member"}}},
		{name: "a repository role the creator holds there", viewer: f.mel, in: agents.Input{Name: "Dev", Grants: []agents.Grant{org(auth.RoleGuest), repo(auth.RoleDeveloper, f.website)}},
			grants: []agents.Grant{{RoleID: auth.RoleGuest, RoleName: "Guest"}, {RoleID: auth.RoleDeveloper, RoleName: "Developer", RepoID: f.website, RepoSlug: "website"}}},
		{name: "an owner gives anything", viewer: f.olive, in: agents.Input{Name: "Root", Grants: []agents.Grant{org(auth.RoleOwner), repo(auth.RoleMaintainer, f.vault)}},
			grants: []agents.Grant{{RoleID: auth.RoleOwner, RoleName: "Owner"}, {RoleID: auth.RoleMaintainer, RoleName: "Maintainer", RepoID: f.vault, RepoSlug: "vault"}}},
		{name: "a role beyond the creator's", viewer: f.mel, in: agents.Input{Name: "X", Grants: []agents.Grant{org(auth.RoleOwner)}}, want: auth.ErrForbidden},
		{name: "a repository role beyond the creator's", viewer: f.mel, in: agents.Input{Name: "X", Grants: []agents.Grant{repo(auth.RoleMaintainer, f.website)}}, want: auth.ErrForbidden},
		{name: "a repository the creator doesn't read", viewer: f.mel, in: agents.Input{Name: "X", Grants: []agents.Grant{repo(auth.RoleReader, f.vault)}}, want: invalid("repository.invalid")},
		{name: "another organization's repository", viewer: f.olive, in: agents.Input{Name: "X", Grants: []agents.Grant{repo(auth.RoleReader, uuid.New())}}, want: invalid("repository.invalid")},
		{name: "a repository role at the organization", viewer: f.olive, in: agents.Input{Name: "X", Grants: []agents.Grant{org(auth.RoleDeveloper)}}, want: invalid("role.invalid")},
		{name: "an organization role on a repository", viewer: f.olive, in: agents.Input{Name: "X", Grants: []agents.Grant{repo(auth.RoleMember, f.website)}}, want: invalid("role.invalid")},
		{name: "an unknown role", viewer: f.olive, in: agents.Input{Name: "X", Grants: []agents.Grant{org(uuid.New())}}, want: invalid("role.invalid")},
		{name: "a grant twice", viewer: f.olive, in: agents.Input{Name: "X", Grants: []agents.Grant{org(auth.RoleGuest), org(auth.RoleGuest)}}, want: invalid("agent.grants_invalid")},
		{name: "no name", viewer: f.olive, in: agents.Input{Name: "  "}, want: invalid("agent.name_invalid")},
		{name: "a name taken in another case", viewer: f.olive, in: agents.Input{Name: "SCOUT"}, want: agents.ErrNameTaken},
		{name: "a guest", viewer: f.gus, in: agents.Input{Name: "X"}, want: auth.ErrForbidden},
		{name: "an agent", viewer: f.bot, in: agents.Input{Name: "X"}, want: auth.ErrForbidden},
		{name: "no member", viewer: f.nia, in: agents.Input{Name: "X"}, want: orgs.ErrNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a, err := f.service.CreateAgent(context.Background(), tt.viewer, f.org, tt.in)
			if !is(err, tt.want) {
				t.Fatalf("CreateAgent() = %v, want %v", err, tt.want)
			}
			if err != nil {
				return
			}
			if a.OrgID != f.org || a.Version != 1 || a.CreatedBy == nil || a.CreatedBy.ID != tt.viewer.ID || a.Name != strings.TrimSpace(tt.in.Name) {
				t.Errorf("CreateAgent() = %+v", a)
			}
			if !slices.Equal(a.Grants, tt.grants) {
				t.Errorf("grants = %+v, want %+v", a.Grants, tt.grants)
			}
			if got, err := f.service.Agent(context.Background(), f.gus, f.org, a.ID); err != nil || got.Name != a.Name {
				t.Errorf("Agent() = %+v, %v", got, err)
			}
		})
	}
}

func TestAgents(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.agent(t, f.mel, "scout")
	f.agent(t, f.olive, "Archer")
	list, err := f.service.Agents(context.Background(), f.gus, f.org)
	if err != nil || len(list) != 2 || list[0].Name != "Archer" || list[1].Name != "scout" {
		t.Errorf("Agents() = %+v, %v; want Archer, scout", list, err)
	}
	if _, err := f.service.Agents(context.Background(), f.nia, f.org); !errors.Is(err, orgs.ErrNotFound) {
		t.Errorf("Agents(no member) = %v, want %v", err, orgs.ErrNotFound)
	}
	for name, id := range map[string]uuid.UUID{"another organization's": list[0].ID, "none": uuid.New()} {
		org := f.org
		if name == "another organization's" {
			org = uuid.New()
			f.grants.AddMember(org, f.gus.ID, auth.RoleGuest)
		}
		if _, err := f.service.Agent(context.Background(), f.gus, org, id); !errors.Is(err, orgs.ErrNotFound) {
			t.Errorf("Agent(%s) = %v, want %v", name, err, orgs.ErrNotFound)
		}
	}
}

func TestUpdateAgent(t *testing.T) {
	t.Parallel()
	name := func(s string) *string { return &s }
	tests := []struct {
		name    string
		viewer  func(*fixture) agents.Viewer
		version int64
		change  agents.Change
		want    error
	}{
		{name: "its creator", viewer: func(f *fixture) agents.Viewer { return f.mel }, version: 1, change: agents.Change{Name: name(" Seeker "), Instructions: name("Look")}},
		{name: "a principals manager", viewer: func(f *fixture) agents.Viewer { return f.stu }, version: agents.AnyVersion, change: agents.Change{Description: name("Finds")}},
		{name: "another member", viewer: func(f *fixture) agents.Viewer { return f.max }, version: 1, change: agents.Change{Name: name("Mine")}, want: auth.ErrForbidden},
		{name: "a stale version", viewer: func(f *fixture) agents.Viewer { return f.mel }, version: 2, change: agents.Change{Name: name("Late")}, want: agents.ErrChanged},
		{name: "a name taken", viewer: func(f *fixture) agents.Viewer { return f.mel }, version: 1, change: agents.Change{Name: name("archer")}, want: agents.ErrNameTaken},
		{name: "an empty name", viewer: func(f *fixture) agents.Viewer { return f.mel }, version: 1, change: agents.Change{Name: name("")}, want: invalid("agent.name_invalid")},
		{name: "no member", viewer: func(f *fixture) agents.Viewer { return f.nia }, version: 1, change: agents.Change{Name: name("X")}, want: orgs.ErrNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			f := newFixture(t)
			f.agent(t, f.olive, "Archer")
			a := f.agent(t, f.mel, "Scout")
			got, err := f.service.UpdateAgent(context.Background(), tt.viewer(f), f.org, a.ID, tt.version, tt.change)
			if !is(err, tt.want) {
				t.Fatalf("UpdateAgent() = %v, want %v", err, tt.want)
			}
			if err != nil {
				return
			}
			want := a
			for field, value := range map[*string]*string{&want.Name: tt.change.Name, &want.Description: tt.change.Description, &want.Instructions: tt.change.Instructions} {
				if value != nil {
					*field = strings.TrimSpace(*value)
				}
			}
			if got.Name != want.Name || got.Description != want.Description || got.Instructions != want.Instructions || got.Version != 2 {
				t.Errorf("UpdateAgent() = %+v, want %+v at version 2", got, want)
			}
		})
	}
	t.Run("an agent not there", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)
		if _, err := f.service.UpdateAgent(context.Background(), f.olive, f.org, uuid.New(), 1, agents.Change{Name: name("X")}); !errors.Is(err, orgs.ErrNotFound) {
			t.Errorf("UpdateAgent() = %v, want %v", err, orgs.ErrNotFound)
		}
	})
}

func TestDeleteAgent(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		viewer  func(*fixture) agents.Viewer
		agent   func(*testing.T, *fixture) agents.Agent
		version int64
		want    error
	}{
		{name: "an owner", viewer: func(f *fixture) agents.Viewer { return f.olive }, version: 1,
			agent: func(t *testing.T, f *fixture) agents.Agent {
				return f.agent(t, f.mel, "Scout", agents.Grant{RoleID: auth.RoleMember})
			}},
		{name: "a steward, an agent that holds nothing", viewer: func(f *fixture) agents.Viewer { return f.stu }, version: agents.AnyVersion,
			agent: func(t *testing.T, f *fixture) agents.Agent { return f.agent(t, f.mel, "Scout") }},
		{name: "a steward, an agent that holds more", viewer: func(f *fixture) agents.Viewer { return f.stu }, version: 1, want: auth.ErrForbidden,
			agent: func(t *testing.T, f *fixture) agents.Agent {
				return f.agent(t, f.mel, "Scout", agents.Grant{RoleID: auth.RoleMember})
			}},
		{name: "a steward, an agent that holds more on a repository", viewer: func(f *fixture) agents.Viewer { return f.stu }, version: 1, want: auth.ErrForbidden,
			agent: func(t *testing.T, f *fixture) agents.Agent {
				return f.agent(t, f.mel, "Scout", agents.Grant{RoleID: auth.RoleReader, RepoID: f.website})
			}},
		{name: "its creator, without principals.manage", viewer: func(f *fixture) agents.Viewer { return f.mel }, version: 1, want: auth.ErrForbidden,
			agent: func(t *testing.T, f *fixture) agents.Agent { return f.agent(t, f.mel, "Scout") }},
		{name: "a stale version", viewer: func(f *fixture) agents.Viewer { return f.olive }, version: 2, want: agents.ErrChanged,
			agent: func(t *testing.T, f *fixture) agents.Agent { return f.agent(t, f.mel, "Scout") }},
		{name: "a built-in agent", viewer: func(f *fixture) agents.Viewer { return f.olive }, version: 1, want: agents.ErrBuiltIn,
			agent: func(_ *testing.T, f *fixture) agents.Agent {
				return f.store.AddAgent(agents.Agent{OrgID: f.org, Name: "Reviewer", BuiltIn: true})
			}},
		{name: "no member", viewer: func(f *fixture) agents.Viewer { return f.nia }, version: 1, want: orgs.ErrNotFound,
			agent: func(t *testing.T, f *fixture) agents.Agent { return f.agent(t, f.mel, "Scout") }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			f := newFixture(t)
			a := tt.agent(t, f)
			f.assign(t, f.mel, a, "Look around", uuid.Nil)
			err := f.service.DeleteAgent(context.Background(), tt.viewer(f), f.org, a.ID, tt.version)
			if !is(err, tt.want) {
				t.Fatalf("DeleteAgent() = %v, want %v", err, tt.want)
			}
			_, found := f.service.Agent(context.Background(), f.olive, f.org, a.ID)
			page, _ := f.service.Tasks(context.Background(), f.olive, f.org, agents.TaskFilter{}, agents.MaxPage)
			if gone := errors.Is(found, orgs.ErrNotFound); gone != (err == nil) || (len(page.Tasks) == 0) != gone {
				t.Errorf("after DeleteAgent() = %v: Agent() = %v and %d tasks", err, found, len(page.Tasks))
			}
		})
	}
}
