package code_test

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"cobranch.dev/internal/services/auth"
	"cobranch.dev/internal/services/code"
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
