package artifacts_test

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"testing"
	"time"

	"cobranch.dev/internal/services/artifacts"
	"cobranch.dev/internal/services/live/livetest"
)

// followed are the titles of what ch sends next, sorted, or fails after a few seconds.
func followed(t *testing.T, ch <-chan []artifacts.Artifact) []string {
	t.Helper()
	select {
	case list, ok := <-ch:
		if !ok {
			t.Fatal("the follower stopped")
		}
		out := []string{}
		for _, a := range list {
			out = append(out, a.Title)
		}
		slices.Sort(out)
		return out
	case <-time.After(5 * time.Second):
		t.Fatal("the follower sent nothing")
		return nil
	}
}

// checkChanged checks what lists sends after a change: want, or nothing while want is the list it was.
func checkChanged(t *testing.T, lists <-chan []artifacts.Artifact, was, want []string) {
	t.Helper()
	if fmt.Sprint(want) != fmt.Sprint(was) {
		if got := followed(t, lists); fmt.Sprint(got) != fmt.Sprint(want) {
			t.Errorf("after the change = %v; want %v", got, want)
		}
		return
	}
	select {
	case list := <-lists:
		t.Errorf("sent %d artifacts though the filter's list didn't change", len(list))
	case <-time.After(200 * time.Millisecond):
	}
}

func TestFollowOrganization(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		filter artifacts.Filter
		before []string
		after  []string
	}{
		{name: "an artifact made elsewhere", before: []string{"Plan", "Report"}, after: []string{"Notes", "Plan", "Report"}},
		{name: "one the filter leaves out", filter: artifacts.Filter{Kind: artifacts.KindPage}, before: []string{}, after: []string{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			f := newFixture(t)
			notifier := &livetest.Notifier{}
			f.service.WithNotifier(notifier)
			if _, err := f.service.FollowOrganization(ctx, f.nia, f.acme, tt.filter); !errors.Is(err, artifacts.ErrNotFound) {
				t.Errorf("an outsider follows them: %v; want %v", err, artifacts.ErrNotFound)
			}
			lists, err := f.service.FollowOrganization(ctx, f.olivia, f.acme, tt.filter)
			if err != nil {
				t.Fatal(err)
			}
			if got := followed(t, lists); fmt.Sprint(got) != fmt.Sprint(tt.before) {
				t.Errorf("first = %v; want %v", got, tt.before)
			}
			f.store.Now = f.store.Now.Add(time.Second)
			if _, _, err := f.service.Create(ctx, f.olivia, f.acme, f.website, input("Notes")); err != nil {
				t.Fatal(err)
			}
			notifier.Wake(f.acme)
			checkChanged(t, lists, tt.before, tt.after)
		})
	}
}
