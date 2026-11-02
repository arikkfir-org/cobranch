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

// scoped is an auth.Grants that answers held at one repository and nothing anywhere else: a job's Secret reader.
type scoped struct {
	repo uuid.UUID
	held []string
}

func (g scoped) Permissions(_ context.Context, _ uuid.UUID, scope auth.Scope, scopeID uuid.UUID) ([]string, error) {
	if scope == auth.ScopeRepository && scopeID == g.repo {
		return slices.Clone(g.held), nil
	}
	return nil, nil
}

func (g scoped) Repositories(_ context.Context, _, _ uuid.UUID, permission string) ([]uuid.UUID, error) {
	if slices.Contains(g.held, permission) {
		return []uuid.UUID{g.repo}, nil
	}
	return nil, nil
}

func TestAuthorizerUnderARunToken(t *testing.T) {
	job, other, org, repo, elsewhere := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	g := scoped{repo: repo, held: []string{"secrets.read"}}
	run := auth.Authenticated{User: auth.User{Principal: auth.Principal{ID: job, Kind: auth.KindJob}},
		Credential: auth.Credential{ID: uuid.New(), Kind: auth.CredentialRunToken, PrincipalID: job, OrgID: org, RepoID: repo,
			Permissions: []string{"annotations.write", "repo.read"}}}
	tests := []struct {
		name      string
		viewer    *auth.Authenticated
		principal uuid.UUID
		scope     auth.Scope
		scopeID   uuid.UUID
		want      []string
	}{
		{name: "its run's repository: its grants and what its job declared", viewer: &run, principal: job, scope: auth.ScopeRepository,
			scopeID: repo, want: []string{"annotations.write", "repo.read", "secrets.read"}},
		{name: "its run's organization: reading it", viewer: &run, principal: job, scope: auth.ScopeOrganization, scopeID: org,
			want: []string{"org.read"}},
		{name: "another repository: nothing", viewer: &run, principal: job, scope: auth.ScopeRepository, scopeID: elsewhere},
		{name: "another organization: nothing", viewer: &run, principal: job, scope: auth.ScopeOrganization, scopeID: uuid.New()},
		{name: "the job without its run's token: its grants alone", principal: job, scope: auth.ScopeRepository, scopeID: repo,
			want: []string{"secrets.read"}},
		{name: "a run's token never widens someone else", viewer: &run, principal: other, scope: auth.ScopeRepository, scopeID: repo,
			want: []string{"secrets.read"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			if tt.viewer != nil {
				ctx = auth.WithViewer(ctx, *tt.viewer)
			}
			held, err := auth.NewAuthorizer(g).Holds(ctx, tt.principal, tt.scope, tt.scopeID)
			if err != nil || !slices.Equal(held, tt.want) {
				t.Errorf("Holds() = %v, %v, want %v", held, err, tt.want)
			}
		})
	}
}

func TestRepositoriesUnderARunToken(t *testing.T) {
	job, org, repo := uuid.New(), uuid.New(), uuid.New()
	ctx := auth.WithViewer(context.Background(), auth.Authenticated{User: auth.User{Principal: auth.Principal{ID: job, Kind: auth.KindJob}},
		Credential: auth.Credential{Kind: auth.CredentialRunToken, OrgID: org, RepoID: repo, Permissions: []string{"repo.read"}}})
	a := auth.NewAuthorizer(scoped{repo: repo, held: []string{"secrets.read"}})
	tests := []struct {
		name       string
		org        uuid.UUID
		permission string
		want       []uuid.UUID
	}{
		{name: "what it declared, in its run's organization", org: org, permission: "repo.read", want: []uuid.UUID{repo}},
		{name: "what its grants give, once", org: org, permission: "secrets.read", want: []uuid.UUID{repo}},
		{name: "what it didn't declare", org: org, permission: "repo.push"},
		{name: "another organization", org: uuid.New(), permission: "repo.read"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := a.Repositories(ctx, job, tt.org, tt.permission)
			if err != nil || !slices.Equal(got, tt.want) {
				t.Errorf("Repositories() = %v, %v, want %v", got, err, tt.want)
			}
		})
	}
}

// byPrincipal is an auth.Grants that answers each principal's own permissions and repositories, the same at every
// scope.
type byPrincipal map[uuid.UUID]grants

func (g byPrincipal) Permissions(ctx context.Context, principalID uuid.UUID, scope auth.Scope, scopeID uuid.UUID) ([]string, error) {
	return g[principalID].Permissions(ctx, principalID, scope, scopeID)
}

func (g byPrincipal) Repositories(ctx context.Context, principalID, orgID uuid.UUID, permission string) ([]uuid.UUID, error) {
	return g[principalID].Repositories(ctx, principalID, orgID, permission)
}

func TestAuthorizerUnderATaskToken(t *testing.T) {
	agent, alice, bob, repo, other := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	g := byPrincipal{
		agent: {held: []string{"org.read", "repo.push", "repo.read"}, repos: []uuid.UUID{repo, other}},
		alice: {held: []string{"org.read", "repo.manage", "repo.read"}, repos: []uuid.UUID{repo}},
		bob:   {held: []string{"org.read"}},
	}
	token := func(assigner uuid.UUID) *auth.Authenticated {
		return &auth.Authenticated{User: auth.User{Principal: auth.Principal{ID: agent, Kind: auth.KindAgent}},
			Credential: auth.Credential{Kind: auth.CredentialTaskToken, PrincipalID: agent, Task: auth.TaskGrant{ID: uuid.New(), AssignerID: assigner}}}
	}
	tests := []struct {
		name      string
		viewer    *auth.Authenticated
		principal uuid.UUID
		wantHeld  []string
		wantRepos []uuid.UUID
		wantCan   map[string]bool
	}{
		{name: "the assigner caps the agent", viewer: token(alice), principal: agent,
			wantHeld: []string{"org.read", "repo.read"}, wantRepos: []uuid.UUID{repo},
			wantCan: map[string]bool{"repo.read": true, "repo.push": false, "repo.manage": false}},
		{name: "an assigner who holds little leaves little", viewer: token(bob), principal: agent,
			wantHeld: []string{"org.read"}, wantCan: map[string]bool{"repo.read": false, "org.read": true}},
		{name: "a task Cobranch started holds the agent's own", viewer: token(agent), principal: agent,
			wantHeld: []string{"org.read", "repo.push", "repo.read"}, wantRepos: []uuid.UUID{repo, other},
			wantCan: map[string]bool{"repo.push": true, "repo.manage": false}},
		{name: "a task whose assigner is gone holds nothing", viewer: token(uuid.Nil), principal: agent,
			wantCan: map[string]bool{"org.read": false, "repo.read": false}},
		{name: "a task token never caps someone else", viewer: token(bob), principal: alice,
			wantHeld: []string{"org.read", "repo.manage", "repo.read"}, wantRepos: []uuid.UUID{repo},
			wantCan: map[string]bool{"repo.manage": true}},
		{name: "the agent without a task token holds its own", principal: agent,
			wantHeld: []string{"org.read", "repo.push", "repo.read"}, wantRepos: []uuid.UUID{repo, other},
			wantCan: map[string]bool{"repo.push": true}},
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

// TestAuthorizerUnderADeliveryToken holds a delivery's token, which acts as its target while it renders, to reading
// its organization and repository, and to uploading its own delivery's render.
func TestAuthorizerUnderADeliveryToken(t *testing.T) {
	target, org, repo, delivery := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	g := scoped{repo: repo, held: []string{"secrets.read"}}
	token := auth.Authenticated{User: auth.User{Principal: auth.Principal{ID: target, Kind: auth.KindTarget}},
		Credential: auth.Credential{ID: delivery, Kind: auth.CredentialDeliveryToken, PrincipalID: target, OrgID: org, RepoID: repo,
			Permissions: []string{"repo.read"}}}
	run := token
	run.Credential.Kind = auth.CredentialRunToken
	tests := []struct {
		name       string
		viewer     *auth.Authenticated
		scope      auth.Scope
		scopeID    uuid.UUID
		want       []string
		uploads    uuid.UUID
		wantUpload bool
	}{
		{name: "its repository: reading it", viewer: &token, scope: auth.ScopeRepository, scopeID: repo,
			want: []string{"repo.read", "secrets.read"}, uploads: delivery, wantUpload: true},
		{name: "its organization: reading it", viewer: &token, scope: auth.ScopeOrganization, scopeID: org, want: []string{"org.read"},
			uploads: delivery, wantUpload: true},
		{name: "another repository: nothing, and not another delivery's render", viewer: &token, scope: auth.ScopeRepository,
			scopeID: uuid.New(), uploads: uuid.New()},
		{name: "a run's token by the same ID uploads nothing", viewer: &run, scope: auth.ScopeRepository, scopeID: repo,
			want: []string{"repo.read", "secrets.read"}, uploads: delivery},
		{name: "no token: its grants alone", scope: auth.ScopeRepository, scopeID: repo, want: []string{"secrets.read"}, uploads: delivery},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			if tt.viewer != nil {
				ctx = auth.WithViewer(ctx, *tt.viewer)
			}
			a := auth.NewAuthorizer(g)
			held, err := a.Holds(ctx, target, tt.scope, tt.scopeID)
			if err != nil || !slices.Equal(held, tt.want) {
				t.Errorf("Holds() = %v, %v, want %v", held, err, tt.want)
			}
			err = a.Renders(ctx, tt.uploads)
			if tt.wantUpload != (err == nil) || err != nil && !errors.Is(err, auth.ErrForbidden) {
				t.Errorf("Renders() = %v, want allowed %t", err, tt.wantUpload)
			}
		})
	}
}
