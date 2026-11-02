package graphql

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"

	"cobranch.dev/internal/services/auth"
	"cobranch.dev/internal/services/limits"
	"cobranch.dev/internal/services/repos"
)

// fixedLimits are the same limits for the installation and every organization.
type fixedLimits limits.Limits

func (l fixedLimits) Installation(context.Context) (limits.Limits, error) {
	return limits.Limits(l), nil
}

func (l fixedLimits) Organization(context.Context, uuid.UUID) (limits.Limits, error) {
	return limits.Limits(l), nil
}

func TestLimitRefusals(t *testing.T) {
	tests := []struct {
		name     string
		document func(f *orgFixture) string
		want     []string
	}{
		{name: "an organization over the user's limit", document: func(*orgFixture) string {
			return `mutation { createOrganization(slug: "initech", name: "Initech") { id } }`
		}, want: []string{`"code":"CONFLICT"`, `"reason":"limit.organizations"`, "You created 1 organization, and a user may create at most 1 here."}},
		{name: "a repository over the organization's limit", document: func(f *orgFixture) string {
			return `mutation { createRepository(organizationId: "` + f.acme.ID.String() + `", input: {slug: "tools"}) { id } }`
		}, want: []string{`"code":"CONFLICT"`, `"reason":"limit.repositories"`, "This organization holds 1 repository, and it may hold at most 1."}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newOrgFixture() // Acme holds website
			f.store.CreatedBy[uuid.New()] = f.nia.User.ID
			f.service.WithLimits(fixedLimits{OrganizationsPerUser: 1, RepositoriesPerOrganization: 1})
			f.repos.WithLimits(fixedLimits{OrganizationsPerUser: 1, RepositoriesPerOrganization: 1})
			viewer := &f.nia
			if strings.Contains(tt.name, "repository") {
				viewer = &f.olivia
			}
			body := f.query(viewer, tt.document(f))
			for _, w := range tt.want {
				if !strings.Contains(body, w) {
					t.Errorf("body = %s, want %s in it", body, w)
				}
			}
		})
	}
}

func TestUsage(t *testing.T) {
	tests := []struct {
		name     string
		viewer   func(*orgFixture) *auth.Authenticated
		document string
		want     string
	}{
		{name: "the organizations the viewer created, against the limit", viewer: func(f *orgFixture) *auth.Authenticated { return &f.nia },
			document: `{ viewer { usage { organizations { used limit } } } }`,
			want:     `{"data":{"viewer":{"usage":{"organizations":{"used":2,"limit":5}}}}}`},
		{name: "an Owner sees every repository's Git data, past 32 bits", viewer: func(f *orgFixture) *auth.Authenticated { return &f.olivia },
			document: `{ viewer { organizations { usage { repositories { used limit } gitData { used limit } byRepository { repository { slug } gitData } } } } }`,
			want: `{"data":{"viewer":{"organizations":[{"usage":{"repositories":{"used":2,"limit":10},"gitData":{"used":5000000000,"limit":10737418240},` +
				`"byRepository":[{"repository":{"slug":"secret"},"gitData":2000},{"repository":{"slug":"website"},"gitData":4999998000}]}}]}}}`},
		{name: "a member sees the totals, and only the repositories they read", viewer: func(f *orgFixture) *auth.Authenticated { return &f.max },
			document: `{ viewer { organizations { usage { repositories { used } gitData { used } byRepository { repository { slug } } } } } }`,
			want: `{"data":{"viewer":{"organizations":[{"usage":{"repositories":{"used":2},"gitData":{"used":5000000000},` +
				`"byRepository":[{"repository":{"slug":"website"}}]}}]}}}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newOrgFixture() // Acme holds website
			secret := repos.Repository{ID: uuid.New(), OrgID: f.acme.ID, Slug: "secret", Name: "Secret", ObjectFormat: "sha1", CreatedBy: f.olivia.User.ID, CreatedAt: now}
			if err := f.repoStore.Create(t.Context(), f.olivia.User.ID, secret, 0); err != nil {
				t.Fatal(err)
			}
			f.store.Grant(f.max.User.ID, auth.RoleDeveloper, auth.ScopeRepository, f.website.ID)
			f.repoStore.GitData[f.website.ID], f.repoStore.GitData[secret.ID] = 4999998000, 2000
			f.store.CreatedBy[uuid.New()], f.store.CreatedBy[uuid.New()] = f.nia.User.ID, f.nia.User.ID
			l := fixedLimits{OrganizationsPerUser: 5, RepositoriesPerOrganization: 10, GitDataPerOrganization: 10 << 30}
			f.service.WithLimits(l)
			f.repos.WithLimits(l)
			if body := f.query(tt.viewer(f), tt.document); body != tt.want {
				t.Errorf("body = %s, want %s", body, tt.want)
			}
		})
	}
}
