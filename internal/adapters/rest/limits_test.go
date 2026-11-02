package rest

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
		name, path, body, wantCode, wantDetail string
	}{
		{name: "an organization over the user's limit", path: "/v1/orgs", body: `{"slug":"initech","name":"Initech"}`, wantCode: "limit.organizations",
			wantDetail: "You created 1 organization, the most a user may create here. An administrator can create more for you."},
		{name: "a repository over the organization's limit", path: "/v1/orgs/acme/repos", body: `{"slug":"tools"}`, wantCode: "limit.repositories",
			wantDetail: "This organization holds 1 repository, the most it may hold. An administrator can raise its limit."},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newOrgFixture(t) // Acme holds website
			f.store.CreatedBy[uuid.New()] = f.nia.User.ID
			f.service.WithLimits(fixedLimits{OrganizationsPerUser: 1, RepositoriesPerOrganization: 1})
			f.repos.WithLimits(fixedLimits{OrganizationsPerUser: 1, RepositoriesPerOrganization: 1})
			viewer := &f.nia
			if strings.Contains(tt.path, "repos") {
				viewer = &f.olivia
			}
			rec := f.call(viewer, "POST", tt.path, tt.body)
			if rec.Code != 409 {
				t.Fatalf("status = %d, want 409: %s", rec.Code, rec.Body)
			}
			checkBody(t, rec, "", tt.wantCode)
			if problem := decode[struct{ Detail string }](t, rec.Body.Bytes()); problem.Detail != tt.wantDetail {
				t.Errorf("detail = %q, want %q", problem.Detail, tt.wantDetail)
			}
		})
	}
}
