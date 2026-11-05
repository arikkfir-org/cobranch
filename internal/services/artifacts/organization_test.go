package artifacts_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"

	"cobranch.dev/internal/services/artifacts"
	"cobranch.dev/internal/services/auth"
	"cobranch.dev/internal/services/orgs"
)

// listed is an artifact as a list shows it: its title, and which of artifacts.list and artifacts.read the viewer holds.
type listed struct {
	title       string
	permissions []string
}

var (
	reads   = []string{"artifacts.read"}
	lists   = []string{"artifacts.list"}
	owns    = []string{"artifacts.list", "artifacts.read"}
	nothing = []listed{}
)

// listedOf is list as listed.
func listedOf(list []artifacts.Artifact) []listed {
	out := make([]listed, 0, len(list))
	for _, a := range list {
		out = append(out, listed{title: a.Title, permissions: a.Permissions})
	}
	return out
}

func equalListed(a, b []listed) bool {
	return slices.EqualFunc(a, b, func(x, y listed) bool { return x.title == y.title && slices.Equal(x.permissions, y.permissions) })
}

// listCase lists the organization's artifacts as viewer, through ctx when it is given, with filter, after the
// fixture's Rita writes Notes, attached to no repository, and Max's Report changes last.
type listCase struct {
	name     string
	ctx      func(context.Context, *fixture) context.Context
	viewer   func(*fixture) artifacts.Viewer
	filter   func(*fixture) artifacts.Filter
	want     []listed
	wantErr  error
	wantCode string
}

func TestListOrganization(t *testing.T) {
	runListCases(t, []listCase{
		{name: "an Owner lists those attached to its repositories", viewer: func(f *fixture) artifacts.Viewer { return f.olivia },
			want: []listed{{"Report", owns}, {"Plan", owns}}},
		{name: "a member who reads no repository lists their own", viewer: func(f *fixture) artifacts.Viewer { return f.rita },
			want: []listed{{"Notes", owns}}},
		{name: "a Developer lists their own and those its readers view", viewer: func(f *fixture) artifacts.Viewer { return f.max },
			want: []listed{{"Report", owns}}},
		{name: "mine", viewer: func(f *fixture) artifacts.Viewer { return f.olivia }, filter: func(*fixture) artifacts.Filter { return artifacts.Filter{Mine: true} },
			want: []listed{{"Plan", owns}}},
		{name: "attached to a repository", viewer: func(f *fixture) artifacts.Viewer { return f.olivia },
			filter: func(f *fixture) artifacts.Filter { return artifacts.Filter{RepoID: f.docs} }, want: nothing},
		{name: "of a kind", viewer: func(f *fixture) artifacts.Viewer { return f.olivia },
			filter: func(*fixture) artifacts.Filter { return artifacts.Filter{Kind: artifacts.KindPage} }, want: nothing},
		{name: "of an unknown kind", viewer: func(f *fixture) artifacts.Viewer { return f.olivia },
			filter: func(*fixture) artifacts.Filter { return artifacts.Filter{Kind: "movie"} }, wantCode: "artifact.kind_invalid"},
		{name: "one listed through a grant on its owner doesn't open", viewer: func(f *fixture) artifacts.Viewer { return f.max },
			ctx: func(ctx context.Context, f *fixture) context.Context {
				f.orgs.Grant(f.max.ID, auth.RoleArtifactLister, auth.ScopeUser, f.rita.ID)
				return ctx
			},
			want: []listed{{"Report", owns}, {"Notes", lists}}},
		{name: "one read through a grant on its owner", viewer: func(f *fixture) artifacts.Viewer { return f.gwen },
			ctx: func(ctx context.Context, f *fixture) context.Context {
				f.orgs.Grant(f.gwen.ID, auth.RoleArtifactViewer, auth.ScopeUser, f.rita.ID)
				return ctx
			},
			want: []listed{{"Report", reads}, {"Notes", reads}}},
		{name: "a grant on an owner reaches no one outside the organization", viewer: func(f *fixture) artifacts.Viewer { return f.nia },
			ctx: func(ctx context.Context, f *fixture) context.Context {
				f.orgs.Grant(f.nia.ID, auth.RoleArtifactViewer, auth.ScopeUser, f.rita.ID)
				return ctx
			},
			wantErr: artifacts.ErrNotFound},
		{name: "a personal token that lists artifacts.list alone", viewer: func(f *fixture) artifacts.Viewer { return f.olivia },
			ctx: func(ctx context.Context, f *fixture) context.Context {
				return as(ctx, f.olivia, auth.Credential{ID: uuid.New(), Kind: auth.CredentialPersonalToken, Permissions: []string{"org.read", "artifacts.list"}})
			},
			want: []listed{{"Report", lists}, {"Plan", lists}}},
	})
}

func runListCases(t *testing.T, tests []listCase) {
	t.Helper()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture(t)
			ctx := t.Context()
			if _, _, err := f.service.Create(ctx, f.rita, f.acme, uuid.Nil, input("Notes")); err != nil {
				t.Fatal(err)
			}
			f.store.Now = f.store.Now.Add(1)
			if _, err := f.store.Update(ctx, f.max.ID, f.report.ID, artifacts.AnyVersion, artifacts.Changes{Title: "Report",
				RepositoryAccess: artifacts.ReachView, OrganizationAccess: artifacts.ReachNone}); err != nil {
				t.Fatal(err)
			}
			if tt.ctx != nil {
				ctx = tt.ctx(ctx, f)
			}
			var filter artifacts.Filter
			if tt.filter != nil {
				filter = tt.filter(f)
			}
			list, err := f.service.ListOrganization(ctx, tt.viewer(f), f.acme, filter)
			tt.check(t, list, err)
		})
	}
}

// check fails t unless list and err are what the case expects.
func (tt listCase) check(t *testing.T, list []artifacts.Artifact, err error) {
	t.Helper()
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
	case err != nil || !equalListed(listedOf(list), tt.want):
		t.Fatalf("ListOrganization() = %v, %v, want %v", listedOf(list), err, tt.want)
	}
}

func TestListedWithoutRead(t *testing.T) {
	runCases(t, []serviceCase{
		{name: "the repository's list shows one its viewer may list", run: func(ctx context.Context, t *testing.T, f *fixture) error {
			f.orgs.Grant(f.gwen.ID, auth.RoleArtifactLister, auth.ScopeRepository, f.website)
			list, err := f.service.List(ctx, f.gwen, f.acme, f.website, "")
			slices.SortFunc(list, func(a, b artifacts.Artifact) int { return strings.Compare(a.Title, b.Title) })
			if want := []listed{{"Plan", lists}, {"Report", owns}}; err == nil && !equalListed(listedOf(list), want) {
				t.Errorf("List() = %v, want %v", listedOf(list), want)
			}
			return passed(err)
		}},
		{name: "one listed opens to nobody who may only list it", wantErr: artifacts.ErrNotFound, run: func(ctx context.Context, _ *testing.T, f *fixture) error {
			f.orgs.Grant(f.gwen.ID, auth.RoleArtifactLister, auth.ScopeRepository, f.website)
			_, err := f.service.Get(ctx, f.gwen, f.acme, f.plan.ID)
			return passed(err)
		}},
	})
}
