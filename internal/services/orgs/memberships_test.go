package orgs_test

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"

	"cobranch.dev/internal/services/auth"
	"cobranch.dev/internal/services/orgs"
	"cobranch.dev/internal/services/orgs/orgstest"
)

// shown is a list of memberships as a test compares it: each organization's slug and name, and whether it holds the
// permission probe.
func shown(list []orgs.Membership, probe string) []string {
	out := make([]string, 0, len(list))
	for _, m := range list {
		out = append(out, m.Slug+" "+m.Name+map[bool]string{true: " +" + probe, false: ""}[slices.Contains(m.Permissions, probe)])
	}
	return out
}

// revoke takes back every role user holds at org.
func revoke(s *orgstest.Store, user, org uuid.UUID) {
	s.Grants = slices.DeleteFunc(s.Grants, func(g orgstest.Grant) bool {
		return g.Principal == user && g.Scope == auth.ScopeOrganization && g.ScopeID == org
	})
}

// rename names an organization anew, at its next version, and answers its ID, which the rename's notification names.
func rename(f *fixture, id uuid.UUID, name string) uuid.UUID {
	o := f.store.Orgs[id]
	o.Name, o.Version = name, o.Version+1
	f.store.Orgs[id] = o
	return id
}

func TestMemberships(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		viewer func(f *fixture) orgs.Viewer
		change func(f *fixture)
		want   []string
	}{
		{name: "an Owner", viewer: func(f *fixture) orgs.Viewer { return f.olivia }, want: []string{"acme Acme +org.manage"}},
		{name: "a member of two", viewer: func(f *fixture) orgs.Viewer { return f.max }, want: []string{"acme Acme", "globex Globex +org.manage"}},
		{name: "nobody's member", viewer: func(f *fixture) orgs.Viewer { return f.nia }, want: []string{}},
		{
			name:   "a member who may no longer read one",
			viewer: func(f *fixture) orgs.Viewer { return f.max },
			change: func(f *fixture) { revoke(f.store, f.max.ID, f.acme.ID) },
			want:   []string{"globex Globex +org.manage"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			f := newFixture(t)
			if tt.change != nil {
				tt.change(f)
			}
			got, err := f.service.Memberships(context.Background(), tt.viewer(f))
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(shown(got, orgs.PermManage), tt.want) {
				t.Errorf("Memberships() = %v; want %v", shown(got, orgs.PermManage), tt.want)
			}
		})
	}
}

func TestFollowMemberships(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		viewer func(f *fixture) orgs.Viewer
		change func(f *fixture) uuid.UUID // changes the store and answers what the change's notification names
		want   []string                   // the list sent next; nil for none
	}{
		{
			name:   "joining an organization",
			viewer: func(f *fixture) orgs.Viewer { return f.olivia },
			change: func(f *fixture) uuid.UUID {
				f.store.AddMember(f.globex.ID, f.olivia.ID, auth.RoleGuest)
				return f.olivia.ID
			},
			want: []string{"acme Acme +org.manage", "globex Globex"},
		},
		{
			name:   "leaving one",
			viewer: func(f *fixture) orgs.Viewer { return f.olivia },
			change: func(f *fixture) uuid.UUID {
				f.store.Memberships[f.acme.ID] = slices.DeleteFunc(f.store.Memberships[f.acme.ID], func(u uuid.UUID) bool { return u == f.olivia.ID })
				revoke(f.store, f.olivia.ID, f.acme.ID)
				return f.olivia.ID
			},
			want: []string{},
		},
		{
			name:   "one renamed by someone else",
			viewer: func(f *fixture) orgs.Viewer { return f.max },
			change: func(f *fixture) uuid.UUID { return rename(f, f.globex.ID, "Globex Corp") },
			want:   []string{"acme Acme", "globex Globex Corp +org.manage"},
		},
		{
			name:   "a role granted",
			viewer: func(f *fixture) orgs.Viewer { return f.max },
			change: func(f *fixture) uuid.UUID {
				f.store.Grant(f.max.ID, auth.RoleOwner, auth.ScopeOrganization, f.acme.ID)
				return f.max.ID
			},
			want: []string{"acme Acme +org.manage", "globex Globex +org.manage"},
		},
		{
			name:   "a wake that changed nothing",
			viewer: func(f *fixture) orgs.Viewer { return f.max },
			change: func(f *fixture) uuid.UUID { return f.acme.ID },
		},
		{
			name:   "a change to an organization the viewer isn't in",
			viewer: func(f *fixture) orgs.Viewer { return f.olivia },
			change: func(f *fixture) uuid.UUID { return rename(f, f.globex.ID, "Globex Corp") },
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			f := newFixture(t)
			followOnce(t, f, tt.viewer(f), tt.change, tt.want)
		})
	}
}

// followOnce follows v's memberships, checks the first list and what the follower watches, makes change, wakes those
// who watch what it names and checks the list sent next (none when want is nil), then ends the follower.
func followOnce(t *testing.T, f *fixture, v orgs.Viewer, change func(f *fixture) uuid.UUID, want []string) {
	t.Helper()
	notifier := &orgstest.Notifier{}
	f.service.WithNotifier(notifier)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	lists, err := f.service.FollowMemberships(ctx, v)
	if err != nil {
		t.Fatal(err)
	}
	first, err := f.service.Memberships(ctx, v)
	if err != nil {
		t.Fatal(err)
	}
	if got := <-lists; !slices.Equal(shown(got, orgs.PermManage), shown(first, orgs.PermManage)) {
		t.Errorf("first list = %v; want %v", shown(got, orgs.PermManage), shown(first, orgs.PermManage))
	}
	for _, m := range first {
		if n := notifier.Watching(m.ID); n != 1 {
			t.Errorf("watchers of %s = %d; want 1", m.Slug, n)
		}
	}
	notifier.Wake(change(f))
	wait := 5 * time.Second
	if want == nil {
		wait = 200 * time.Millisecond
	}
	select {
	case got := <-lists:
		if want == nil || !slices.Equal(shown(got, orgs.PermManage), want) {
			t.Errorf("next list = %v; want %v", shown(got, orgs.PermManage), want)
		}
	case <-time.After(wait):
		if want != nil {
			t.Fatalf("no list after the change; want %v", want)
		}
	}
	cancel()
	for range lists {
	}
	if n := notifier.Watching(v.ID); n != 0 {
		t.Errorf("watchers of the viewer after the end = %d; want 0", n)
	}
}

// TestFollowMembershipsWatchesNewOrganizations checks that a follower watches an organization from the list that
// shows it on: a rename of one the viewer just joined wakes it.
func TestFollowMembershipsWatchesNewOrganizations(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	notifier := &orgstest.Notifier{}
	f.service.WithNotifier(notifier)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	lists, err := f.service.FollowMemberships(ctx, f.olivia)
	if err != nil {
		t.Fatal(err)
	}
	<-lists
	f.store.AddMember(f.globex.ID, f.olivia.ID, auth.RoleGuest)
	notifier.Wake(f.olivia.ID)
	<-lists
	notifier.Wake(rename(f, f.globex.ID, "Globex Corp"))
	select {
	case got := <-lists:
		if want := []string{"acme Acme", "globex Globex Corp"}; !slices.Equal(shown(got, ""), want) {
			t.Errorf("list after the rename = %v; want %v", shown(got, ""), want)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("a rename of an organization the viewer joined while following sent nothing")
	}
}
