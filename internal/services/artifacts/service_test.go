package artifacts_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"

	"cobranch.dev/internal/services/artifacts"
	"cobranch.dev/internal/services/artifacts/artifactstest"
	"cobranch.dev/internal/services/auth"
	"cobranch.dev/internal/services/code/codetest"
	"cobranch.dev/internal/services/orgs"
	"cobranch.dev/internal/services/orgs/orgstest"
)

// fixture is Acme, with Olivia its Owner, Max a Member who develops website, Gwen a Guest who reads website, and Rita a
// Member who reads no repository; Nia belongs nowhere. Scout is Acme's agent, a Guest that develops website, and
// Build one of website's jobs. Website keeps two artifacts: Olivia's plan, which only grants reach, and Max's report,
// which website's readers view. Docs is another of Acme's repositories, with no artifact.
type fixture struct {
	orgs                                *orgstest.Store
	store                               *artifactstest.Store
	service                             *artifacts.Service
	acme, website, docs                 uuid.UUID
	olivia, max, gwen, rita, nia, scout artifacts.Viewer
	build, task                         uuid.UUID
	plan, report                        artifacts.Artifact
}

func principal(name string, kind auth.Kind) artifacts.Viewer {
	return artifacts.Viewer{Principal: auth.Principal{ID: uuid.New(), Kind: kind, DisplayName: name}}
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	o := orgstest.NewStore()
	s := artifactstest.NewStore(o)
	f := &fixture{orgs: o, store: s, service: artifacts.NewService(s, auth.NewAuthorizer(o), &codetest.Renderer{}),
		acme: uuid.New(), website: uuid.New(), docs: uuid.New(), build: uuid.New(), task: uuid.New()}
	f.olivia, f.max, f.gwen, f.rita, f.nia = principal("Olivia", auth.KindUser), principal("Max", auth.KindUser), principal("Gwen", auth.KindUser),
		principal("Rita", auth.KindUser), principal("Nia", auth.KindUser)
	f.scout = principal("Scout", auth.KindAgent)
	for _, v := range []artifacts.Viewer{f.olivia, f.max, f.gwen, f.rita, f.nia} {
		o.AddUser(v.ID, v.DisplayName)
	}
	o.Orgs[f.acme] = orgs.Organization{ID: f.acme, Slug: "acme"}
	o.Repos[f.website], o.Repos[f.docs] = f.acme, f.acme
	s.RepoSlugs[f.website], s.RepoSlugs[f.docs] = "website", "docs"
	o.AddMember(f.acme, f.olivia.ID, auth.RoleOwner)
	o.AddMember(f.acme, f.max.ID, auth.RoleMember)
	o.AddMember(f.acme, f.gwen.ID, auth.RoleGuest)
	o.AddMember(f.acme, f.rita.ID, auth.RoleMember)
	o.Grant(f.max.ID, auth.RoleDeveloper, auth.ScopeRepository, f.website)
	o.Grant(f.gwen.ID, auth.RoleReader, auth.ScopeRepository, f.website)
	o.Grant(f.scout.ID, auth.RoleGuest, auth.ScopeOrganization, f.acme)
	o.Grant(f.scout.ID, auth.RoleDeveloper, auth.ScopeRepository, f.website)
	s.Others[f.scout.ID] = artifactstest.Other{OrgID: f.acme, Principal: artifacts.Principal{ID: f.scout.ID, Kind: auth.KindAgent, DisplayName: "Scout"}}
	s.Others[f.build] = artifactstest.Other{OrgID: f.acme, Principal: artifacts.Principal{ID: f.build, Kind: auth.KindJob, DisplayName: "build"}}
	s.Tasks[f.task] = artifactstest.Task{Number: 12, Branch: "scout/report"}
	f.plan = f.seed(t, f.olivia.ID, "Plan", artifacts.ReachNone)
	f.report = f.seed(t, f.max.ID, "Report", artifacts.ReachView)
	return f
}

// seed writes an artifact of website's that owner made and owns, which website's readers reach as far as reach.
func (f *fixture) seed(t *testing.T, owner uuid.UUID, title string, reach artifacts.Reach) artifacts.Artifact {
	t.Helper()
	id := uuid.New()
	a, err := f.store.Create(t.Context(), owner, artifacts.New{
		Artifact: artifacts.Artifact{ID: id, OrgID: f.acme, RepoID: f.website, Owner: artifacts.Principal{ID: owner}, Title: title,
			Kind: artifacts.KindDocument, RepositoryAccess: reach, OrganizationAccess: artifacts.ReachNone},
		First:  artifacts.VersionInput{ID: id, MediaType: artifacts.MediaMarkdown, Body: "# " + title + "\n"},
		Owners: []artifacts.NewGrant{{PrincipalID: owner, RoleID: auth.RoleArtifactOwner}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return a
}

// as is ctx with v signed in through credential.
func as(ctx context.Context, v artifacts.Viewer, credential auth.Credential) context.Context {
	return auth.WithViewer(ctx, auth.Authenticated{User: v, Credential: credential})
}

// onTask is ctx with Scout signed in through a token of the fixture's task, which assigner assigned.
func (f *fixture) onTask(ctx context.Context, assigner uuid.UUID) context.Context {
	return as(ctx, f.scout, auth.Credential{Kind: auth.CredentialTaskToken, PrincipalID: f.scout.ID, Task: auth.TaskGrant{ID: f.task, AssignerID: assigner}})
}

// input is what makes a Markdown document titled title.
func input(title string) artifacts.Input {
	return artifacts.Input{Title: title, Kind: artifacts.KindDocument, MediaType: artifacts.MediaMarkdown, Body: "# " + title + "\n"}
}

// roles are the roles principals hold on the artifact itself, by display name.
func (f *fixture) roles(ctx context.Context, t *testing.T, id uuid.UUID) map[string]artifacts.Role {
	t.Helper()
	grants, err := f.store.Grants(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]artifacts.Role{}
	for _, g := range grants {
		out[g.Principal.DisplayName] = g.Role
	}
	return out
}

// passed is err, as an operation of the service answered it.
func passed(err error) error { return err }

// serviceCase is an operation of the service on the fixture, and the error it should answer: a sentinel error, or a
// validation error by its code.
type serviceCase struct {
	name     string
	run      func(context.Context, *testing.T, *fixture) error
	wantErr  error
	wantCode string
}

func runCases(t *testing.T, tests []serviceCase) {
	t.Helper()
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			err := tc.run(t.Context(), t, f)
			var verr *orgs.ValidationError
			switch {
			case tc.wantCode != "":
				if !errors.As(err, &verr) || verr.Code != tc.wantCode {
					t.Fatalf("err = %v, want a validation error %s", err, tc.wantCode)
				}
			case tc.wantErr == nil:
				if err != nil {
					t.Fatalf("err = %v", err)
				}
			case !errors.Is(err, tc.wantErr):
				t.Fatalf("err = %v, want %v", err, tc.wantErr)
			}
		})
	}
}

func TestCreate(t *testing.T) {
	runCases(t, []serviceCase{
		{name: "a Developer makes one and owns it", run: func(ctx context.Context, t *testing.T, f *fixture) error {
			a, created, err := f.service.Create(ctx, f.max, f.acme, f.website, artifacts.Input{Title: "  Notes ", Kind: artifacts.KindReport,
				RepositoryAccess: artifacts.ReachView, MediaType: artifacts.MediaHTML, Body: "<p>Notes</p>", Note: " first "})
			if err != nil {
				return passed(err)
			}
			v, _ := f.store.Version(ctx, a.ID, 1)
			if !created || a.Title != "Notes" || a.Head != 1 || a.Version != 1 || a.OrganizationAccess != artifacts.ReachNone || a.RepoSlug != "website" ||
				a.Owner.ID != f.max.ID || a.Agent.ID != uuid.Nil || v.ID != a.ID || v.Body != "<p>Notes</p>" || v.Note != "first" || v.Author.ID != f.max.ID {
				t.Errorf("made %+v, %v, first version %+v", a, created, v)
			}
			if got := f.roles(ctx, t, a.ID); len(got) != 1 || got["Max"] != artifacts.RoleOwner {
				t.Errorf("roles = %v", got)
			}
			return nil
		}},
		{name: "a Reader may not make one", wantErr: auth.ErrForbidden, run: func(ctx context.Context, _ *testing.T, f *fixture) error {
			_, _, err := f.service.Create(ctx, f.gwen, f.acme, f.website, input("Notes"))
			return passed(err)
		}},
		{name: "a stranger finds no repository", wantErr: artifacts.ErrNotFound, run: func(ctx context.Context, _ *testing.T, f *fixture) error {
			_, _, err := f.service.Create(ctx, f.nia, f.acme, f.website, input("Notes"))
			return passed(err)
		}},
		{name: "a title of spaces", wantCode: "artifact.title_invalid", run: func(ctx context.Context, _ *testing.T, f *fixture) error {
			_, _, err := f.service.Create(ctx, f.max, f.acme, f.website, input("   "))
			return passed(err)
		}},
		{name: "a body with a NUL", wantCode: "artifact.body_invalid", run: func(ctx context.Context, _ *testing.T, f *fixture) error {
			in := input("Notes")
			in.Body = "a\x00b"
			_, _, err := f.service.Create(ctx, f.max, f.acme, f.website, in)
			return passed(err)
		}},
		{name: "a body over 1 MiB", wantCode: "artifact.body_invalid", run: func(ctx context.Context, _ *testing.T, f *fixture) error {
			in := input("Notes")
			in.Body = strings.Repeat("a", artifacts.MaxBodyBytes+1)
			_, _, err := f.service.Create(ctx, f.max, f.acme, f.website, in)
			return passed(err)
		}},
		{name: "an unknown format", wantCode: "artifact.media_type_invalid", run: func(ctx context.Context, _ *testing.T, f *fixture) error {
			in := input("Notes")
			in.MediaType = "application/javascript"
			_, _, err := f.service.Create(ctx, f.max, f.acme, f.website, in)
			return passed(err)
		}},
		{name: "an unknown audience", wantCode: "artifact.access_invalid", run: func(ctx context.Context, _ *testing.T, f *fixture) error {
			in := input("Notes")
			in.OrganizationAccess = "everyone"
			_, _, err := f.service.Create(ctx, f.max, f.acme, f.website, in)
			return passed(err)
		}},
	})
}

func TestCreateOfNoRepository(t *testing.T) {
	runCases(t, []serviceCase{
		{name: "a member makes one attached to no repository and owns it", run: func(ctx context.Context, t *testing.T, f *fixture) error {
			a, created, err := f.service.Create(ctx, f.gwen, f.acme, uuid.Nil, input("Notes"))
			if err != nil {
				return passed(err)
			}
			if !created || a.RepoID != uuid.Nil || a.RepoSlug != "" || a.Owner.ID != f.gwen.ID || a.RepositoryAccess != artifacts.ReachNone {
				t.Errorf("made %+v, %v", a, created)
			}
			if got := f.roles(ctx, t, a.ID); len(got) != 1 || got["Gwen"] != artifacts.RoleOwner {
				t.Errorf("roles = %v", got)
			}
			return nil
		}},
		{name: "one attached to no repository has no repository audience", wantCode: "artifact.access_invalid",
			run: func(ctx context.Context, _ *testing.T, f *fixture) error {
				in := input("Notes")
				in.RepositoryAccess = artifacts.ReachView
				_, _, err := f.service.Create(ctx, f.max, f.acme, uuid.Nil, in)
				return passed(err)
			}},
		{name: "a stranger finds no organization", wantErr: artifacts.ErrNotFound, run: func(ctx context.Context, _ *testing.T, f *fixture) error {
			_, _, err := f.service.Create(ctx, f.nia, f.acme, uuid.Nil, input("Notes"))
			return passed(err)
		}},
	})
}

func TestCreateAgain(t *testing.T) {
	runCases(t, []serviceCase{
		{name: "the same call run again makes nothing more", run: func(ctx context.Context, t *testing.T, f *fixture) error {
			in := input("Notes")
			in.ID = uuid.New()
			first, _, err := f.service.Create(ctx, f.max, f.acme, f.website, in)
			if err != nil {
				return passed(err)
			}
			again, created, err := f.service.Create(ctx, f.max, f.acme, f.website, in)
			if created || again.ID != first.ID || len(f.store.Kept[first.ID]) != 1 || len(f.store.Artifacts) != 3 {
				t.Errorf("again = %+v, %v; %d artifacts", again, created, len(f.store.Artifacts))
			}
			return passed(err)
		}},
		{name: "an ID another principal took", wantErr: artifacts.ErrExists, run: func(ctx context.Context, _ *testing.T, f *fixture) error {
			in := input("Notes")
			in.ID = f.plan.ID
			_, _, err := f.service.Create(ctx, f.max, f.acme, f.website, in)
			return passed(err)
		}},
		{name: "an ID taken in another repository", wantErr: artifacts.ErrExists, run: func(ctx context.Context, _ *testing.T, f *fixture) error {
			in := input("Notes")
			in.ID = f.plan.ID
			_, _, err := f.service.Create(ctx, f.olivia, f.acme, f.docs, in)
			return passed(err)
		}},
	})
}

func TestCreateOnTask(t *testing.T) {
	runCases(t, []serviceCase{
		{name: "an agent's task makes its assigner the owner and the agent an editor", run: func(ctx context.Context, t *testing.T, f *fixture) error {
			a, _, err := f.service.Create(f.onTask(ctx, f.max.ID), f.scout, f.acme, f.website, input("Findings"))
			if err != nil {
				return passed(err)
			}
			v, _ := f.store.Version(ctx, a.ID, 1)
			if a.Owner.ID != f.max.ID || a.Agent.ID != f.scout.ID || a.TaskID != f.task || a.TaskNumber != 12 || a.Branch != "scout/report" ||
				v.TaskID != f.task {
				t.Errorf("made %+v, first version %+v", a, v)
			}
			if got := f.roles(ctx, t, a.ID); len(got) != 2 || got["Max"] != artifacts.RoleOwner || got["Scout"] != artifacts.RoleEditor {
				t.Errorf("roles = %v", got)
			}
			return nil
		}},
		{name: "a task names its branch only when the call names none", run: func(ctx context.Context, t *testing.T, f *fixture) error {
			in := input("Findings")
			in.Branch = "main"
			a, _, err := f.service.Create(f.onTask(ctx, f.max.ID), f.scout, f.acme, f.website, in)
			if err == nil && a.Branch != "main" {
				t.Errorf("branch = %q", a.Branch)
			}
			return passed(err)
		}},
		{name: "a task Cobranch started makes the agent the owner", run: func(ctx context.Context, t *testing.T, f *fixture) error {
			a, _, err := f.service.Create(f.onTask(ctx, f.scout.ID), f.scout, f.acme, f.website, input("Findings"))
			if err == nil {
				if got := f.roles(ctx, t, a.ID); len(got) != 1 || got["Scout"] != artifacts.RoleOwner || a.Owner.ID != f.scout.ID {
					t.Errorf("owner %+v, roles = %v", a.Owner, got)
				}
			}
			return passed(err)
		}},
		{name: "a task whose assigner may not make one", wantErr: auth.ErrForbidden, run: func(ctx context.Context, _ *testing.T, f *fixture) error {
			_, _, err := f.service.Create(f.onTask(ctx, f.gwen.ID), f.scout, f.acme, f.website, input("Findings"))
			return passed(err)
		}},
	})
}

// titles are the artifacts' titles, in order.
func titles(list []artifacts.Artifact) []string {
	out := make([]string, 0, len(list))
	for _, a := range list {
		out = append(out, a.Title)
	}
	return out
}

func TestList(t *testing.T) {
	tests := []struct {
		name     string
		ctx      func(context.Context, *fixture) context.Context
		viewer   func(*fixture) artifacts.Viewer
		kind     artifacts.Kind
		want     []string
		wantErr  error
		wantCode string
	}{
		{name: "an Owner lists every one, newest change first", viewer: func(f *fixture) artifacts.Viewer { return f.olivia }, want: []string{"Report", "Plan"}},
		{name: "a Reader lists those its readers view", viewer: func(f *fixture) artifacts.Viewer { return f.gwen }, want: []string{"Report"}},
		{name: "of a kind", viewer: func(f *fixture) artifacts.Viewer { return f.olivia }, kind: artifacts.KindDiagram, want: []string{}},
		{name: "of an unknown kind", viewer: func(f *fixture) artifacts.Viewer { return f.olivia }, kind: "movie", wantCode: "artifact.kind_invalid"},
		{name: "a personal token that doesn't list artifacts.read", viewer: func(f *fixture) artifacts.Viewer { return f.olivia }, want: []string{},
			ctx: func(ctx context.Context, f *fixture) context.Context {
				return as(ctx, f.olivia, auth.Credential{ID: uuid.New(), Kind: auth.CredentialPersonalToken, Permissions: []string{"repo.read"}})
			}},
		{name: "an agent's task: those its assigner reads too", viewer: func(f *fixture) artifacts.Viewer { return f.scout }, want: []string{"Report"},
			ctx: func(ctx context.Context, f *fixture) context.Context {
				f.orgs.Grant(f.scout.ID, auth.RoleArtifactViewer, auth.ScopeArtifact, f.plan.ID)
				return f.onTask(ctx, f.gwen.ID)
			}},
		{name: "a Member who reads no repository", viewer: func(f *fixture) artifacts.Viewer { return f.rita }, wantErr: artifacts.ErrNotFound},
		{name: "a stranger", viewer: func(f *fixture) artifacts.Viewer { return f.nia }, wantErr: artifacts.ErrNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture(t)
			f.store.Now = f.store.Now.Add(1)
			if _, err := f.store.Update(t.Context(), f.max.ID, f.report.ID, artifacts.AnyVersion, artifacts.Changes{Title: "Report",
				RepositoryAccess: artifacts.ReachView, OrganizationAccess: artifacts.ReachNone}); err != nil {
				t.Fatal(err)
			}
			ctx := t.Context()
			if tt.ctx != nil {
				ctx = tt.ctx(ctx, f)
			}
			list, err := f.service.List(ctx, tt.viewer(f), f.acme, f.website, tt.kind)
			var verr *orgs.ValidationError
			switch {
			case tt.wantCode != "":
				if !errors.As(err, &verr) || verr.Code != tt.wantCode {
					t.Fatalf("err = %v, want %s", err, tt.wantCode)
				}
			case tt.wantErr != nil:
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("err = %v, want %v", err, tt.wantErr)
				}
			case err != nil || !slices.Equal(titles(list), tt.want):
				t.Fatalf("List() = %v, %v, want %v", titles(list), err, tt.want)
			}
		})
	}
}

func TestRead(t *testing.T) {
	runCases(t, []serviceCase{
		{name: "an Owner holds every permission", run: func(ctx context.Context, t *testing.T, f *fixture) error {
			a, err := f.service.Get(ctx, f.olivia, f.acme, f.report.ID)
			if err == nil && !slices.Equal(a.Permissions, []string{"artifacts.list", "artifacts.manage", "artifacts.read", "artifacts.write"}) {
				t.Errorf("permissions = %v", a.Permissions)
			}
			return passed(err)
		}},
		{name: "a Reader views what its readers view", run: func(ctx context.Context, t *testing.T, f *fixture) error {
			a, err := f.service.Get(ctx, f.gwen, f.acme, f.report.ID)
			if err == nil && !slices.Equal(a.Permissions, []string{"artifacts.read"}) {
				t.Errorf("permissions = %v", a.Permissions)
			}
			return passed(err)
		}},
		{name: "a Reader finds no private one", wantErr: artifacts.ErrNotFound, run: func(ctx context.Context, _ *testing.T, f *fixture) error {
			_, err := f.service.Get(ctx, f.gwen, f.acme, f.plan.ID)
			return passed(err)
		}},
		{name: "the organization's audience reaches a Member who reads no repository", run: func(ctx context.Context, t *testing.T, f *fixture) error {
			view := artifacts.ReachView
			if _, err := f.service.Update(ctx, f.olivia, f.acme, f.plan.ID, artifacts.AnyVersion, artifacts.Patch{OrganizationAccess: &view}); err != nil {
				return passed(err)
			}
			a, err := f.service.Get(ctx, f.rita, f.acme, f.plan.ID)
			if err == nil && (a.Audience() != "organization" || !slices.Equal(a.Permissions, []string{"artifacts.read"})) {
				t.Errorf("got %+v", a)
			}
			return passed(err)
		}},
		{name: "a grant on the artifact alone reaches it", run: func(ctx context.Context, _ *testing.T, f *fixture) error {
			f.orgs.Grant(f.rita.ID, auth.RoleArtifactViewer, auth.ScopeArtifact, f.plan.ID)
			_, err := f.service.Get(ctx, f.rita, f.acme, f.plan.ID)
			return passed(err)
		}},
		{name: "another organization's path", wantErr: artifacts.ErrNotFound, run: func(ctx context.Context, _ *testing.T, f *fixture) error {
			_, err := f.service.Get(ctx, f.olivia, uuid.New(), f.plan.ID)
			return passed(err)
		}},
	})
}

func TestReadVersions(t *testing.T) {
	runCases(t, []serviceCase{
		{name: "the newest version, rendered", run: func(ctx context.Context, t *testing.T, f *fixture) error {
			_, v, err := f.service.Version(ctx, f.gwen, f.acme, f.report.ID, 0)
			if err == nil && (v.Number != 1 || v.Body != "# Report\n" || v.HTML != `<div data-doc="acme/@::artifacts/`+f.report.ID.String()+`"># Report
</div>`) {
				t.Errorf("version = %+v", v)
			}
			return passed(err)
		}},
		{name: "a page renders nothing", run: func(ctx context.Context, t *testing.T, f *fixture) error {
			if _, _, _, err := f.service.AddVersion(ctx, f.max, f.acme, f.report.ID, artifacts.AnyVersion,
				artifacts.VersionInput{MediaType: artifacts.MediaHTML, Body: "<script>alert(1)</script>"}); err != nil {
				return passed(err)
			}
			_, v, err := f.service.Version(ctx, f.gwen, f.acme, f.report.ID, 2)
			if err == nil && (v.HTML != "" || v.Body != "<script>alert(1)</script>") {
				t.Errorf("version = %+v", v)
			}
			return passed(err)
		}},
		{name: "a version that isn't there", wantErr: artifacts.ErrNotFound, run: func(ctx context.Context, _ *testing.T, f *fixture) error {
			_, _, err := f.service.Version(ctx, f.olivia, f.acme, f.report.ID, 2)
			return passed(err)
		}},
		{name: "a private one's versions", wantErr: artifacts.ErrNotFound, run: func(ctx context.Context, _ *testing.T, f *fixture) error {
			_, err := f.service.Versions(ctx, f.gwen, f.acme, f.plan.ID)
			return passed(err)
		}},
	})
}

func TestChange(t *testing.T) {
	title := func(s string) *string { return &s }
	reach := func(r artifacts.Reach) *artifacts.Reach { return &r }
	runCases(t, []serviceCase{
		{name: "an editor retitles it", run: func(ctx context.Context, t *testing.T, f *fixture) error {
			f.orgs.Grant(f.rita.ID, auth.RoleArtifactEditor, auth.ScopeArtifact, f.plan.ID)
			a, err := f.service.Update(ctx, f.rita, f.acme, f.plan.ID, 1, artifacts.Patch{Title: title(" Roadmap "), Summary: title("Q1")})
			if err == nil && (a.Title != "Roadmap" || a.Summary != "Q1" || a.Version != 2 || a.UpdatedBy.ID != f.rita.ID) {
				t.Errorf("changed %+v", a)
			}
			return passed(err)
		}},
		{name: "one attached to no repository gets no repository audience", wantCode: "artifact.access_invalid",
			run: func(ctx context.Context, _ *testing.T, f *fixture) error {
				a, _, err := f.service.Create(ctx, f.rita, f.acme, uuid.Nil, input("Notes"))
				if err != nil {
					return passed(err)
				}
				_, err = f.service.Update(ctx, f.rita, f.acme, a.ID, artifacts.AnyVersion, artifacts.Patch{RepositoryAccess: reach(artifacts.ReachView)})
				return passed(err)
			}},
		{name: "an editor may not change who it reaches", wantErr: auth.ErrForbidden, run: func(ctx context.Context, _ *testing.T, f *fixture) error {
			f.orgs.Grant(f.rita.ID, auth.RoleArtifactEditor, auth.ScopeArtifact, f.plan.ID)
			_, err := f.service.Update(ctx, f.rita, f.acme, f.plan.ID, 1, artifacts.Patch{RepositoryAccess: reach(artifacts.ReachView)})
			return passed(err)
		}},
		{name: "the owner opens it to the repository's readers", run: func(ctx context.Context, _ *testing.T, f *fixture) error {
			if _, err := f.service.Update(ctx, f.olivia, f.acme, f.plan.ID, 1, artifacts.Patch{RepositoryAccess: reach(artifacts.ReachEdit)}); err != nil {
				return passed(err)
			}
			_, _, _, err := f.service.AddVersion(ctx, f.gwen, f.acme, f.plan.ID, artifacts.AnyVersion,
				artifacts.VersionInput{MediaType: artifacts.MediaMarkdown, Body: "# Plan, edited\n"})
			return passed(err)
		}},
		{name: "a viewer may not retitle it", wantErr: auth.ErrForbidden, run: func(ctx context.Context, _ *testing.T, f *fixture) error {
			_, err := f.service.Update(ctx, f.gwen, f.acme, f.report.ID, 1, artifacts.Patch{Title: title("Mine")})
			return passed(err)
		}},
		{name: "one changed since", wantErr: artifacts.ErrChanged, run: func(ctx context.Context, _ *testing.T, f *fixture) error {
			_, err := f.service.Update(ctx, f.olivia, f.acme, f.plan.ID, 7, artifacts.Patch{Title: title("Roadmap")})
			return passed(err)
		}},
		{name: "an unknown audience", wantCode: "artifact.access_invalid", run: func(ctx context.Context, _ *testing.T, f *fixture) error {
			_, err := f.service.Update(ctx, f.olivia, f.acme, f.plan.ID, 1, artifacts.Patch{OrganizationAccess: reach("all")})
			return passed(err)
		}},
		{name: "an empty title", wantCode: "artifact.title_invalid", run: func(ctx context.Context, _ *testing.T, f *fixture) error {
			_, err := f.service.Update(ctx, f.olivia, f.acme, f.plan.ID, 1, artifacts.Patch{Title: title("")})
			return passed(err)
		}},
	})
}

func TestDelete(t *testing.T) {
	runCases(t, []serviceCase{
		{name: "the owner deletes it, its versions and its grants", run: func(ctx context.Context, t *testing.T, f *fixture) error {
			if err := f.service.Delete(ctx, f.olivia, f.acme, f.plan.ID, 1); err != nil {
				return passed(err)
			}
			if _, ok := f.store.Kept[f.plan.ID]; ok || len(f.roles(ctx, t, f.plan.ID)) != 0 {
				t.Errorf("left versions %v and roles %v", f.store.Kept[f.plan.ID], f.roles(ctx, t, f.plan.ID))
			}
			_, err := f.service.Get(ctx, f.olivia, f.acme, f.plan.ID)
			if !errors.Is(err, artifacts.ErrNotFound) {
				t.Errorf("Get() after = %v", err)
			}
			return nil
		}},
		{name: "an editor may not delete it", wantErr: auth.ErrForbidden, run: func(ctx context.Context, _ *testing.T, f *fixture) error {
			f.orgs.Grant(f.rita.ID, auth.RoleArtifactEditor, auth.ScopeArtifact, f.plan.ID)
			return passed(f.service.Delete(ctx, f.rita, f.acme, f.plan.ID, 1))
		}},
		{name: "a deletion decided on an older version", wantErr: artifacts.ErrChanged, run: func(ctx context.Context, _ *testing.T, f *fixture) error {
			return passed(f.service.Delete(ctx, f.olivia, f.acme, f.plan.ID, 2))
		}},
	})
}

// markdown is a Markdown version of body.
func markdown(body string) artifacts.VersionInput {
	return artifacts.VersionInput{MediaType: artifacts.MediaMarkdown, Body: body}
}

func TestAddVersion(t *testing.T) {
	runCases(t, []serviceCase{
		{name: "the owner adds one and keeps the earlier", run: func(ctx context.Context, t *testing.T, f *fixture) error {
			a, v, added, err := f.service.AddVersion(ctx, f.max, f.acme, f.report.ID, 1, artifacts.VersionInput{MediaType: artifacts.MediaSVG,
				Body: "<svg/>", Note: " redrawn "})
			if err != nil {
				return passed(err)
			}
			first, _ := f.store.Version(ctx, f.report.ID, 1)
			if !added || a.Head != 2 || a.Version != 2 || a.MediaType != artifacts.MediaSVG || v.Number != 2 || v.Body != "" || v.Note != "redrawn" ||
				v.Size != 6 || first.Body != "# Report\n" {
				t.Errorf("added %+v to %+v; first %+v", v, a, first)
			}
			list, err := f.service.Versions(ctx, f.gwen, f.acme, f.report.ID)
			if err != nil || len(list) != 2 || list[0].Number != 2 || list[1].Number != 1 || list[0].Body != "" {
				t.Errorf("Versions() = %+v, %v", list, err)
			}
			return nil
		}},
		{name: "the same call run again adds nothing more", run: func(ctx context.Context, t *testing.T, f *fixture) error {
			in := markdown("# Report, again\n")
			in.ID = uuid.New()
			if _, _, _, err := f.service.AddVersion(ctx, f.max, f.acme, f.report.ID, 1, in); err != nil {
				return passed(err)
			}
			a, v, added, err := f.service.AddVersion(ctx, f.max, f.acme, f.report.ID, 1, in)
			if added || v.Number != 2 || a.Head != 2 || len(f.store.Kept[f.report.ID]) != 2 {
				t.Errorf("again %+v, %v", v, added)
			}
			return passed(err)
		}},
		{name: "a version ID another artifact took", wantErr: artifacts.ErrExists, run: func(ctx context.Context, _ *testing.T, f *fixture) error {
			in := markdown("# Report\n")
			in.ID = f.plan.ID
			_, _, _, err := f.service.AddVersion(ctx, f.olivia, f.acme, f.report.ID, 1, in)
			return passed(err)
		}},
		{name: "a viewer may not add one", wantErr: auth.ErrForbidden, run: func(ctx context.Context, _ *testing.T, f *fixture) error {
			_, _, _, err := f.service.AddVersion(ctx, f.gwen, f.acme, f.report.ID, 1, markdown("# Mine\n"))
			return passed(err)
		}},
		{name: "one added meanwhile", wantErr: artifacts.ErrChanged, run: func(ctx context.Context, _ *testing.T, f *fixture) error {
			if _, _, _, err := f.service.AddVersion(ctx, f.max, f.acme, f.report.ID, 1, markdown("# Report 2\n")); err != nil {
				return passed(err)
			}
			_, _, _, err := f.service.AddVersion(ctx, f.olivia, f.acme, f.report.ID, 1, markdown("# Report 2, too\n"))
			return passed(err)
		}},
		{name: "an empty body", wantCode: "artifact.body_invalid", run: func(ctx context.Context, _ *testing.T, f *fixture) error {
			_, _, _, err := f.service.AddVersion(ctx, f.max, f.acme, f.report.ID, 1, markdown(""))
			return passed(err)
		}},
	})
}

func TestAddVersionOnTask(t *testing.T) {
	runCases(t, []serviceCase{
		{name: "an agent's task writes it in the task", run: func(ctx context.Context, t *testing.T, f *fixture) error {
			f.orgs.Grant(f.scout.ID, auth.RoleArtifactEditor, auth.ScopeArtifact, f.report.ID)
			_, v, _, err := f.service.AddVersion(f.onTask(ctx, f.max.ID), f.scout, f.acme, f.report.ID, 1, markdown("# Report, by Scout\n"))
			if err == nil && (v.TaskID != f.task || v.TaskNumber != 12 || v.Author.ID != f.scout.ID) {
				t.Errorf("added %+v", v)
			}
			return passed(err)
		}},
		{name: "an agent's task whose assigner only views it", wantErr: auth.ErrForbidden, run: func(ctx context.Context, _ *testing.T, f *fixture) error {
			f.orgs.Grant(f.scout.ID, auth.RoleArtifactEditor, auth.ScopeArtifact, f.report.ID)
			_, _, _, err := f.service.AddVersion(f.onTask(ctx, f.gwen.ID), f.scout, f.acme, f.report.ID, 1, markdown("# Report, by Scout\n"))
			return passed(err)
		}},
	})
}

func TestShare(t *testing.T) {
	runCases(t, []serviceCase{
		{name: "an artifact grants no lister", wantCode: "artifact.role_invalid", run: func(ctx context.Context, _ *testing.T, f *fixture) error {
			return passed(f.service.Share(ctx, f.olivia, f.acme, f.plan.ID, f.rita.ID, artifacts.RoleLister))
		}},
		{name: "the owner shares it with a Member, who then views it", run: func(ctx context.Context, t *testing.T, f *fixture) error {
			if err := f.service.Share(ctx, f.olivia, f.acme, f.plan.ID, f.rita.ID, artifacts.RoleViewer); err != nil {
				return passed(err)
			}
			access, err := f.service.Access(ctx, f.rita, f.acme, f.plan.ID)
			if err != nil || access.RepositoryAccess != artifacts.ReachNone || len(access.Grants) != 2 || access.Grants[0].Principal.DisplayName != "Olivia" ||
				access.Grants[1].Principal.DisplayName != "Rita" || access.Grants[1].Role != artifacts.RoleViewer {
				t.Errorf("Access() = %+v, %v", access, err)
			}
			return nil
		}},
		{name: "sharing again replaces the role", run: func(ctx context.Context, t *testing.T, f *fixture) error {
			for _, role := range []artifacts.Role{artifacts.RoleEditor, artifacts.RoleViewer} {
				if err := f.service.Share(ctx, f.olivia, f.acme, f.plan.ID, f.rita.ID, role); err != nil {
					return passed(err)
				}
			}
			if got := f.roles(ctx, t, f.plan.ID); got["Rita"] != artifacts.RoleViewer || len(got) != 2 {
				t.Errorf("roles = %v", got)
			}
			return nil
		}},
		{name: "with an agent", run: func(ctx context.Context, _ *testing.T, f *fixture) error {
			return passed(f.service.Share(ctx, f.olivia, f.acme, f.plan.ID, f.scout.ID, artifacts.RoleEditor))
		}},
		{name: "with a job", wantCode: "artifact.grantee_invalid", run: func(ctx context.Context, _ *testing.T, f *fixture) error {
			return passed(f.service.Share(ctx, f.olivia, f.acme, f.plan.ID, f.build, artifacts.RoleViewer))
		}},
		{name: "with someone outside the organization", wantErr: artifacts.ErrNotFound, run: func(ctx context.Context, _ *testing.T, f *fixture) error {
			return passed(f.service.Share(ctx, f.olivia, f.acme, f.plan.ID, f.nia.ID, artifacts.RoleViewer))
		}},
		{name: "as an unknown role", wantCode: "artifact.role_invalid", run: func(ctx context.Context, _ *testing.T, f *fixture) error {
			return passed(f.service.Share(ctx, f.olivia, f.acme, f.plan.ID, f.rita.ID, "admin"))
		}},
		{name: "an editor may not share it", wantErr: auth.ErrForbidden, run: func(ctx context.Context, _ *testing.T, f *fixture) error {
			f.orgs.Grant(f.max.ID, auth.RoleArtifactEditor, auth.ScopeArtifact, f.plan.ID)
			return passed(f.service.Share(ctx, f.max, f.acme, f.plan.ID, f.rita.ID, artifacts.RoleViewer))
		}},
		{name: "the owner of an artifact shares it, whatever they hold on its repository", run: func(ctx context.Context, _ *testing.T, f *fixture) error {
			return passed(f.service.Share(ctx, f.max, f.acme, f.report.ID, f.rita.ID, artifacts.RoleOwner))
		}},
	})
}

func TestUnshare(t *testing.T) {
	runCases(t, []serviceCase{
		{name: "the owner takes it back", run: func(ctx context.Context, t *testing.T, f *fixture) error {
			if err := f.service.Share(ctx, f.olivia, f.acme, f.plan.ID, f.rita.ID, artifacts.RoleViewer); err != nil {
				return passed(err)
			}
			if err := f.service.Unshare(ctx, f.olivia, f.acme, f.plan.ID, f.rita.ID); err != nil {
				return passed(err)
			}
			if _, err := f.service.Get(ctx, f.rita, f.acme, f.plan.ID); !errors.Is(err, artifacts.ErrNotFound) {
				t.Errorf("Get() after = %v", err)
			}
			return nil
		}},
		{name: "taking back what nobody holds", wantErr: artifacts.ErrNotFound, run: func(ctx context.Context, _ *testing.T, f *fixture) error {
			return passed(f.service.Unshare(ctx, f.olivia, f.acme, f.plan.ID, f.rita.ID))
		}},
		{name: "a viewer may not take it back", wantErr: auth.ErrForbidden, run: func(ctx context.Context, _ *testing.T, f *fixture) error {
			return passed(f.service.Unshare(ctx, f.gwen, f.acme, f.report.ID, f.max.ID))
		}},
	})
}
