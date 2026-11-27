package agents_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"

	"cobranch.dev/internal/services/agents"
	"cobranch.dev/internal/services/live/livetest"
	"cobranch.dev/internal/services/orgs"
)

// followed connects provider anthropic with the organization's own secret, whose ID it answers, and adds its model
// claude-opus-5-5.
func (f *fixture) followed(t *testing.T) uuid.UUID {
	t.Helper()
	key, _, _ := f.withProviders()
	ctx := context.Background()
	if _, _, err := f.service.SetModelProvider(ctx, f.olive, f.org, "anthropic", key, agents.NoVersion); err != nil {
		t.Fatal(err)
	}
	if _, _, err := f.service.PutModel(ctx, f.olive, f.org, "anthropic", "claude-opus-5-5", agents.ModelInput{}, agents.NoVersion); err != nil {
		t.Fatal(err)
	}
	return key
}

// next names what a follower of the organization's providers or models sends next, or fails after a few seconds.
func next[T any](t *testing.T, ch <-chan []T, name func(T) string) []string {
	t.Helper()
	select {
	case list, ok := <-ch:
		if !ok {
			t.Fatal("the follower stopped")
		}
		out := []string{}
		for _, item := range list {
			out = append(out, name(item))
		}
		return out
	case <-time.After(5 * time.Second):
		t.Fatal("the follower sent nothing")
		return nil
	}
}

// followProviders follows the organization's providers as v, and answers a function that names those it sends next.
func followProviders(ctx context.Context, t *testing.T, f *fixture, v agents.Viewer) (func() []string, error) {
	ch, err := f.service.FollowModelProviders(ctx, v, f.org)
	if err != nil {
		return nil, fmt.Errorf("following the providers: %w", err)
	}
	return func() []string { return next(t, ch, func(p agents.ModelProvider) string { return p.Name }) }, nil
}

// followModels follows the organization's models as v, and answers a function that names those it sends next.
func followModels(ctx context.Context, t *testing.T, f *fixture, v agents.Viewer) (func() []string, error) {
	ch, err := f.service.FollowModels(ctx, v, f.org)
	if err != nil {
		return nil, fmt.Errorf("following the models: %w", err)
	}
	return func() []string { return next(t, ch, func(m agents.Model) string { return m.Provider + "/" + m.Name }) }, nil
}

// followAgents follows the organization's agents as v, and answers a function that names those it sends next.
func followAgents(ctx context.Context, t *testing.T, f *fixture, v agents.Viewer) (func() []string, error) {
	ch, err := f.service.FollowAgents(ctx, v, f.org)
	if err != nil {
		return nil, fmt.Errorf("following the agents: %w", err)
	}
	return func() []string { return next(t, ch, func(a agents.Agent) string { return a.Name }) }, nil
}

// followTasks follows the organization's newest tasks as v, and answers a function that names those it sends next.
func followTasks(ctx context.Context, t *testing.T, f *fixture, v agents.Viewer) (func() []string, error) {
	ch, err := f.service.FollowTasks(ctx, v, f.org, agents.TaskFilter{}, 10)
	if err != nil {
		return nil, fmt.Errorf("following the tasks: %w", err)
	}
	return func() []string {
		select {
		case page, ok := <-ch:
			if !ok {
				t.Fatal("the follower stopped")
			}
			out := []string{}
			for _, task := range page.Tasks {
				out = append(out, task.Title)
			}
			return out
		case <-time.After(5 * time.Second):
			t.Fatal("the follower sent nothing")
			return nil
		}
	}, nil
}

// assignElsewhere has Olive make an agent and assign it a task titled title, a change the followers didn't make.
func assignElsewhere(ctx context.Context, t *testing.T, f *fixture, title string) {
	t.Helper()
	a, err := f.service.CreateAgent(ctx, f.olive, f.org, agents.Input{Name: "Ranger", Instructions: "Look."})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.Assign(ctx, f.olive, f.org, a.ID, agents.TaskInput{Title: title}); err != nil {
		t.Fatal(err)
	}
}

func TestFollow(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		// follow follows one of the organization's lists as v, and answers a function that names what the follower
		// sends next.
		follow func(ctx context.Context, t *testing.T, f *fixture, v agents.Viewer) (func() []string, error)
		// change changes them through the service, key being the secret that holds anthropic's key.
		change        func(t *testing.T, ctx context.Context, f *fixture, key uuid.UUID)
		before, after []string
	}{
		{name: "a provider connected elsewhere",
			follow: followProviders,
			change: func(t *testing.T, ctx context.Context, f *fixture, key uuid.UUID) {
				if _, _, err := f.service.SetModelProvider(ctx, f.olive, f.org, "openai", key, agents.NoVersion); err != nil {
					t.Fatal(err)
				}
			}, before: []string{"anthropic"}, after: []string{"anthropic", "openai"}},
		{name: "a model added elsewhere",
			follow: followModels,
			change: func(t *testing.T, ctx context.Context, f *fixture, _ uuid.UUID) {
				if _, _, err := f.service.PutModel(ctx, f.olive, f.org, "anthropic", "claude-sonnet-5-5", agents.ModelInput{}, agents.NoVersion); err != nil {
					t.Fatal(err)
				}
			}, before: []string{"anthropic/claude-opus-5-5"}, after: []string{"anthropic/claude-opus-5-5", "anthropic/claude-sonnet-5-5"}},
		{name: "an agent created elsewhere",
			follow: followAgents,
			change: func(t *testing.T, ctx context.Context, f *fixture, _ uuid.UUID) {
				if _, err := f.service.CreateAgent(ctx, f.olive, f.org, agents.Input{Name: "Ranger", Instructions: "Look."}); err != nil {
					t.Fatal(err)
				}
			}, before: []string{}, after: []string{"Ranger"}},
		{name: "a task assigned elsewhere",
			follow: followTasks,
			change: func(t *testing.T, ctx context.Context, f *fixture, _ uuid.UUID) {
				assignElsewhere(ctx, t, f, "Look around")
			}, before: []string{}, after: []string{"Look around"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			f := newFixture(t)
			key := f.followed(t)
			notifier := &livetest.Notifier{}
			f.service.WithNotifier(notifier)
			if _, err := tt.follow(ctx, t, f, f.nia); !is(err, orgs.ErrNotFound) {
				t.Errorf("an outsider follows them: %v; want %v", err, orgs.ErrNotFound)
			}
			sent, err := tt.follow(ctx, t, f, f.max)
			if err != nil {
				t.Fatal(err)
			}
			if got := sent(); fmt.Sprint(got) != fmt.Sprint(tt.before) {
				t.Errorf("first = %v; want %v", got, tt.before)
			}
			tt.change(t, ctx, f, key)
			notifier.Wake(f.org)
			if got := sent(); fmt.Sprint(got) != fmt.Sprint(tt.after) {
				t.Errorf("after the change = %v; want %v", got, tt.after)
			}
		})
	}
}
