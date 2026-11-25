package orgs_test

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"

	"cobranch.dev/internal/services/auth"
	"cobranch.dev/internal/services/orgs"
	"cobranch.dev/internal/services/orgs/orgstest"
)

// deleted checks that Acme is gone for everyone: by its slugs and its ID, from its members' lists, with nobody's
// grants there but its API key's, whose secrets are gone, and that Globex and Max's place in it are as they were.
func deleted(ctx context.Context, t *testing.T, f *fixture, key orgs.APIKey) {
	t.Helper()
	for _, slug := range []string{"acme", "acme-corp"} {
		if _, err := f.service.Get(ctx, f.olivia, slug); !errors.Is(err, orgs.ErrNotFound) {
			t.Errorf("Get(%s) error = %v, want not found", slug, err)
		}
	}
	if _, err := f.service.Update(ctx, f.olivia, f.acme.ID, orgs.Settings{Slug: "acme", Name: "Acme", MembershipRoleID: auth.RoleMember}); !errors.Is(err, orgs.ErrNotFound) {
		t.Errorf("Update() of a deleted organization error = %v, want not found", err)
	}
	if list, err := f.service.Organizations(ctx, f.max); err != nil || len(list) != 1 || list[0].ID != f.globex.ID {
		t.Errorf("Max's organizations = %+v, %v, want Globex alone", list, err)
	}
	if list, err := f.service.Organizations(ctx, f.olivia); err != nil || len(list) != 0 {
		t.Errorf("Olivia's organizations = %+v, %v, want none", list, err)
	}
	for _, g := range f.store.Grants {
		if g.Scope != auth.ScopeInstallation && g.ScopeID != f.globex.ID && g.Principal != key.ID {
			t.Errorf("grant %+v outlived the deletion", g)
		}
	}
	if k := f.store.Keys[key.ID]; len(k.Secrets) != 0 {
		t.Errorf("the API key kept its secrets %+v", k.Secrets)
	}
	if len(f.store.Invites) != 0 || len(f.store.Reqs) != 0 {
		t.Errorf("invitations %v and join requests %v outlived the deletion", f.store.Invites, f.store.Reqs)
	}
	if d := f.store.Deleting[f.acme.ID]; d.By != f.olivia.ID || !d.At.Equal(now) {
		t.Errorf("deletion = %+v, want by Olivia at %s", d, now)
	}
}

func TestDelete(t *testing.T) {
	tests := []struct {
		name    string
		viewer  func(f *fixture) orgs.Viewer
		org     func(f *fixture) uuid.UUID
		version int64
		wantErr error
	}{
		{name: "the Owner, at its version", viewer: func(f *fixture) orgs.Viewer { return f.olivia }, version: 1},
		{name: "the Owner, at any version", viewer: func(f *fixture) orgs.Viewer { return f.olivia }, version: orgs.AnyVersion},
		{name: "the Owner, at an older version", viewer: func(f *fixture) orgs.Viewer { return f.olivia }, version: 2, wantErr: orgs.ErrChanged},
		{name: "a Member", viewer: func(f *fixture) orgs.Viewer { return f.max }, version: orgs.AnyVersion, wantErr: auth.ErrForbidden},
		{name: "a Manager, who manages members only", viewer: func(f *fixture) orgs.Viewer { return f.mia }, version: orgs.AnyVersion, wantErr: auth.ErrForbidden},
		{name: "an outsider", viewer: func(f *fixture) orgs.Viewer { return f.nia }, version: orgs.AnyVersion, wantErr: orgs.ErrNotFound},
		{name: "an organization that isn't there", viewer: func(f *fixture) orgs.Viewer { return f.olivia },
			org: func(*fixture) uuid.UUID { return uuid.New() }, version: orgs.AnyVersion, wantErr: orgs.ErrNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := t.Context()
			f := newFixture(t)
			key := f.store.AddKey(orgs.APIKey{ID: uuid.New(), OrgID: f.acme.ID, Name: "deploy", CreatedAt: now}, auth.RoleMember)
			repo := uuid.New()
			f.store.Repos[repo] = f.acme.ID
			f.store.Grant(f.gwen.ID, orgstest.Developer.ID, auth.ScopeRepository, repo)
			orgID := f.acme.ID
			if tt.org != nil {
				orgID = tt.org(f)
			}
			err := f.service.Delete(ctx, tt.viewer(f), orgID, tt.version)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("Delete() error = %v, want %v", err, tt.wantErr)
			}
			if tt.wantErr != nil {
				if _, ok := f.store.Orgs[f.acme.ID]; !ok || len(f.store.Deleting) != 0 {
					t.Errorf("a refused Delete() left Acme deleting: %+v", f.store.Deleting)
				}
				return
			}
			deleted(ctx, t, f, key)
		})
	}
}

func TestDeleteFreesTheSlugs(t *testing.T) {
	ctx := t.Context()
	f := newFixture(t)
	if err := f.service.Delete(ctx, f.olivia, f.acme.ID, orgs.AnyVersion); err != nil {
		t.Fatal(err)
	}
	if err := f.service.Delete(ctx, f.olivia, f.acme.ID, orgs.AnyVersion); !errors.Is(err, orgs.ErrNotFound) {
		t.Errorf("deleting it again error = %v, want not found", err)
	}
	for _, slug := range []string{"acme", "acme-corp"} {
		org, err := f.service.Create(ctx, f.nia, slug, "A new "+slug)
		if err != nil || org.ID == f.acme.ID {
			t.Errorf("Create(%s) after the deletion = %+v, %v, want a new organization", slug, org, err)
		}
	}
}

func TestPurge(t *testing.T) {
	tests := []struct {
		name       string
		deletedAgo time.Duration
		tornDown   bool
		fail       error
		want       int
		wantErr    bool
		wantLeft   bool
	}{
		{name: "torn down", deletedAgo: time.Minute, tornDown: true, want: 1},
		{name: "waiting for its teardowns", deletedAgo: time.Hour, wantLeft: true},
		{name: "past the wait, whatever its teardowns do", deletedAgo: orgs.PurgeWait + time.Minute, want: 1},
		{name: "the store fails", deletedAgo: orgs.PurgeWait + time.Minute, fail: errors.New("the database is down"), wantErr: true, wantLeft: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := t.Context()
			f := newFixture(t)
			if err := f.store.Delete(ctx, f.olivia.ID, f.acme.ID, orgs.AnyVersion, now.Add(-tt.deletedAgo)); err != nil {
				t.Fatal(err)
			}
			f.store.TornDown[f.acme.ID], f.store.Fail = tt.tornDown, tt.fail
			n, err := f.service.Purge(ctx)
			if n != tt.want || (err != nil) != tt.wantErr {
				t.Errorf("Purge() = %d, %v, want %d purged, error %t", n, err, tt.want, tt.wantErr)
			}
			if _, left := f.store.Deleting[f.acme.ID]; left != tt.wantLeft {
				t.Errorf("Acme left deleting = %t, want %t", left, tt.wantLeft)
			}
			if _, ok := f.store.Orgs[f.globex.ID]; !ok || !slices.Contains(f.store.Memberships[f.globex.ID], f.max.ID) {
				t.Error("Purge() touched Globex")
			}
		})
	}
}
