package limits_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"cobranch.dev/internal/services/limits"
)

// of points at n.
func of(n int64) *int64 { return &n }

func TestResolve(t *testing.T) {
	tests := []struct {
		name       string
		values, db limits.Settings
		want       limits.Limits
	}{
		{name: "none set", want: limits.Defaults},
		{name: "the database's", db: limits.Settings{OrganizationsPerUser: of(2), GitDataPerOrganization: of(0)},
			want: limits.Limits{OrganizationsPerUser: 2, RepositoriesPerOrganization: 100, GitDataPerOrganization: 0}},
		{name: "the values win", values: limits.Settings{OrganizationsPerUser: of(9), RepositoriesPerOrganization: of(0)},
			db:   limits.Settings{OrganizationsPerUser: of(2), RepositoriesPerOrganization: of(7), GitDataPerOrganization: of(5 << 30)},
			want: limits.Limits{OrganizationsPerUser: 9, RepositoriesPerOrganization: 0, GitDataPerOrganization: 5 << 30}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := limits.Resolve(tt.values, tt.db); got != tt.want {
				t.Errorf("Resolve() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestLimitsFor(t *testing.T) {
	installation := limits.Limits{OrganizationsPerUser: 5, RepositoriesPerOrganization: 100, GitDataPerOrganization: 1 << 30}
	tests := []struct {
		name string
		own  limits.Overrides
		want limits.Limits
	}{
		{name: "none of its own", want: installation},
		{name: "its own", own: limits.Overrides{Repositories: of(500), GitData: of(0)},
			want: limits.Limits{OrganizationsPerUser: 5, RepositoriesPerOrganization: 500, GitDataPerOrganization: 0}},
		{name: "one of its own", own: limits.Overrides{GitData: of(10 << 30)},
			want: limits.Limits{OrganizationsPerUser: 5, RepositoriesPerOrganization: 100, GitDataPerOrganization: 10 << 30}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := installation.For(tt.own); got != tt.want {
				t.Errorf("For() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestExceeds(t *testing.T) {
	tests := []struct {
		name                string
		limit, used, adding int64
		want                bool
	}{
		{name: "under", limit: 5, used: 3, adding: 1},
		{name: "up to it", limit: 5, used: 4, adding: 1},
		{name: "over", limit: 5, used: 5, adding: 1, want: true},
		{name: "already over, adding some", limit: 5, used: 7, adding: 1, want: true},
		{name: "already over, adding nothing", limit: 5, used: 7, adding: 0},
		{name: "no limit", limit: 0, used: 1 << 40, adding: 1 << 40},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := limits.Exceeds(tt.limit, tt.used, tt.adding); got != tt.want {
				t.Errorf("Exceeds(%d, %d, %d) = %t, want %t", tt.limit, tt.used, tt.adding, got, tt.want)
			}
		})
	}
}

func TestExceededErrorMessage(t *testing.T) {
	tests := []struct {
		name string
		err  limits.ExceededError
		want string
	}{
		{name: "organizations", err: limits.ExceededError{Kind: limits.KindOrganizations, Limit: 5, Used: 5, Adding: 1},
			want: "You created 5 organizations, the most a user may create here. An administrator can create more for you."},
		{name: "repositories", err: limits.ExceededError{Kind: limits.KindRepositories, Limit: 100, Used: 100, Adding: 1},
			want: "This organization holds 100 repositories, the most it may hold. An administrator can raise its limit."},
		{name: "one organization", err: limits.ExceededError{Kind: limits.KindOrganizations, Limit: 1, Used: 1, Adding: 1},
			want: "You created 1 organization, the most a user may create here. An administrator can create more for you."},
		{name: "one repository", err: limits.ExceededError{Kind: limits.KindRepositories, Limit: 1, Used: 1, Adding: 1},
			want: "This organization holds 1 repository, the most it may hold. An administrator can raise its limit."},
		{name: "Git data", err: limits.ExceededError{Kind: limits.KindGitData, Limit: 1 << 30, Used: 980 << 20, Adding: 50 << 20},
			want: "This organization stores 980 MiB of Git data, and this push adds 50 MiB: over its limit of 1 GiB. Nothing changed; an administrator can raise the limit."},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.err.Error(); got != tt.want {
				t.Errorf("Error() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestBytes(t *testing.T) {
	tests := []struct {
		n    int64
		want string
	}{
		{n: 0, want: "0 B"},
		{n: 300, want: "300 B"},
		{n: 1023, want: "1023 B"},
		{n: 1024, want: "1 KiB"},
		{n: 2560, want: "2.5 KiB"},
		{n: 2047, want: "1.9 KiB"},
		{n: 980 << 20, want: "980 MiB"},
		{n: 10<<20 + 900<<10, want: "10 MiB"},
		{n: 1 << 30, want: "1 GiB"},
		{n: 1<<30 - 1, want: "1023 MiB"},
		{n: 5 << 40, want: "5 TiB"},
	}
	for _, tt := range tests {
		if got := limits.Bytes(tt.n); got != tt.want {
			t.Errorf("Bytes(%d) = %q, want %q", tt.n, got, tt.want)
		}
	}
}

// store is a limits.Store over fixed answers.
type store struct {
	installation limits.Settings
	own          map[uuid.UUID]limits.Overrides
	used         map[uuid.UUID]int64
	err          error
}

func (s *store) InstallationLimits(context.Context) (limits.Settings, error) {
	return s.installation, s.err
}

func (s *store) OrganizationLimits(_ context.Context, orgID uuid.UUID) (limits.Overrides, error) {
	return s.own[orgID], s.err
}

func (s *store) GitData(_ context.Context, orgID uuid.UUID) (int64, error) {
	return s.used[orgID], s.err
}

func TestServiceOrganization(t *testing.T) {
	acme, globex := uuid.New(), uuid.New()
	s := &store{installation: limits.Settings{RepositoriesPerOrganization: of(50)},
		own: map[uuid.UUID]limits.Overrides{acme: {Repositories: of(500)}}}
	tests := []struct {
		name   string
		values limits.Settings
		orgID  uuid.UUID
		want   limits.Limits
	}{
		{name: "its own", orgID: acme, want: limits.Limits{OrganizationsPerUser: 5, RepositoriesPerOrganization: 500, GitDataPerOrganization: 1 << 30}},
		{name: "the installation's", orgID: globex, want: limits.Limits{OrganizationsPerUser: 5, RepositoriesPerOrganization: 50, GitDataPerOrganization: 1 << 30}},
		{name: "its own over the values'", values: limits.Settings{RepositoriesPerOrganization: of(10)}, orgID: acme,
			want: limits.Limits{OrganizationsPerUser: 5, RepositoriesPerOrganization: 500, GitDataPerOrganization: 1 << 30}},
		{name: "the values' over the database's", values: limits.Settings{RepositoriesPerOrganization: of(10)}, orgID: globex,
			want: limits.Limits{OrganizationsPerUser: 5, RepositoriesPerOrganization: 10, GitDataPerOrganization: 1 << 30}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := limits.NewService(s, tt.values).Organization(t.Context(), tt.orgID)
			if err != nil || got != tt.want {
				t.Errorf("Organization() = %+v, %v, want %+v", got, err, tt.want)
			}
		})
	}
	broken := errors.New("broken")
	if _, err := limits.NewService(&store{err: broken}, limits.Settings{}).Organization(t.Context(), acme); !errors.Is(err, broken) {
		t.Errorf("Organization() with a broken store = %v, want %v", err, broken)
	}
}

func TestServiceWeighPush(t *testing.T) {
	acme := uuid.New()
	tests := []struct {
		name      string
		store     *store
		size      int64
		wantLimit int64
		wantErr   *limits.ExceededError
	}{
		{name: "fits", store: &store{used: map[uuid.UUID]int64{acme: 900 << 20}}, size: 100 << 20, wantLimit: 1 << 30},
		{name: "over", store: &store{used: map[uuid.UUID]int64{acme: 1000 << 20}}, size: 100 << 20, wantLimit: 1 << 30,
			wantErr: &limits.ExceededError{Kind: limits.KindGitData, Limit: 1 << 30, Used: 1000 << 20, Adding: 100 << 20}},
		{name: "no limit", store: &store{own: map[uuid.UUID]limits.Overrides{acme: {GitData: of(0)}}, used: map[uuid.UUID]int64{acme: 1 << 40}},
			size: 1 << 40},
		{name: "a limit of its own", store: &store{own: map[uuid.UUID]limits.Overrides{acme: {GitData: of(1 << 20)}}}, size: 2 << 20,
			wantLimit: 1 << 20, wantErr: &limits.ExceededError{Kind: limits.KindGitData, Limit: 1 << 20, Adding: 2 << 20}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			limit, err := limits.NewService(tt.store, limits.Settings{}).WeighPush(t.Context(), acme, tt.size)
			var exceeded *limits.ExceededError
			if errors.As(err, &exceeded) != (tt.wantErr != nil) || (exceeded != nil && *exceeded != *tt.wantErr) || (err != nil && exceeded == nil) {
				t.Fatalf("WeighPush() error = %v, want %v", err, tt.wantErr)
			}
			if limit != tt.wantLimit {
				t.Errorf("WeighPush() = %d, want %d", limit, tt.wantLimit)
			}
		})
	}
}
