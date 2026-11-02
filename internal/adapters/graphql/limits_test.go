package graphql

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"

	"cobranch.dev/internal/services/limits"
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
		}, want: []string{`"code":"CONFLICT"`, `"reason":"limit.organizations"`, "You created 1 organization, the most a user may create here."}},
		{name: "a repository over the organization's limit", document: func(f *orgFixture) string {
			return `mutation { createRepository(organizationId: "` + f.acme.ID.String() + `", input: {slug: "tools"}) { id } }`
		}, want: []string{`"code":"CONFLICT"`, `"reason":"limit.repositories"`, "This organization holds 1 repository, the most it may hold."}},
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
