package artifacts_test

import (
	"context"
	"slices"
	"testing"

	"github.com/google/uuid"

	"cobranch.dev/internal/services/artifacts"
	"cobranch.dev/internal/services/auth"
)

// signedIn is ctx with v signed in with a session.
func signedIn(ctx context.Context, v artifacts.Viewer) context.Context {
	return auth.WithViewer(ctx, auth.Authenticated{User: v, Session: auth.Session{ID: uuid.New()}})
}

// granted are the roles principals hold on v, by display name.
func (f *fixture) granted(ctx context.Context, t *testing.T, v artifacts.Viewer) map[string]artifacts.Role {
	t.Helper()
	grants, err := f.service.MyAccess(ctx, v)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]artifacts.Role{}
	for _, g := range grants {
		out[g.Principal.DisplayName] = g.Role
	}
	return out
}

func TestShareMine(t *testing.T) {
	runCases(t, []serviceCase{
		{name: "a member lets another list theirs, then read them", run: func(ctx context.Context, t *testing.T, f *fixture) error {
			ctx = signedIn(ctx, f.rita)
			if err := f.service.ShareMine(ctx, f.rita, f.gwen.ID, artifacts.RoleLister); err != nil {
				return passed(err)
			}
			if err := f.service.ShareMine(ctx, f.rita, f.gwen.ID, artifacts.RoleViewer); err != nil {
				return passed(err)
			}
			if got := f.granted(ctx, t, f.rita); len(got) != 1 || got["Gwen"] != artifacts.RoleViewer {
				t.Errorf("MyAccess() = %v", got)
			}
			return nil
		}},
		{name: "an agent of the member's organization", run: func(ctx context.Context, t *testing.T, f *fixture) error {
			err := f.service.ShareMine(signedIn(ctx, f.rita), f.rita, f.scout.ID, artifacts.RoleViewer)
			if got := f.granted(ctx, t, f.rita); err == nil && got["Scout"] != artifacts.RoleViewer {
				t.Errorf("MyAccess() = %v", got)
			}
			return passed(err)
		}},
		{name: "a person grants nothing more than viewer", wantCode: "artifact.role_invalid", run: func(ctx context.Context, _ *testing.T, f *fixture) error {
			return passed(f.service.ShareMine(signedIn(ctx, f.rita), f.rita, f.gwen.ID, artifacts.RoleEditor))
		}},
		{name: "someone in none of the member's organizations", wantErr: artifacts.ErrNotFound, run: func(ctx context.Context, _ *testing.T, f *fixture) error {
			return passed(f.service.ShareMine(signedIn(ctx, f.rita), f.rita, f.nia.ID, artifacts.RoleViewer))
		}},
		{name: "oneself", wantErr: artifacts.ErrNotFound, run: func(ctx context.Context, _ *testing.T, f *fixture) error {
			return passed(f.service.ShareMine(signedIn(ctx, f.rita), f.rita, f.rita.ID, artifacts.RoleViewer))
		}},
		{name: "a job", wantErr: artifacts.ErrNotFound, run: func(ctx context.Context, _ *testing.T, f *fixture) error {
			return passed(f.service.ShareMine(signedIn(ctx, f.rita), f.rita, f.build, artifacts.RoleViewer))
		}},
		{name: "through a personal token", wantErr: auth.ErrForbidden, run: func(ctx context.Context, _ *testing.T, f *fixture) error {
			ctx = as(ctx, f.rita, auth.Credential{ID: uuid.New(), Kind: auth.CredentialPersonalToken, Permissions: []string{"org.read"}})
			return passed(f.service.ShareMine(ctx, f.rita, f.gwen.ID, artifacts.RoleViewer))
		}},
		{name: "an agent", wantErr: auth.ErrForbidden, run: func(ctx context.Context, _ *testing.T, f *fixture) error {
			return passed(f.service.ShareMine(signedIn(ctx, f.scout), f.scout, f.gwen.ID, artifacts.RoleViewer))
		}},
	})
}

func TestUnshareMine(t *testing.T) {
	runCases(t, []serviceCase{
		{name: "a member takes back what they gave", run: func(ctx context.Context, t *testing.T, f *fixture) error {
			ctx = signedIn(ctx, f.rita)
			if err := f.service.ShareMine(ctx, f.rita, f.gwen.ID, artifacts.RoleViewer); err != nil {
				return passed(err)
			}
			if err := f.service.UnshareMine(ctx, f.rita, f.gwen.ID); err != nil {
				return passed(err)
			}
			if got := f.granted(ctx, t, f.rita); len(got) != 0 {
				t.Errorf("MyAccess() = %v", got)
			}
			return nil
		}},
		{name: "one who holds nothing", wantErr: artifacts.ErrNotFound, run: func(ctx context.Context, _ *testing.T, f *fixture) error {
			return passed(f.service.UnshareMine(signedIn(ctx, f.rita), f.rita, f.gwen.ID))
		}},
		{name: "through a personal token", wantErr: auth.ErrForbidden, run: func(ctx context.Context, _ *testing.T, f *fixture) error {
			f.orgs.Grant(f.gwen.ID, auth.RoleArtifactViewer, auth.ScopeUser, f.rita.ID)
			ctx = as(ctx, f.rita, auth.Credential{ID: uuid.New(), Kind: auth.CredentialPersonalToken})
			return passed(f.service.UnshareMine(ctx, f.rita, f.gwen.ID))
		}},
		{name: "an agent has no artifacts of its own to share", wantErr: auth.ErrForbidden, run: func(ctx context.Context, _ *testing.T, f *fixture) error {
			_, err := f.service.MyAccess(ctx, f.scout)
			return passed(err)
		}},
		{name: "who reads a member's artifacts, by name", run: func(ctx context.Context, t *testing.T, f *fixture) error {
			f.orgs.Grant(f.gwen.ID, auth.RoleArtifactViewer, auth.ScopeUser, f.rita.ID)
			f.orgs.Grant(f.max.ID, auth.RoleArtifactLister, auth.ScopeUser, f.rita.ID)
			grants, err := f.service.MyAccess(ctx, f.rita)
			names := make([]string, 0, len(grants))
			for _, g := range grants {
				names = append(names, g.Principal.DisplayName+" "+string(g.Role))
			}
			if err == nil && !slices.Equal(names, []string{"Gwen viewer", "Max lister"}) {
				t.Errorf("MyAccess() = %v", names)
			}
			return passed(err)
		}},
	})
}
