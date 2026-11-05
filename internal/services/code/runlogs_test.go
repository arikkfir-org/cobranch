package code_test

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"

	"cobranch.dev/internal/services/code"
	"cobranch.dev/internal/services/jobs"
	"cobranch.dev/internal/services/repos"
)

// archived is code.ArchivedLogs over a map of runs' logs, by run ID.
type archived struct {
	logs map[uuid.UUID]map[string][]byte
	fail error
	repo uuid.UUID
}

func (a archived) Read(_ context.Context, repoID, runID uuid.UUID) (map[string][]byte, error) {
	if repoID != a.repo {
		return nil, fmt.Errorf("read repository %s's logs, want %s's", repoID, a.repo)
	}
	return a.logs[runID], a.fail
}

// live is code.LiveLogs over maps of running runs' logs and steps, by run ID, which records the limit it was asked for.
type live struct {
	logs  map[uuid.UUID]map[string][]byte
	steps map[uuid.UUID][]jobs.Step
	fail  error
	org   uuid.UUID
	limit *int64
}

func (l live) Steps(_ context.Context, orgID, runID uuid.UUID) ([]jobs.Step, error) {
	if orgID != l.org {
		return nil, fmt.Errorf("read organization %s's pods, want %s's", orgID, l.org)
	}
	return l.steps[runID], l.fail
}

func (l live) Logs(_ context.Context, orgID, runID uuid.UUID, limit int64) (map[string][]byte, error) {
	if orgID != l.org {
		return nil, fmt.Errorf("read organization %s's pods, want %s's", orgID, l.org)
	}
	*l.limit = limit
	return l.logs[runID], l.fail
}

func TestRunLogs(t *testing.T) {
	started, down := now, errors.New("down")
	script := jobs.Job{Script: "make"}
	tests := []struct {
		name   string
		run    code.Run
		noLogs bool
		// stranger reads as Nia, who doesn't read the repository, in place of Max.
		stranger bool
		fail     error
		want     []string
		wantErr  error
		wantRead int64
	}{
		{name: "an ended run's, the checkout's first", run: code.Run{State: jobs.StateFailed, StartedAt: &started, Definition: script},
			want: []string{"cobranch-checkout: checked out", "main: built"}},
		{name: "a running run's, from its pod, at most the limit each",
			run:  code.Run{State: jobs.StateRunning, StartedAt: &started, Definition: script},
			want: []string{"cobranch-checkout: checking out (live)", "main: building (live)"}, wantRead: 1 << 20},
		{name: "none for a queued run", run: code.Run{State: jobs.StateQueued, Definition: script}},
		{name: "none for a run that failed before it started", run: code.Run{State: jobs.StateFailed, Definition: script}},
		{name: "none for a webhook's run", run: code.Run{State: jobs.StateSucceeded, StartedAt: &started,
			Definition: jobs.Job{Webhook: &jobs.Webhook{URL: "https://hooks.example.com"}}}},
		{name: "none without an archive", run: code.Run{State: jobs.StateSucceeded, StartedAt: &started, Definition: script}, noLogs: true},
		{name: "none without pods", run: code.Run{State: jobs.StateRunning, StartedAt: &started, Definition: script}, noLogs: true},
		{name: "a viewer who doesn't read the repository finds none", run: code.Run{State: jobs.StateSucceeded, StartedAt: &started},
			stranger: true, wantErr: repos.ErrNotFound},
		{name: "the archive's failure", run: code.Run{State: jobs.StateSucceeded, StartedAt: &started, Definition: script},
			fail: down, wantErr: down},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture()
			tt.run.OrgID = uuid.New()
			r := f.store.AddRun(f.repo, tt.run)
			var limit int64
			if !tt.noLogs {
				withRunLogs(f, r, &limit, tt.fail)
			}
			v := f.max
			if tt.stranger {
				v = f.nia
			}
			logs, err := f.service.RunLogs(t.Context(), v, f.repo, r.Number)
			if err != nil || tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("RunLogs() error = %v, want %v", err, tt.wantErr)
				}
				return
			}
			if got := logLines(logs); !slices.Equal(got, tt.want) || limit != tt.wantRead {
				t.Errorf("RunLogs() = %q, read %d bytes each, want %q, %d", got, limit, tt.want, tt.wantRead)
			}
		})
	}
}

// withRunLogs has f's service read r's logs, its checkout's and its script's, from an archive that fails with fail, and
// while it runs, from a pod, recording in limit the most it reads.
func withRunLogs(f *fixture, r code.Run, limit *int64, fail error) {
	f.service.WithRunLogs(
		archived{repo: f.repo, fail: fail, logs: map[uuid.UUID]map[string][]byte{r.ID: {"main": []byte("built"), "cobranch-checkout": []byte("checked out")}}},
		live{org: r.OrgID, limit: limit, logs: map[uuid.UUID]map[string][]byte{r.ID: {"main": []byte("building"), "cobranch-checkout": []byte("checking out")}}},
		1<<20)
}

// logLines are logs as "container: text", with " (live)" after a live one's.
func logLines(logs []code.RunLog) []string {
	var out []string
	for _, l := range logs {
		line := l.Container + ": " + string(l.Text)
		if l.Live {
			line += " (live)"
		}
		out = append(out, line)
	}
	return out
}

func TestRunLog(t *testing.T) {
	f := newFixture()
	started := now
	r := f.store.AddRun(f.repo, code.Run{State: jobs.StateSucceeded, StartedAt: &started, Definition: jobs.Job{Script: "make"}})
	f.service.WithRunLogs(archived{repo: f.repo, logs: map[uuid.UUID]map[string][]byte{r.ID: {"main": []byte("built")}}}, nil, 0)
	if l, err := f.service.RunLog(t.Context(), f.max, f.repo, r.Number, "main"); err != nil || string(l.Text) != "built" || l.Live {
		t.Errorf("RunLog(main) = %+v, %v, want its archived log", l, err)
	}
	if _, err := f.service.RunLog(t.Context(), f.max, f.repo, r.Number, "cobranch-checkout"); !errors.Is(err, repos.ErrNotFound) {
		t.Errorf("RunLog() of a container that kept none: error = %v, want not found", err)
	}
	if _, err := f.service.RunLog(t.Context(), f.max, f.repo, r.Number+1, "main"); !errors.Is(err, repos.ErrNotFound) {
		t.Errorf("RunLog() of a run that isn't there: error = %v, want not found", err)
	}
}

func TestRunLogTimes(t *testing.T) {
	at := func(s int) time.Time { return now.Add(time.Duration(s) * time.Second).UTC() }
	stamp := func(s int) string { return at(s).Format(time.RFC3339Nano) }
	tests := []struct {
		name      string
		log       string
		wantText  string
		wantTimes []time.Time
	}{
		{name: "each line's time, taken off its text", log: stamp(1) + " go test ./...\n" + stamp(3) + " \n" + stamp(14) + " ok\n",
			wantText: "go test ./...\n\nok\n", wantTimes: []time.Time{at(1), at(3), at(14)}},
		{name: "a last line without its newline", log: stamp(1) + " one\n" + stamp(2) + " two", wantText: "one\ntwo", wantTimes: []time.Time{at(1), at(2)}},
		{name: "a line the limit cut short takes the time before", log: stamp(1) + " one\n" + stamp(2)[:10], wantText: "one\n" + stamp(2)[:10],
			wantTimes: []time.Time{at(1), at(1)}},
		{name: "a log kept before logs kept their times, as it is", log: "built\nok\n", wantText: "built\nok\n"},
		{name: "an empty log", log: "", wantText: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture()
			started := now
			r := f.store.AddRun(f.repo, code.Run{State: jobs.StateSucceeded, StartedAt: &started, Definition: jobs.Job{Script: "make"}})
			f.service.WithRunLogs(archived{repo: f.repo, logs: map[uuid.UUID]map[string][]byte{r.ID: {"main": []byte(tt.log)}}}, nil, 0)
			l, err := f.service.RunLog(t.Context(), f.max, f.repo, r.Number, "main")
			if err != nil || string(l.Text) != tt.wantText || !slices.EqualFunc(l.Times, tt.wantTimes, time.Time.Equal) {
				t.Errorf("RunLog() = %q at %v, %v, want %q at %v", l.Text, l.Times, err, tt.wantText, tt.wantTimes)
			}
		})
	}
}

func TestRunSteps(t *testing.T) {
	started, down := now, errors.New("down")
	script := jobs.Job{Script: "make"}
	kept := []jobs.Step{{Name: "cobranch-checkout", State: jobs.StepSucceeded}, {Name: "main", State: jobs.StepFailed, ExitCode: 2}}
	running := []jobs.Step{{Name: "cobranch-checkout", State: jobs.StepSucceeded}, {Name: "main", State: jobs.StepRunning}}
	tests := []struct {
		name    string
		run     code.Run
		noPods  bool
		fail    error
		want    []jobs.Step
		wantErr error
	}{
		{name: "an ended run's, as the controller kept them", run: code.Run{State: jobs.StateFailed, StartedAt: &started, Definition: script}, want: kept},
		{name: "a running run's, from its pod", run: code.Run{State: jobs.StateRunning, StartedAt: &started, Definition: script}, want: running},
		{name: "none for a running run without pods", run: code.Run{State: jobs.StateRunning, StartedAt: &started, Definition: script}, noPods: true},
		{name: "none for a queued run", run: code.Run{State: jobs.StateQueued, Definition: script}},
		{name: "none for a run that failed before it started", run: code.Run{State: jobs.StateFailed, Definition: script}},
		{name: "none for a webhook's run", run: code.Run{State: jobs.StateSucceeded, StartedAt: &started,
			Definition: jobs.Job{Webhook: &jobs.Webhook{URL: "https://hooks.example.com"}}}},
		{name: "the pod's failure", run: code.Run{State: jobs.StateRunning, StartedAt: &started, Definition: script}, fail: down, wantErr: down},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture()
			tt.run.OrgID = uuid.New()
			r := f.store.AddRun(f.repo, tt.run)
			f.store.Steps[r.ID] = kept
			if !tt.noPods {
				var limit int64
				f.service.WithRunLogs(nil, live{org: r.OrgID, limit: &limit, fail: tt.fail, steps: map[uuid.UUID][]jobs.Step{r.ID: running}}, 0)
			}
			steps, err := f.service.RunSteps(t.Context(), f.max, f.repo, r.Number)
			if !errors.Is(err, tt.wantErr) || !slices.Equal(stepNames(steps), stepNames(tt.want)) {
				t.Errorf("RunSteps() = %v, %v, want %v, %v", stepNames(steps), err, stepNames(tt.want), tt.wantErr)
			}
		})
	}
	f := newFixture()
	r := f.store.AddRun(f.repo, code.Run{State: jobs.StateSucceeded, StartedAt: &started, Definition: script})
	if _, err := f.service.RunSteps(t.Context(), f.nia, f.repo, r.Number); !errors.Is(err, repos.ErrNotFound) {
		t.Errorf("RunSteps() for a viewer who doesn't read the repository: error = %v, want not found", err)
	}
}

// stepNames are steps as "name state", with the exit code after one that ended badly.
func stepNames(steps []jobs.Step) []string {
	out := make([]string, 0, len(steps))
	for _, s := range steps {
		name := s.Name + " " + string(s.State)
		if s.ExitCode != 0 {
			name += fmt.Sprintf(" %d", s.ExitCode)
		}
		out = append(out, name)
	}
	return out
}
