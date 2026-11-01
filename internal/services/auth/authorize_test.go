package auth_test

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/google/uuid"

	"cobranch.dev/internal/services/auth"
)

// grants is an auth.Grants that answers the same permissions, and the same repositories, for every principal and scope.
type grants struct {
	held  []string
	repos []uuid.UUID
}

func (g grants) Permissions(context.Context, uuid.UUID, auth.Scope, uuid.UUID) ([]string, error) {
	return slices.Clone(g.held), nil
}

func (g grants) Repositories(_ context.Context, _, _ uuid.UUID, permission string) ([]uuid.UUID, error) {
	if !slices.Contains(g.held, permission) {
		return nil, nil
	}
	return g.repos, nil
}

func TestAuthorizerUnderAToken(t *testing.T) {
	user, other, repo := uuid.New(), uuid.New(), uuid.New()
	g := grants{held: []string{"repo.push", "repo.read", "repo.manage", "org.read"}, repos: []uuid.UUID{repo}}
	token := func(permissions ...string) *auth.Authenticated {
		return &auth.Authenticated{User: auth.User{Principal: auth.Principal{ID: user, Kind: auth.KindUser}},
			Credential: auth.Credential{ID: uuid.New(), Kind: auth.CredentialPersonalToken, Permissions: permissions}}
	}
	tests := []struct {
		name      string
		viewer    *auth.Authenticated
		principal uuid.UUID
		wantHeld  []string
		wantRepos []uuid.UUID
		wantCan   map[string]bool
	}{
		{name: "a session holds every grant", viewer: &auth.Authenticated{User: auth.User{Principal: auth.Principal{ID: user}}, Session: auth.Session{ID: uuid.New()}},
			principal: user, wantHeld: []string{"org.read", "repo.manage", "repo.push", "repo.read"}, wantRepos: []uuid.UUID{repo},
			wantCan: map[string]bool{"repo.manage": true, "repo.push": true}},
		{name: "no viewer holds every grant", principal: user, wantHeld: []string{"org.read", "repo.manage", "repo.push", "repo.read"}, wantRepos: []uuid.UUID{repo},
			wantCan: map[string]bool{"repo.manage": true}},
		{name: "a token holds what both give", viewer: token("repo.read", "repo.push", "secrets.manage"), principal: user,
			wantHeld: []string{"repo.push", "repo.read"}, wantRepos: []uuid.UUID{repo},
			wantCan: map[string]bool{"repo.push": true, "repo.manage": false, "secrets.manage": false}},
		{name: "a token without the permission lists no repositories", viewer: token("org.read"), principal: user,
			wantHeld: []string{"org.read"}, wantCan: map[string]bool{"repo.read": false, "org.read": true}},
		{name: "a token never narrows someone else", viewer: token("repo.read"), principal: other,
			wantHeld: []string{"org.read", "repo.manage", "repo.push", "repo.read"}, wantRepos: []uuid.UUID{repo},
			wantCan: map[string]bool{"repo.manage": true}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			if tt.viewer != nil {
				ctx = auth.WithViewer(ctx, *tt.viewer)
			}
			a := auth.NewAuthorizer(g)
			held, err := a.Holds(ctx, tt.principal, auth.ScopeRepository, repo)
			if err != nil || !slices.Equal(held, tt.wantHeld) {
				t.Errorf("Holds() = %v, %v, want %v", held, err, tt.wantHeld)
			}
			repos, err := a.Repositories(ctx, tt.principal, uuid.New(), "repo.read")
			if err != nil || !slices.Equal(repos, tt.wantRepos) {
				t.Errorf("Repositories() = %v, %v, want %v", repos, err, tt.wantRepos)
			}
			checkCan(ctx, t, a, tt.principal, repo, tt.wantCan)
		})
	}
}

// checkCan checks that a allows principalID each permission of want on the repository, or refuses it, as want says.
func checkCan(ctx context.Context, t *testing.T, a *auth.Authorizer, principalID, repo uuid.UUID, want map[string]bool) {
	t.Helper()
	for permission, allowed := range want {
		err := a.Can(ctx, principalID, permission, auth.ScopeRepository, repo)
		if got := err == nil; got != allowed || (!allowed && !errors.Is(err, auth.ErrForbidden)) {
			t.Errorf("Can(%s) = %v, want allowed %t", permission, err, allowed)
		}
	}
}
