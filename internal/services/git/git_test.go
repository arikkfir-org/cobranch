package git_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"cobranch.dev/internal/services/git"
)

var (
	a = strings.Repeat("a", 40)
	b = strings.Repeat("b", 40)
)

func TestRefusal(t *testing.T) {
	tests := []struct {
		name    string
		updates []git.RefUpdate
		want    string
	}{
		{name: "a branch and a tag", updates: []git.RefUpdate{{Name: "refs/heads/main", Old: a, New: b}, {Name: "refs/tags/v1", Old: git.ZeroSHA, New: a}}},
		{name: "a branch's deletion", updates: []git.RefUpdate{{Name: "refs/heads/old", Old: a, New: git.ZeroSHA}}},
		{name: "nothing", updates: nil},
		{name: "a note", updates: []git.RefUpdate{{Name: "refs/notes/commits", Old: git.ZeroSHA, New: a}},
			want: "Cobranch keeps branches and tags only: refs/notes/commits refused"},
		{name: "HEAD", updates: []git.RefUpdate{{Name: "HEAD", Old: a, New: b}}, want: "Cobranch keeps branches and tags only: HEAD refused"},
		{name: "a branch under cobranch/", updates: []git.RefUpdate{{Name: "refs/heads/cobranch/x", Old: git.ZeroSHA, New: a}},
			want: "Cobranch writes refs under cobranch/ itself"},
		{name: "a tag under cobranch/", updates: []git.RefUpdate{{Name: "refs/tags/cobranch/x", Old: git.ZeroSHA, New: a}},
			want: "Cobranch writes refs under cobranch/ itself"},
		{name: "one refused ref refuses the rest", updates: []git.RefUpdate{{Name: "refs/heads/main", Old: a, New: b}, {Name: "refs/pull/1/head", Old: git.ZeroSHA, New: b}},
			want: "Cobranch keeps branches and tags only: refs/pull/1/head refused"},
		{name: "a ref twice", updates: []git.RefUpdate{{Name: "refs/heads/main", Old: a, New: b}, {Name: "refs/heads/main", Old: b, New: a}},
			want: "refs/heads/main is updated twice"},
		{name: "an object name in uppercase", updates: []git.RefUpdate{{Name: "refs/heads/main", Old: a, New: strings.ToUpper(b)}},
			want: "refs/heads/main: not an update Git sends"},
		{name: "a short object name", updates: []git.RefUpdate{{Name: "refs/heads/main", Old: a, New: "abc"}},
			want: "refs/heads/main: not an update Git sends"},
		{name: "a ref to where it is", updates: []git.RefUpdate{{Name: "refs/heads/main", Old: a, New: a}},
			want: "refs/heads/main: not an update Git sends"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := git.Refusal(tt.updates); got != tt.want {
				t.Errorf("Refusal() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestBranch(t *testing.T) {
	tests := []struct {
		ref, want string
		ok        bool
	}{
		{ref: "refs/heads/main", want: "main", ok: true},
		{ref: "refs/heads/feature/x", want: "feature/x", ok: true},
		{ref: "refs/tags/v1", want: "refs/tags/v1"},
	}
	for _, tt := range tests {
		if got, ok := git.Branch(tt.ref); got != tt.want || ok != tt.ok {
			t.Errorf("Branch(%q) = %q, %t, want %q, %t", tt.ref, got, ok, tt.want, tt.ok)
		}
	}
}

// store is git.Store in memory: it answers what its fields say and keeps the pushes it recorded.
type store struct {
	snapshot git.Snapshot
	err      error
	recorded []git.Push
}

func (s *store) Snapshot(context.Context, uuid.UUID) (git.Snapshot, error) {
	return s.snapshot, s.err
}

func (s *store) Record(_ context.Context, push git.Push) error {
	if s.err != nil {
		return s.err
	}
	s.recorded = append(s.recorded, push)
	return nil
}

func TestServiceRecord(t *testing.T) {
	push := git.Push{ID: uuid.New(), RepoID: uuid.New(), PusherID: uuid.New(), Transport: git.TransportHTTPS, ReceivedAt: time.Now(),
		Updates: []git.RefUpdate{{Name: "refs/heads/main", Old: a, New: b}}}
	broken := errors.New("the database is down")
	tests := []struct {
		name         string
		updates      []git.RefUpdate
		storeErr     error
		want         git.Answer
		wantErr      error
		wantRecorded int
	}{
		{name: "accepted", updates: push.Updates, want: git.Answer{Accepted: true}, wantRecorded: 1},
		{name: "refused before the store", updates: []git.RefUpdate{{Name: "refs/notes/x", Old: git.ZeroSHA, New: a}},
			want: git.Answer{Message: "Cobranch keeps branches and tags only: refs/notes/x refused"}},
		{name: "moving no ref", want: git.Answer{Message: "the push moves no ref"}},
		{name: "a ref another push moved", updates: push.Updates, storeErr: &git.StaleError{Ref: "refs/heads/main"},
			want: git.Answer{Message: "refs/heads/main moved since you fetched: fetch, merge or rebase, then push again"}},
		{name: "a store that fails", updates: push.Updates, storeErr: broken, wantErr: broken},
		{name: "a repository that's gone", updates: push.Updates, storeErr: git.ErrNotFound, wantErr: git.ErrNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &store{err: tt.storeErr}
			p := push
			p.Updates = tt.updates
			got, err := git.NewService(s).Record(t.Context(), p)
			if !errors.Is(err, tt.wantErr) || got != tt.want {
				t.Errorf("Record() = %+v, %v, want %+v, %v", got, err, tt.want, tt.wantErr)
			}
			if len(s.recorded) != tt.wantRecorded {
				t.Errorf("the store recorded %d pushes, want %d", len(s.recorded), tt.wantRecorded)
			}
		})
	}
}

func TestServiceSnapshot(t *testing.T) {
	want := git.Snapshot{Refs: []git.Ref{{Name: "refs/heads/main", SHA: a}}, Packs: []string{"pack-" + b}}
	got, err := git.NewService(&store{snapshot: want}).Snapshot(t.Context(), uuid.New())
	if err != nil || len(got.Refs) != 1 || got.Refs[0] != want.Refs[0] || len(got.Packs) != 1 || got.Packs[0] != want.Packs[0] {
		t.Errorf("Snapshot() = %+v, %v, want %+v", got, err, want)
	}
	broken := errors.New("the database is down")
	if _, err := git.NewService(&store{err: broken}).Snapshot(t.Context(), uuid.New()); !errors.Is(err, broken) {
		t.Errorf("Snapshot() of a failing store = %v, want %v", err, broken)
	}
}
