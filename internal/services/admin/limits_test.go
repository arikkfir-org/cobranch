package admin_test

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"

	"cobranch.dev/internal/services/admin"
	"cobranch.dev/internal/services/auth"
	"cobranch.dev/internal/services/limits"
	"cobranch.dev/internal/services/orgs"
)

// of points at n.
func of(n int64) *int64 { return &n }

// withLimits is f's service under the limits the values set.
func withLimits(f *fixture, values limits.Settings) *admin.Service {
	s := admin.NewService(f.store, auth.NewAuthorizer(f.store), auth.Settings{}).WithLimits(values)
	s.SetClock(func() time.Time { return now })
	return s
}

func TestSettingsLimits(t *testing.T) {
	tests := []struct {
		name       string
		values     limits.Settings
		stored     limits.Settings
		want       limits.Limits
		wantLocked []string
	}{
		{name: "the defaults", want: limits.Defaults},
		{name: "the Back-office's", stored: limits.Settings{RepositoriesPerOrganization: of(500), GitDataPerOrganization: of(0)},
			want: limits.Limits{OrganizationsPerUser: 5, RepositoriesPerOrganization: 500}},
		{name: "the values', locked", values: limits.Settings{OrganizationsPerUser: of(1), GitDataPerOrganization: of(5 << 30)},
			stored:     limits.Settings{OrganizationsPerUser: of(9), RepositoriesPerOrganization: of(7)},
			want:       limits.Limits{OrganizationsPerUser: 1, RepositoriesPerOrganization: 7, GitDataPerOrganization: 5 << 30},
			wantLocked: []string{admin.SettingOrganizationsPerUser, admin.SettingGitDataPerOrganization}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture(t)
			f.store.Installation.Limits = tt.stored
			got, err := withLimits(f, tt.values).Settings(context.Background(), f.ada)
			if err != nil || got.Limits != tt.want || !slices.Equal(got.Locked, tt.wantLocked) {
				t.Errorf("Settings() = %+v, %v; want limits %+v locked %v", got, err, tt.want, tt.wantLocked)
			}
		})
	}
}

func TestChangeSettingsLimits(t *testing.T) {
	tests := []struct {
		name      string
		values    limits.Settings
		change    admin.SettingsChange
		want      limits.Limits
		wantErr   error
		wantCode  string
		wantField string
	}{
		{name: "one", change: admin.SettingsChange{RepositoriesPerOrganization: of(500)},
			want: limits.Limits{OrganizationsPerUser: 5, RepositoriesPerOrganization: 500, GitDataPerOrganization: 1 << 30}},
		{name: "none, with 0", change: admin.SettingsChange{OrganizationsPerUser: of(0), GitDataPerOrganization: of(0)},
			want: limits.Limits{RepositoriesPerOrganization: 100}},
		{name: "one the values leave beside one they lock", values: limits.Settings{OrganizationsPerUser: of(2)},
			change: admin.SettingsChange{GitDataPerOrganization: of(10 << 30)},
			want:   limits.Limits{OrganizationsPerUser: 2, RepositoriesPerOrganization: 100, GitDataPerOrganization: 10 << 30}},
		{name: "one the values lock", values: limits.Settings{GitDataPerOrganization: of(1 << 20)},
			change: admin.SettingsChange{GitDataPerOrganization: of(10 << 30)}, wantErr: admin.ErrLocked},
		{name: "below 0", change: admin.SettingsChange{RepositoriesPerOrganization: of(-1)}, wantCode: "limit.invalid"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture(t)
			s := withLimits(f, tt.values)
			read, err := s.Settings(context.Background(), f.ada)
			if err != nil {
				t.Fatal(err)
			}
			got, err := s.ChangeSettings(context.Background(), f.ada, read.Fingerprint(), tt.change)
			if failed(t, "ChangeSettings", err, tt.wantErr, tt.wantCode) {
				if f.store.Installation.Version != 1 {
					t.Errorf("a refused change was written: %+v", f.store.Installation)
				}
				return
			}
			if got.Limits != tt.want || got.Version != 2 || got.Fingerprint() == read.Fingerprint() {
				t.Errorf("ChangeSettings() = %+v, want limits %+v at version 2, fingerprinted anew", got, tt.want)
			}
			if again, err := s.Settings(context.Background(), f.ada); err != nil || again.Limits != tt.want {
				t.Errorf("Settings() after the change = %+v, %v; want limits %+v", again, err, tt.want)
			}
		})
	}
}

func TestOrganizationLimits(t *testing.T) {
	tests := []struct {
		name        string
		slug        string
		own         admin.OwnLimits
		viewer      func(*fixture) auth.User
		wantApplies limits.Limits
		wantErr     error
	}{
		{name: "none of its own", slug: "acme", wantApplies: limits.Defaults},
		{name: "its own", slug: "acme", own: admin.OwnLimits{Overrides: limits.Overrides{GitData: of(0)}, Version: 3},
			wantApplies: limits.Limits{OrganizationsPerUser: 5, RepositoriesPerOrganization: 100}},
		{name: "an organization that isn't there", slug: "initech", wantErr: orgs.ErrNotFound},
		{name: "not without installation.manage", slug: "acme", viewer: func(f *fixture) auth.User { return f.uma }, wantErr: auth.ErrForbidden},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture(t)
			f.store.Limits[f.acme.ID] = tt.own
			v := f.ada
			if tt.viewer != nil {
				v = tt.viewer(f)
			}
			got, err := f.service.OrganizationLimits(context.Background(), v, tt.slug)
			if failed(t, "OrganizationLimits", err, tt.wantErr, "") {
				return
			}
			if got.ID != f.acme.ID || got.Own != tt.own || got.Installation != limits.Defaults || got.Applies() != tt.wantApplies {
				t.Errorf("OrganizationLimits() = %+v, want its own %+v applying %+v", got, tt.own, tt.wantApplies)
			}
		})
	}
}

func TestChangeOrganizationLimits(t *testing.T) {
	set := func(n int64) admin.LimitChange { return admin.LimitChange{Set: true, Value: &n} }
	cleared := admin.LimitChange{Set: true}
	tests := []struct {
		name     string
		own      admin.OwnLimits
		change   admin.OwnLimitsChange
		stale    bool
		viewer   func(*fixture) auth.User
		want     limits.Overrides
		wantErr  error
		wantCode string
	}{
		{name: "a first limit of its own", change: admin.OwnLimitsChange{Repositories: set(500)}, want: limits.Overrides{Repositories: of(500)}},
		{name: "one cleared, the other kept", own: admin.OwnLimits{Overrides: limits.Overrides{Repositories: of(500), GitData: of(0)}, Version: 2},
			change: admin.OwnLimitsChange{GitData: cleared}, want: limits.Overrides{Repositories: of(500)}},
		{name: "none", own: admin.OwnLimits{Overrides: limits.Overrides{GitData: of(1 << 20)}, Version: 1},
			change: admin.OwnLimitsChange{GitData: set(0)}, want: limits.Overrides{GitData: of(0)}},
		{name: "below 0", change: admin.OwnLimitsChange{GitData: set(-1)}, wantCode: "limit.invalid"},
		{name: "changed meanwhile", stale: true, change: admin.OwnLimitsChange{Repositories: set(500)}, wantErr: admin.ErrStale},
		{name: "not without installation.manage", viewer: func(f *fixture) auth.User { return f.uma }, change: admin.OwnLimitsChange{Repositories: set(500)},
			wantErr: auth.ErrForbidden},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture(t)
			f.store.Limits[f.acme.ID] = tt.own
			read, err := f.service.OrganizationLimits(context.Background(), f.ada, "acme")
			if err != nil {
				t.Fatal(err)
			}
			if tt.stale {
				f.store.Limits[f.acme.ID] = admin.OwnLimits{Version: tt.own.Version + 1}
			}
			got, err := f.service.ChangeOrganizationLimits(context.Background(), viewerOr(f, tt.viewer), "acme", read.Fingerprint(), tt.change)
			if failed(t, "ChangeOrganizationLimits", err, tt.wantErr, tt.wantCode) {
				if slices.Contains(f.store.Events, "org.limits_changed") {
					t.Errorf("a refused change was recorded: %v", f.store.Events)
				}
				return
			}
			checkOwn(t, got, tt.want, tt.own.Version+1, f.ada.ID)
			if got.Fingerprint() == read.Fingerprint() || !slices.Contains(f.store.Events, "org.limits_changed") {
				t.Errorf("ChangeOrganizationLimits() = %+v, events %v; want a new fingerprint and the change recorded", got, f.store.Events)
			}
		})
	}
}

// viewerOr is viewer's user of f, or Ada when there is none.
func viewerOr(f *fixture, viewer func(*fixture) auth.User) auth.User {
	if viewer == nil {
		return f.ada
	}
	return viewer(f)
}

// checkOwn fails t unless got's own limits are want, at version, changed by who now.
func checkOwn(t *testing.T, got admin.OrganizationLimits, want limits.Overrides, version int64, who uuid.UUID) {
	t.Helper()
	same := func(a, b *int64) bool { return (a == nil) == (b == nil) && (a == nil || *a == *b) }
	if !same(got.Own.Repositories, want.Repositories) || !same(got.Own.GitData, want.GitData) || got.Own.Version != version ||
		got.Own.UpdatedBy != who || !got.Own.UpdatedAt.Equal(now) {
		t.Errorf("ChangeOrganizationLimits() = %+v, want its own %+v at version %d by %s", got.Own, want, version, who)
	}
}

func TestChangeOrganizationLimitsAnyVersion(t *testing.T) {
	f := newFixture(t)
	n := int64(7)
	change := admin.OwnLimitsChange{Repositories: admin.LimitChange{Set: true, Value: &n}}
	if got, err := f.service.ChangeOrganizationLimits(context.Background(), f.ada, "acme", "*", change); err != nil || got.Applies().RepositoriesPerOrganization != 7 {
		t.Errorf("ChangeOrganizationLimits(*) = %+v, %v; want 7 repositories", got, err)
	}
	if _, err := f.service.ChangeOrganizationLimits(context.Background(), f.ada, "acme", "not-a-fingerprint", change); !errors.Is(err, admin.ErrStale) {
		t.Errorf("ChangeOrganizationLimits(another fingerprint) error = %v, want ErrStale", err)
	}
	if _, err := f.service.ChangeOrganizationLimits(context.Background(), f.ada, "initech", "*", change); !errors.Is(err, orgs.ErrNotFound) {
		t.Errorf("ChangeOrganizationLimits(initech) error = %v, want ErrNotFound", err)
	}
}

func TestOrganizationsUsage(t *testing.T) {
	f := newFixture(t)
	f.store.Installation.Limits = limits.Settings{RepositoriesPerOrganization: of(50)}
	f.store.Limits[f.acme.ID] = admin.OwnLimits{Overrides: limits.Overrides{GitData: of(10 << 30)}, Version: 1}
	f.store.Repos[uuid.New()], f.store.Repos[uuid.New()] = f.acme.ID, f.acme.ID
	f.store.GitData[f.acme.ID] = 3 << 20
	got, err := f.service.Organizations(context.Background(), f.ada, admin.Page{})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]struct {
		usage  limits.Usage
		limits limits.Limits
	}{
		"acme":   {limits.Usage{Repositories: 2, GitData: 3 << 20}, limits.Limits{OrganizationsPerUser: 5, RepositoriesPerOrganization: 50, GitDataPerOrganization: 10 << 30}},
		"globex": {limits.Usage{}, limits.Limits{OrganizationsPerUser: 5, RepositoriesPerOrganization: 50, GitDataPerOrganization: 1 << 30}},
	}
	for _, o := range got.Items {
		if w := want[o.Slug]; o.Usage != w.usage || o.Limits != w.limits {
			t.Errorf("%s: usage %+v under %+v, want %+v under %+v", o.Slug, o.Usage, o.Limits, w.usage, w.limits)
		}
	}
	created, err := f.service.CreateOrganization(context.Background(), f.ada, "initech", "Initech", "max@example.com")
	if err != nil || created.Limits != want["globex"].limits {
		t.Errorf("CreateOrganization() = %+v, %v; want the installation's limits", created, err)
	}
}
