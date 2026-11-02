package git_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"cobranch.dev/internal/services/git"
	"cobranch.dev/internal/services/limits"
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
	// processed is the push Process claims: uuid.Nil when none waits.
	processed uuid.UUID
	// retry is the retry function Process got.
	retry func(attempts int) time.Duration
	// failures is how many of Record's calls fail with err before the rest succeed; 0 fails them all when err is set.
	failures, calls int
}

func (s *store) Snapshot(context.Context, uuid.UUID) (git.Snapshot, error) {
	return s.snapshot, s.err
}

func (s *store) Record(_ context.Context, push git.Push) error {
	s.calls++
	if s.err != nil && (s.failures == 0 || s.calls <= s.failures) {
		return s.err
	}
	s.recorded = append(s.recorded, push)
	return nil
}

func (s *store) Process(_ context.Context, retry func(attempts int) time.Duration) (uuid.UUID, error) {
	s.retry = retry
	return s.processed, s.err
}

func TestServiceRecord(t *testing.T) {
	push := git.Push{ID: uuid.New(), RepoID: uuid.New(), PusherID: uuid.New(), Transport: git.TransportHTTPS, ReceivedAt: time.Now(),
		Updates: []git.RefUpdate{{Name: "refs/heads/main", Old: a, New: b}}}
	broken := errors.New("the database is down")
	tests := []struct {
		name         string
		updates      []git.RefUpdate
		storeErr     error
		failures     int
		want         git.Answer
		wantErr      error
		wantRecorded int
		wantCalls    int
	}{
		{name: "accepted", updates: push.Updates, want: git.Answer{Accepted: true}, wantRecorded: 1, wantCalls: 1},
		{name: "accepted once the store answers", updates: push.Updates, storeErr: broken, failures: 2, want: git.Answer{Accepted: true}, wantRecorded: 1, wantCalls: 3},
		{name: "refused before the store", updates: []git.RefUpdate{{Name: "refs/notes/x", Old: git.ZeroSHA, New: a}},
			want: git.Answer{Message: "Cobranch keeps branches and tags only: refs/notes/x refused"}},
		{name: "moving no ref", want: git.Answer{Message: "the push moves no ref"}},
		{name: "a ref another push moved", updates: push.Updates, storeErr: &git.StaleError{Ref: "refs/heads/main"},
			want: git.Answer{Message: "refs/heads/main moved since you fetched: fetch, merge or rebase, then push again", Stale: true}, wantCalls: 1},
		{name: "a push the merge requirements refuse", updates: push.Updates, storeErr: &git.RefusedError{Message: "main requires feature to be Ready: ci/build failed on " + a[:7]},
			want: git.Answer{Message: "main requires feature to be Ready: ci/build failed on " + a[:7]}, wantCalls: 1},
		{name: "a store that fails", updates: push.Updates, storeErr: broken, wantErr: broken, wantCalls: 4},
		{name: "a repository that's gone", updates: push.Updates, storeErr: git.ErrNotFound, wantErr: git.ErrNotFound, wantCalls: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &store{err: tt.storeErr, failures: tt.failures}
			p := push
			p.Updates = tt.updates
			got, err := git.NewService(s).Record(t.Context(), p)
			if !errors.Is(err, tt.wantErr) || got != tt.want {
				t.Errorf("Record() = %+v, %v, want %+v, %v", got, err, tt.want, tt.wantErr)
			}
			if len(s.recorded) != tt.wantRecorded || s.calls != tt.wantCalls {
				t.Errorf("the store recorded %d pushes in %d calls, want %d in %d", len(s.recorded), s.calls, tt.wantRecorded, tt.wantCalls)
			}
		})
	}
}

// quotas are the same limits for every organization, and Git data already used.
type quotas struct {
	limit, used int64
	err         error
}

func (q quotas) Organization(context.Context, uuid.UUID) (limits.Limits, error) {
	return limits.Limits{GitDataPerOrganization: q.limit}, q.err
}

func (q quotas) WeighPush(_ context.Context, _ uuid.UUID, size int64) (int64, error) {
	if q.err != nil {
		return 0, q.err
	}
	if limits.Exceeds(q.limit, q.used, size) {
		return q.limit, &limits.ExceededError{Kind: limits.KindGitData, Limit: q.limit, Used: q.used, Adding: size}
	}
	return q.limit, nil
}

func TestServiceRecordHoldsThePackToItsLimit(t *testing.T) {
	broken := errors.New("broken")
	pack := &git.Pack{Name: "pack-" + a, Upload: uuid.New(), Size: 10 << 20, Objects: 3}
	tests := []struct {
		name      string
		quotas    *quotas
		pack      *git.Pack
		wantLimit int64
		wantErr   error
	}{
		{name: "the organization's limit", quotas: &quotas{limit: 1 << 30}, pack: pack, wantLimit: 1 << 30},
		{name: "no limit", quotas: &quotas{}, pack: pack},
		{name: "no quotas", pack: pack},
		{name: "no pack", quotas: &quotas{limit: 1 << 30}},
		{name: "the limits can't be read", quotas: &quotas{err: broken}, pack: pack, wantErr: broken},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &store{}
			service := git.NewService(s)
			if tt.quotas != nil {
				service.WithQuotas(tt.quotas)
			}
			push := git.Push{ID: uuid.New(), RepoID: uuid.New(), OrgID: uuid.New(), PusherID: uuid.New(), Transport: git.TransportHTTPS,
				Updates: []git.RefUpdate{{Name: "refs/heads/main", Old: a, New: b}}, Pack: tt.pack}
			_, err := service.Record(t.Context(), push)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("Record() = %v, want %v", err, tt.wantErr)
			}
			if tt.wantErr == nil && (len(s.recorded) != 1 || s.recorded[0].GitDataLimit != tt.wantLimit) {
				t.Errorf("the store recorded %+v, want one push held to %d bytes", s.recorded, tt.wantLimit)
			}
		})
	}
}

func TestServiceWeigh(t *testing.T) {
	broken := errors.New("broken")
	pack := "pack-" + b
	tests := []struct {
		name    string
		quotas  *quotas
		store   *store
		size    int64
		want    string
		wantErr error
	}{
		{name: "fits", quotas: &quotas{limit: 1 << 30, used: 900 << 20}, size: 100 << 20},
		{name: "over", quotas: &quotas{limit: 1 << 30, used: 1000 << 20}, size: 100 << 20,
			want: "This organization stores 1000 MiB of Git data, and this push adds 100 MiB: over its limit of 1 GiB. Nothing changed; an administrator can raise the limit."},
		{name: "over, but the repository lists the pack already", quotas: &quotas{limit: 1 << 30, used: 1 << 30}, size: 100 << 20,
			store: &store{snapshot: git.Snapshot{Packs: []git.Pack{{Name: pack, Upload: uuid.New()}}}}},
		{name: "over, and the repository lists other packs", quotas: &quotas{limit: 1 << 30, used: 1 << 30}, size: 100 << 20,
			store: &store{snapshot: git.Snapshot{Packs: []git.Pack{{Name: "pack-" + a, Upload: uuid.New()}}}},
			want:  "This organization stores 1 GiB of Git data, and this push adds 100 MiB: over its limit of 1 GiB. Nothing changed; an administrator can raise the limit."},
		{name: "no quotas", size: 1 << 40},
		{name: "the limits can't be read", quotas: &quotas{err: broken}, size: 1, wantErr: broken},
		{name: "the repository's packs can't be read", quotas: &quotas{limit: 1 << 30}, store: &store{err: broken}, size: 1, wantErr: broken},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.store == nil {
				tt.store = &store{}
			}
			service := git.NewService(tt.store)
			if tt.quotas != nil {
				service.WithQuotas(tt.quotas)
			}
			got, err := service.Weigh(t.Context(), uuid.New(), uuid.New(), pack, tt.size)
			if got != tt.want || !errors.Is(err, tt.wantErr) {
				t.Errorf("Weigh() = %q, %v, want %q, %v", got, err, tt.want, tt.wantErr)
			}
		})
	}
}

func TestServiceSnapshot(t *testing.T) {
	want := git.Snapshot{Refs: []git.Ref{{Name: "refs/heads/main", SHA: a}}, Packs: []git.Pack{{Name: "pack-" + b, Upload: uuid.New()}}}
	got, err := git.NewService(&store{snapshot: want}).Snapshot(t.Context(), uuid.New())
	if err != nil || len(got.Refs) != 1 || got.Refs[0] != want.Refs[0] || len(got.Packs) != 1 || got.Packs[0] != want.Packs[0] {
		t.Errorf("Snapshot() = %+v, %v, want %+v", got, err, want)
	}
	broken := errors.New("the database is down")
	if _, err := git.NewService(&store{err: broken}).Snapshot(t.Context(), uuid.New()); !errors.Is(err, broken) {
		t.Errorf("Snapshot() of a failing store = %v, want %v", err, broken)
	}
}

func TestServiceProcess(t *testing.T) {
	id := uuid.New()
	broken := errors.New("the database is down")
	tests := []struct {
		name          string
		store         store
		wantProcessed bool
		wantErr       error
	}{
		{name: "a push waited", store: store{processed: id}, wantProcessed: true},
		{name: "none waits", wantProcessed: false},
		{name: "a push it failed", store: store{processed: id, err: broken}, wantProcessed: true, wantErr: broken},
		{name: "a database that fails", store: store{err: broken}, wantProcessed: false, wantErr: broken},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := tt.store
			processed, err := git.NewService(&s).Process(t.Context())
			if processed != tt.wantProcessed || !errors.Is(err, tt.wantErr) {
				t.Errorf("Process() = %v, %v, want %v, %v", processed, err, tt.wantProcessed, tt.wantErr)
			}
			if s.retry == nil || s.retry(3) != git.RetryAfter(3) {
				t.Error("Process() didn't hand the store RetryAfter")
			}
		})
	}
}

func TestRetryAfter(t *testing.T) {
	for _, tt := range []struct {
		attempts int
		want     time.Duration
	}{
		{0, time.Second}, {1, time.Second}, {2, 2 * time.Second}, {5, 16 * time.Second}, {12, 2048 * time.Second}, {13, time.Hour}, {1000, time.Hour},
	} {
		if got := git.RetryAfter(tt.attempts); got != tt.want {
			t.Errorf("RetryAfter(%d) = %v, want %v", tt.attempts, got, tt.want)
		}
	}
}
