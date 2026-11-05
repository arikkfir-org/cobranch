package chats_test

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"cobranch.dev/internal/services/auth"
	"cobranch.dev/internal/services/chats"
	"cobranch.dev/internal/services/orgs/orgstest"
	"cobranch.dev/internal/services/repos"
)

// withArtifacts adds the repository's plan, which its Maintainers read, and an artifact of another organization, which
// Dev holds Artifact viewer on; and answers their IDs.
func (f *fixture) withArtifacts() (plan, elsewhere uuid.UUID) {
	plan, elsewhere = uuid.New(), uuid.New()
	other := uuid.New()
	f.store.Artifacts[plan] = chats.ArtifactRef{ID: plan, OrgID: f.feature.OrgID, Title: "Plan", Kind: "document"}
	f.store.Artifacts[elsewhere] = chats.ArtifactRef{ID: elsewhere, OrgID: other, Title: "Theirs", Kind: "report"}
	f.grants.mu.Lock()
	defer f.grants.mu.Unlock()
	f.grants.o.Artifacts[plan] = orgstest.ArtifactScope{OrgID: f.feature.OrgID, RepoID: f.repo, OwnerID: f.mia.ID, RepositoryAccess: "none", OrganizationAccess: "none"}
	f.grants.o.Artifacts[elsewhere] = orgstest.ArtifactScope{OrgID: other, RepositoryAccess: "none", OrganizationAccess: "none"}
	f.grants.o.Grant(f.dev.ID, auth.RoleArtifactViewer, auth.ScopeArtifact, elsewhere)
	return plan, elsewhere
}

func TestCards(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	plan, elsewhere := f.withArtifacts()
	th, err := f.service.StartThread(ctx, f.mia, f.feature, chats.ThreadInput{Body: "Wrote the plan.", ArtifactID: plan})
	if err != nil || len(th.Messages) != 1 || th.Messages[0].Artifact == nil || *th.Messages[0].Artifact != f.store.Artifacts[plan] {
		t.Fatalf("StartThread() = %+v, %v", th, err)
	}
	m, err := f.service.Post(ctx, f.mia, f.feature, th.ID, chats.MessageInput{Body: "Revised it.", ArtifactID: plan})
	if err != nil || m.Artifact == nil || m.Artifact.Title != "Plan" {
		t.Errorf("Post() = %+v, %v", m, err)
	}
	if read, err := f.service.Thread(ctx, f.dev, f.feature, th.ID); err != nil || read.Messages[1].Artifact == nil {
		t.Errorf("Thread() read by one the plan doesn't open for = %+v, %v; want its cards", read, err)
	}
	tests := []struct {
		name     string
		viewer   chats.Viewer
		artifact uuid.UUID
		wantErr  error
	}{
		{name: "one the poster doesn't read", viewer: f.dev, artifact: plan, wantErr: invalid("message.artifact_invalid")},
		{name: "another organization's", viewer: f.dev, artifact: elsewhere, wantErr: invalid("message.artifact_invalid")},
		{name: "one that isn't there", viewer: f.mia, artifact: uuid.New(), wantErr: invalid("message.artifact_invalid")},
		{name: "on a thread that isn't there", viewer: f.mia, artifact: plan, wantErr: repos.ErrNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			thread := th.ID
			if tt.wantErr == repos.ErrNotFound {
				thread = uuid.New()
			}
			if _, err := f.service.Post(ctx, tt.viewer, f.feature, thread, chats.MessageInput{Body: "See this.", ArtifactID: tt.artifact}); !is(err, tt.wantErr) {
				t.Errorf("Post() = %v, want %v", err, tt.wantErr)
			}
			if _, err := f.service.StartThread(ctx, tt.viewer, f.feature, chats.ThreadInput{Body: "See this.", ArtifactID: tt.artifact}); tt.wantErr != repos.ErrNotFound &&
				!is(err, tt.wantErr) {
				t.Errorf("StartThread() = %v, want %v", err, tt.wantErr)
			}
		})
	}
}
