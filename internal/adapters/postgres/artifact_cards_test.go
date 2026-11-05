package postgres

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"cobranch.dev/internal/services/artifacts"
	"cobranch.dev/internal/services/chats"
	"cobranch.dev/internal/services/repos"
)

func TestArtifactCards(t *testing.T) {
	t.Parallel()
	f := newArtifactFixture(t)
	ctx := context.Background()
	plan := f.create(t, f.max, "Plan", artifacts.ReachNone, uuid.Nil)
	want := chats.ArtifactRef{ID: plan.ID, OrgID: f.acme.ID, Title: "Plan", Kind: "document"}
	if got, err := f.s.Artifact(ctx, plan.ID); err != nil || got != want {
		t.Errorf("Artifact() = %+v, %v; want %+v", got, err, want)
	}
	if _, err := f.s.Artifact(ctx, uuid.New()); !errors.Is(err, repos.ErrNotFound) {
		t.Errorf("Artifact() of none = %v", err)
	}
	thread, _, err := f.s.StartThread(ctx, chats.Poster{ID: f.max}, f.feature, v7(), v7(), nil, chats.Said{Body: "Wrote the plan.", ArtifactID: plan.ID})
	if err != nil || len(thread.Messages) != 1 || thread.Messages[0].Artifact == nil || *thread.Messages[0].Artifact != want {
		t.Fatalf("StartThread() = %+v, %v", thread, err)
	}
	posted, _, err := f.s.Post(ctx, chats.Poster{ID: f.max}, f.feature, thread.ID, v7(), chats.Said{Body: "Revised it.", ArtifactID: plan.ID})
	if err != nil || posted.Artifact == nil || *posted.Artifact != want {
		t.Errorf("Post() = %+v, %v", posted, err)
	}
	plain, _, err := f.s.Post(ctx, chats.Poster{ID: f.olivia}, f.feature, thread.ID, v7(), chats.Said{Body: "Thanks."})
	if err != nil || plain.Artifact != nil {
		t.Errorf("Post() without a card = %+v, %v", plain, err)
	}
	if err := f.store.Delete(ctx, f.max, plan.ID, artifacts.AnyVersion); err != nil {
		t.Fatal(err)
	}
	read, err := f.s.Thread(ctx, f.feature, thread.ID)
	if err != nil || len(read.Messages) != 3 || read.Messages[0].Artifact != nil || read.Messages[1].Artifact != nil || read.Messages[1].Body != "Revised it." {
		t.Errorf("Thread() once the plan is deleted = %+v, %v; want its messages without cards", read, err)
	}
}
