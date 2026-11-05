package code_test

import (
	"bytes"
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"

	"cobranch.dev/internal/services/auth"
	"cobranch.dev/internal/services/code"
	"cobranch.dev/internal/services/orgs"
	"cobranch.dev/internal/services/repos"
)

// TestUploadRender keeps a delivery's render only from its own render pod's token, once, while it renders, and
// within 16 MiB.
func TestUploadRender(t *testing.T) {
	render := []byte("apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: site\n")
	tests := []struct {
		name string
		// as is the credential the request comes with: the delivery's token, another's, a run's, or none.
		as        string
		state     code.DeliveryState
		number    int64
		manifests []byte
		kept      bool
		wantErr   error
	}{
		{name: "its render pod's token, while it renders", as: "its", state: code.DeliveryRendering, number: 1, manifests: render},
		{name: "an empty render", as: "its", state: code.DeliveryRendering, number: 1, manifests: []byte{}},
		{name: "a second upload", as: "its", state: code.DeliveryRendering, number: 1, manifests: render, kept: true,
			wantErr: code.ErrDeliveryNotRendering},
		{name: "a delivery that no longer renders", as: "its", state: code.DeliveryCancelled, number: 1, manifests: render,
			wantErr: code.ErrDeliveryNotRendering},
		{name: "past 16 MiB", as: "its", state: code.DeliveryRendering, number: 1, manifests: bytes.Repeat([]byte("a"), code.MaxRender+1),
			wantErr: code.ErrRenderTooLarge},
		{name: "another delivery's token", as: "another", state: code.DeliveryRendering, number: 1, manifests: render, wantErr: auth.ErrForbidden},
		{name: "a run's token of the same ID", as: "run", state: code.DeliveryRendering, number: 1, manifests: render, wantErr: auth.ErrForbidden},
		{name: "a member who reads the repository", as: "member", state: code.DeliveryRendering, number: 1, manifests: render,
			wantErr: auth.ErrForbidden},
		{name: "a delivery that isn't there", as: "its", state: code.DeliveryRendering, number: 2, manifests: render, wantErr: repos.ErrNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture()
			env := f.store.AddEnvironment(f.repo, code.Environment{Target: code.Principal{ID: uuid.New(), DisplayName: "preview"},
				Branch: "feature/x", Namespace: "website-feature-x"})
			d := f.store.AddDelivery(f.repo, code.Delivery{EnvironmentID: env.ID, Kind: code.DeliverCommit, SHA: sha(6), State: tt.state})
			if tt.kept {
				f.store.Renders[d.ID] = []byte("before")
			}
			ctx, v := context.Background(), f.max
			if tt.as != "member" {
				target := auth.Principal{ID: env.Target.ID, Kind: auth.KindTarget, DisplayName: "preview"}
				c := auth.Credential{ID: d.ID, Kind: auth.CredentialDeliveryToken, PrincipalID: target.ID, RepoID: f.repo,
					Permissions: []string{repos.PermRead}}
				switch tt.as {
				case "another":
					c.ID = uuid.New()
				case "run":
					c.Kind = auth.CredentialRunToken
				}
				ctx, v = auth.WithViewer(ctx, auth.Authenticated{User: auth.User{Principal: target}, Credential: c}), code.Viewer{Principal: target}
			}
			err := f.service.UploadRender(ctx, v, f.repo, tt.number, tt.manifests)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("UploadRender() = %v, want %v", err, tt.wantErr)
			}
			got, ok := f.store.Renders[d.ID]
			if want := tt.wantErr == nil; ok != (want || tt.kept) || want && !bytes.Equal(got, tt.manifests) {
				t.Errorf("kept %q (%t), want %q", got, ok, tt.manifests)
			}
		})
	}
}

// TestClusterNames names a target's cluster, on its environments and among the targets, only to a viewer who may
// manage its secret where it lives: the repository's, or the organization's.
func TestClusterNames(t *testing.T) {
	repoSecret, orgSecret := uuid.New(), uuid.New()
	tests := []struct {
		name  string
		grant func(f *fixture)
		want  []string
	}{
		{name: "a reader", grant: func(*fixture) {}, want: []string{"", ""}},
		{name: "a maintainer of the repository", grant: func(f *fixture) {
			f.orgs.Grant(f.max.ID, auth.RoleMaintainer, auth.ScopeRepository, f.repo)
		}, want: []string{"prod-gke", ""}},
		{name: "an owner of the organization", grant: func(f *fixture) {
			f.orgs.Grant(f.max.ID, auth.RoleOwner, auth.ScopeOrganization, f.orgs.Repos[f.repo])
		}, want: []string{"prod-gke", "staging-gke"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture()
			tt.grant(f)
			org := f.orgs.Repos[f.repo]
			f.store.Clusters = []code.ClusterSecret{{ID: repoSecret, OrgID: org, RepoID: &f.repo, Name: "prod-gke"},
				{ID: orgSecret, OrgID: org, Name: "staging-gke"}}
			f.store.Targets[f.repo] = []code.Target{{Name: "production", Branches: []string{"main"}, Cluster: &repoSecret, Namespace: "shop"},
				{Name: "staging", Branches: []string{"main"}, Cluster: &orgSecret, Namespace: "shop-staging"},
				{Name: "preview", Branches: []string{"feature/**"}, Namespace: "shop-${branch.slug}"}}
			f.store.AddEnvironment(f.repo, code.Environment{Target: code.Principal{ID: uuid.New(), DisplayName: "production"}, Branch: "main",
				Cluster: &repoSecret, Namespace: "shop"})
			f.store.AddEnvironment(f.repo, code.Environment{Target: code.Principal{ID: uuid.New(), DisplayName: "staging"}, Branch: "main",
				Cluster: &orgSecret, Namespace: "shop-staging"})
			targets, err := f.service.DeliveryTargets(context.Background(), f.max, f.repo)
			if err != nil {
				t.Fatal(err)
			}
			envs, err := f.service.Environments(context.Background(), f.max, f.repo, "main")
			if err != nil {
				t.Fatal(err)
			}
			if got := []string{targets[0].ClusterName, targets[1].ClusterName}; !slices.Equal(got, tt.want) || targets[2].ClusterName != "" {
				t.Errorf("the targets' clusters = %q, want %q", got, tt.want)
			}
			if got := []string{envs[0].ClusterName, envs[1].ClusterName}; !slices.Equal(got, tt.want) {
				t.Errorf("the environments' clusters = %q, want %q", got, tt.want)
			}
			one, err := f.service.Environment(context.Background(), f.max, f.repo, envs[1].ID)
			if err != nil || one.ClusterName != tt.want[1] {
				t.Errorf("Environment() = %q, %v, want %q", one.ClusterName, err, tt.want[1])
			}
		})
	}
}

// TestDeliveryKinds lists an environment's deliveries of the kinds asked, and refuses a kind there is none of.
func TestDeliveryKinds(t *testing.T) {
	tests := []struct {
		name    string
		kinds   []code.DeliveryKind
		want    []int64
		wantErr string
	}{
		{name: "every kind", want: []int64{4, 3, 2, 1}},
		{name: "commits and teardowns", kinds: []code.DeliveryKind{code.DeliverCommit, code.Teardown}, want: []int64{4, 2, 1}},
		{name: "resyncs", kinds: []code.DeliveryKind{code.Resync}, want: []int64{3}},
		{name: "a kind there is none of", kinds: []code.DeliveryKind{"rollback"}, wantErr: "kind.invalid"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture()
			env := addDeliveries(f, code.DeliverCommit, code.DeliverCommit, code.Resync, code.Teardown)
			page, err := f.service.Deliveries(context.Background(), f.max, f.repo, env.ID, tt.kinds, 0, 0)
			if tt.wantErr != "" {
				var v *orgs.ValidationError
				if !errors.As(err, &v) || v.Code != tt.wantErr {
					t.Fatalf("Deliveries() = %v, want %s", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got := deliveryNumbers(page.Deliveries); !slices.Equal(got, tt.want) {
				t.Errorf("Deliveries() = %v, want %v", got, tt.want)
			}
		})
	}
}

// addDeliveries adds a preview environment to the fixture's repository, with a delivery of each kind in turn.
func addDeliveries(f *fixture, kinds ...code.DeliveryKind) code.Environment {
	env := f.store.AddEnvironment(f.repo, code.Environment{Target: code.Principal{ID: uuid.New(), DisplayName: "preview"},
		Branch: "feature/x", Namespace: "website-feature-x"})
	for _, k := range kinds {
		f.store.AddDelivery(f.repo, code.Delivery{EnvironmentID: env.ID, Kind: k, State: code.DeliverySucceeded})
	}
	return env
}

// deliveryNumbers lists the deliveries' numbers, in their order.
func deliveryNumbers(deliveries []code.Delivery) []int64 {
	var got []int64
	for _, d := range deliveries {
		got = append(got, d.Number)
	}
	return got
}

// TestEnvironmentObjects lists what an environment holds to a viewer who reads the repository.
func TestEnvironmentObjects(t *testing.T) {
	created := time.Date(2026, 11, 5, 9, 0, 0, 0, time.UTC)
	tests := []struct {
		name    string
		viewer  func(*fixture) code.Viewer
		other   bool
		want    int
		wantErr error
	}{
		{name: "a reader", viewer: func(f *fixture) code.Viewer { return f.max }, want: 2},
		{name: "another environment", viewer: func(f *fixture) code.Viewer { return f.max }, other: true, wantErr: repos.ErrNotFound},
		{name: "a viewer who doesn't read the repository", viewer: func(f *fixture) code.Viewer { return f.nia }, wantErr: repos.ErrNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture()
			env := f.store.AddEnvironment(f.repo, code.Environment{Target: code.Principal{ID: uuid.New(), DisplayName: "preview"},
				Branch: "feature/x", Namespace: "website-feature-x"})
			f.store.Objects[env.ID] = []code.EnvironmentObject{
				{Version: "v1", Kind: "Service", Namespace: "website-feature-x", Name: "api", Health: code.Healthy, CreatedAt: &created, Delivery: 1},
				{Group: "apps", Version: "v1", Kind: "Deployment", Namespace: "website-feature-x", Name: "api", Health: code.Progressing,
					Message: "Ready: 1/2", Ready: "1/2", CreatedAt: &created, Delivery: 1}}
			id := env.ID
			if tt.other {
				id = uuid.New()
			}
			got, err := f.service.EnvironmentObjects(context.Background(), tt.viewer(f), f.repo, id)
			if !errors.Is(err, tt.wantErr) || len(got) != tt.want {
				t.Errorf("EnvironmentObjects() = %d objects, %v, want %d, %v", len(got), err, tt.want, tt.wantErr)
			}
		})
	}
}
