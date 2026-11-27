package postgres

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"cobranch.dev/internal/services/auth"
	"cobranch.dev/internal/services/orgs"
)

func TestOrganizationHub(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		// change changes Acme through the store and answers whom the change wakes: the principal or the organization
		// it names.
		change func(ctx context.Context, t *testing.T, f *orgFixture) uuid.UUID
	}{
		{name: "a member leaves", change: func(ctx context.Context, t *testing.T, f *orgFixture) uuid.UUID {
			if err := f.s.Leave(ctx, f.max, f.acme.ID, f.max, now); err != nil {
				t.Fatal(err)
			}
			return f.max
		}},
		{name: "someone joins", change: func(ctx context.Context, t *testing.T, f *orgFixture) uuid.UUID {
			req := orgs.JoinRequest{ID: uuid.New(), OrgID: f.acme.ID, UserID: f.nia, CreatedAt: now, ExpiresAt: now.Add(time.Hour)}
			if err := f.s.Request(ctx, f.nia, req); err != nil {
				t.Fatal(err)
			}
			if err := f.s.Approve(ctx, f.olivia, req, auth.RoleMember, now.Add(time.Minute)); err != nil {
				t.Fatal(err)
			}
			return f.nia
		}},
		{name: "a role granted", change: func(ctx context.Context, t *testing.T, f *orgFixture) uuid.UUID {
			if err := f.s.GrantRole(ctx, f.olivia, f.acme.ID, f.max, auth.RoleOwner); err != nil {
				t.Fatal(err)
			}
			return f.max
		}},
		{name: "a custom role redefined", change: func(ctx context.Context, t *testing.T, f *orgFixture) uuid.UUID {
			release := orgs.Role{ID: uuid.New(), Name: "Release manager", Permissions: []string{"org.read"}, OrgID: f.acme.ID}
			if err := f.s.CreateRole(ctx, f.olivia, release); err != nil {
				t.Fatal(err)
			}
			release.Permissions = []string{"org.read", "repos.create"}
			if err := f.s.UpdateRole(ctx, f.olivia, release); err != nil {
				t.Fatal(err)
			}
			return f.acme.ID
		}},
		{name: "the organization renamed", change: func(ctx context.Context, t *testing.T, f *orgFixture) uuid.UUID {
			acme := f.acme
			acme.Name = "Acme Corp"
			if _, err := f.s.Update(ctx, f.olivia, acme, orgs.AnyVersion); err != nil {
				t.Fatal(err)
			}
			return f.acme.ID
		}},
		{name: "the organization deleted", change: func(ctx context.Context, t *testing.T, f *orgFixture) uuid.UUID {
			if err := f.s.Delete(ctx, f.olivia, f.acme.ID, orgs.AnyVersion, now); err != nil {
				t.Fatal(err)
			}
			return f.acme.ID
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			hearChange(t, newOrgFixture(t), tt.change, tt.name != "the organization deleted")
		})
	}
}

// hearChange watches Olivia, Max, Nia and Acme on a hub of f's database, makes change, and checks that it wakes those
// who watch what it names and, when quiet, leaves Olivia's watcher alone.
func hearChange(t *testing.T, f *orgFixture, change func(ctx context.Context, t *testing.T, f *orgFixture) uuid.UUID, quiet bool) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	hub := ListenOrganizations(ctx, f.pool)
	watched := map[uuid.UUID]<-chan struct{}{}
	for _, id := range []uuid.UUID{f.olivia, f.max, f.nia, f.acme.ID} {
		wake, stop := hub.Watch(id)
		defer stop()
		watched[id] = wake
	}
	// Every watcher wakes once the hub starts to listen.
	for id, wake := range watched {
		select {
		case <-wake:
		case <-time.After(10 * time.Second):
			t.Fatalf("%s: no wake once the hub listened", id)
		}
	}
	woken := change(ctx, t, f)
	select {
	case <-watched[woken]:
	case <-time.After(10 * time.Second):
		t.Fatal("the change woke nobody who watches what it names")
	}
	if !quiet {
		return
	}
	select {
	case <-watched[f.olivia]:
		t.Error("a change that names someone else woke the Owner's watcher")
	case <-time.After(200 * time.Millisecond):
	}
}
