package rest

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"cobranch.dev/internal/services/auth"
	"cobranch.dev/internal/services/orgs"
	"cobranch.dev/internal/services/orgs/orgstest"
)

// tagOf is the ETag of the viewer's organizations as the API answers them now.
func tagOf(t *testing.T, f *orgFixture, viewer *auth.Authenticated) string {
	t.Helper()
	rec := f.call(viewer, "GET", "/v1/me/organizations", "")
	if rec.Code != http.StatusOK || rec.Header().Get("ETag") == "" {
		t.Fatalf("my organizations = %d with ETag %q: %s", rec.Code, rec.Header().Get("ETag"), rec.Body)
	}
	return rec.Header().Get("ETag")
}

func TestMyOrganizationsTagged(t *testing.T) {
	current := func(f *orgFixture) http.Header {
		return http.Header{"If-None-Match": {tagOf(t, f, &f.max)}}
	}
	stale := http.Header{"If-None-Match": {`"stale"`}}
	runOrgCases(t, []orgCase{
		{name: "tagged", viewer: maxim, method: "GET", path: fixed("/v1/me/organizations"), wantStatus: 200, wantTag: `"`},
		{name: "still the list the caller holds", viewer: maxim, method: "GET", path: fixed("/v1/me/organizations"), headerFor: current, wantStatus: 304, wantTag: `"`},
		{name: "another list", viewer: maxim, method: "GET", path: fixed("/v1/me/organizations?wait=15"), header: stale, wantStatus: 200, wantTag: `"`},
		{name: "a wait that sees no change", viewer: maxim, method: "GET", path: fixed("/v1/me/organizations?wait=1"), headerFor: current, wantStatus: 304, wantTag: `"`},
		{name: "a wait without a tag answers at once", viewer: maxim, method: "GET", path: fixed("/v1/me/organizations?wait=15"), wantStatus: 200},
		{name: "a wait past 15 seconds", viewer: maxim, method: "GET", path: fixed("/v1/me/organizations?wait=16"), header: stale, wantStatus: 400, wantCode: "wait.invalid"},
		{name: "a negative wait", viewer: maxim, method: "GET", path: fixed("/v1/me/organizations?wait=-1"), header: stale, wantStatus: 400, wantCode: "wait.invalid"},
		{name: "a member who may no longer read it", viewer: maxim, method: "GET", path: fixed("/v1/me/organizations"), wantStatus: 200,
			before: func(f *orgFixture) { f.store.Grants = nil },
			check: func(t *testing.T, _ *orgFixture, body []byte) {
				if got := decode[struct{ Organizations []Organization }](t, body); len(got.Organizations) != 0 {
					t.Errorf("organizations = %s; want none", body)
				}
			}},
	})
}

// lockedStore is the organizations' store a test changes while a request follows it: the reads a follower makes,
// and the test's changes, take its lock.
type lockedStore struct {
	*orgstest.Store
	mu sync.Mutex
}

func (s *lockedStore) OrganizationsOf(ctx context.Context, user uuid.UUID) ([]orgs.Organization, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	list, err := s.Store.OrganizationsOf(ctx, user)
	if err != nil {
		return nil, fmt.Errorf("listing the organizations: %w", err)
	}
	return list, nil
}

func (s *lockedStore) Permissions(ctx context.Context, principal uuid.UUID, scope auth.Scope, scopeID uuid.UUID) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	held, err := s.Store.Permissions(ctx, principal, scope, scopeID)
	if err != nil {
		return nil, fmt.Errorf("reading the permissions: %w", err)
	}
	return held, nil
}

// change changes the store under its lock.
func (s *lockedStore) change(fn func(*orgstest.Store)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	fn(s.Store)
}

func TestMyOrganizationsWaitForAChange(t *testing.T) {
	globex := orgs.Organization{ID: uuid.New(), Slug: "globex", Name: "Globex", MembershipRoleID: auth.RoleMember, CreatedAt: now, Version: 1}
	tests := []struct {
		name   string
		change func(f *orgFixture, s *orgstest.Store) uuid.UUID // answers what the change's notification names
		want   []string
	}{
		{name: "joined one", change: func(f *orgFixture, s *orgstest.Store) uuid.UUID {
			s.Orgs[globex.ID], s.Slugs[globex.Slug] = globex, globex.ID
			s.AddMember(globex.ID, f.max.User.ID, auth.RoleMember)
			return f.max.User.ID
		}, want: []string{"Acme", "Globex"}},
		{name: "renamed one", change: func(f *orgFixture, s *orgstest.Store) uuid.UUID {
			acme := s.Orgs[f.acme.ID]
			acme.Name, acme.Version = "Acme Corp", 2
			s.Orgs[f.acme.ID] = acme
			return f.acme.ID
		}, want: []string{"Acme Corp"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newOrgFixture(t)
			locked := &lockedStore{Store: f.store}
			notifier := &orgstest.Notifier{}
			f.service = orgs.NewService(locked, auth.NewAuthorizer(locked)).WithNotifier(notifier)
			tag := tagOf(t, f, &f.max)
			answered := make(chan *httptest.ResponseRecorder, 1)
			go func() {
				answered <- f.callWith(&f.max, "GET", "/v1/me/organizations?wait=15", "", http.Header{"If-None-Match": {tag}})
			}()
			for notifier.Watching(f.acme.ID) == 0 {
				time.Sleep(10 * time.Millisecond)
			}
			var woken uuid.UUID
			locked.change(func(s *orgstest.Store) { woken = tt.change(f, s) })
			notifier.Wake(woken)
			select {
			case rec := <-answered:
				if rec.Code != http.StatusOK {
					t.Fatalf("status after the change = %d; want 200: %s", rec.Code, rec.Body)
				}
				var got []string
				for _, o := range decode[struct{ Organizations []Organization }](t, rec.Body.Bytes()).Organizations {
					got = append(got, o.Name)
				}
				if !slices.Equal(got, tt.want) {
					t.Errorf("organizations after the change = %v; want %v", got, tt.want)
				}
			case <-time.After(10 * time.Second):
				t.Fatal("no answer after the change")
			}
		})
	}
}
