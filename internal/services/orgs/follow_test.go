package orgs_test

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"

	"cobranch.dev/internal/services/auth"
	"cobranch.dev/internal/services/live/livetest"
	"cobranch.dev/internal/services/orgs"
)

// follower names what a follower sends next, or fails after a few seconds.
type follower func() []string

// following answers the follower of ch, which names each item it sends with name.
func following[T any](t *testing.T, ch <-chan []T, err error, name func(T) string) (follower, error) {
	if err != nil {
		return nil, err
	}
	return func() []string {
		t.Helper()
		select {
		case list, ok := <-ch:
			if !ok {
				t.Fatal("the follower stopped")
			}
			out := []string{}
			for _, item := range list {
				out = append(out, name(item))
			}
			return out
		case <-time.After(5 * time.Second):
			t.Fatal("the follower sent nothing")
			return nil
		}
	}, nil
}

// followCase follows one of Acme's lists, changes it, and checks what the follower sends.
type followCase struct {
	name string
	// follow follows the list as v.
	follow func(ctx context.Context, t *testing.T, f *fixture, v orgs.Viewer) (follower, error)
	// change changes the list through the service, as Olivia.
	change func(ctx context.Context, t *testing.T, f *fixture)
	// shows is what the list names once it changed, or, with gone, no longer names.
	shows func(f *fixture) string
	gone  bool
}

// must fails the test on err.
func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

// followCases are a change to each list Settings follows.
func followCases() []followCase {
	return []followCase{
		{name: "a member removed",
			follow: func(ctx context.Context, t *testing.T, f *fixture, v orgs.Viewer) (follower, error) {
				ch, err := f.service.FollowMembers(ctx, v, f.acme.ID)
				return following(t, ch, err, func(m orgs.Member) string { return m.DisplayName })
			},
			change: func(ctx context.Context, t *testing.T, f *fixture) {
				must(t, f.service.RemoveMember(ctx, f.olivia, f.acme.ID, f.gwen.ID))
			},
			shows: func(*fixture) string { return "Gwen" }, gone: true},
		{name: "someone invited",
			follow: func(ctx context.Context, t *testing.T, f *fixture, v orgs.Viewer) (follower, error) {
				ch, err := f.service.FollowInvitations(ctx, v, f.acme.ID)
				return following(t, ch, err, func(i orgs.Invitation) string { return i.Email })
			},
			change: func(ctx context.Context, t *testing.T, f *fixture) {
				_, err := f.service.Invite(ctx, f.olivia, f.acme.ID, "late@example.com", auth.RoleMember)
				must(t, err)
			},
			shows: func(*fixture) string { return "late@example.com" }},
		{name: "a request to join declined",
			follow: func(ctx context.Context, t *testing.T, f *fixture, v orgs.Viewer) (follower, error) {
				ch, err := f.service.FollowJoinRequests(ctx, v, f.acme.ID)
				return following(t, ch, err, func(r orgs.JoinRequest) string { return r.UserID.String() })
			},
			change: func(ctx context.Context, t *testing.T, f *fixture) {
				must(t, f.service.DeclineJoinRequest(ctx, f.olivia, f.acme.ID, f.request.ID))
			},
			shows: func(f *fixture) string { return f.nia.ID.String() }, gone: true},
		{name: "a role created",
			follow: func(ctx context.Context, t *testing.T, f *fixture, v orgs.Viewer) (follower, error) {
				ch, err := f.service.FollowRoles(ctx, v, f.acme.ID)
				return following(t, ch, err, func(r orgs.Role) string { return r.Name })
			},
			change: func(ctx context.Context, t *testing.T, f *fixture) {
				_, err := f.service.CreateRole(ctx, f.olivia, f.acme.ID, orgs.RoleInput{Name: "Release manager", Permissions: []string{"org.read"}})
				must(t, err)
			},
			shows: func(*fixture) string { return "Release manager" }},
		{name: "an API key made",
			follow: func(ctx context.Context, t *testing.T, f *fixture, v orgs.Viewer) (follower, error) {
				ch, err := f.service.FollowAPIKeys(ctx, v, f.acme.ID)
				return following(t, ch, err, func(k orgs.APIKey) string { return k.Name })
			},
			change: func(ctx context.Context, t *testing.T, f *fixture) {
				in := orgs.APIKeyInput{Name: "Release", RoleIDs: []uuid.UUID{auth.RoleMember}, Expiry: orgs.Expiry{At: now.Add(time.Hour)}}
				_, _, err := f.service.CreateAPIKey(ctx, f.olivia, f.acme.ID, in)
				must(t, err)
			},
			shows: func(*fixture) string { return "Release" }},
	}
}

func TestFollowSettings(t *testing.T) {
	t.Parallel()
	for _, tt := range followCases() {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			f := newFixture(t)
			notifier := &livetest.Notifier{}
			f.service.WithChanges(notifier)
			if _, err := tt.follow(ctx, t, f, f.nia); !errors.Is(err, orgs.ErrNotFound) {
				t.Errorf("an outsider follows the list: %v; want %v", err, orgs.ErrNotFound)
			}
			sent, err := tt.follow(ctx, t, f, f.olivia)
			must(t, err)
			want := tt.shows(f)
			if first := sent(); slices.Contains(first, want) != tt.gone {
				t.Fatalf("first = %v; want it to name %q: %v", first, want, tt.gone)
			}
			tt.change(ctx, t, f)
			notifier.Wake(f.acme.ID)
			if after := sent(); slices.Contains(after, want) == tt.gone {
				t.Errorf("after the change = %v; want it to name %q: %v", after, want, !tt.gone)
			}
		})
	}
}
