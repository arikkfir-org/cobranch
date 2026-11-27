package runner_test

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"cobranch.dev/internal/services/agents"
	"cobranch.dev/internal/services/jobs"
	"cobranch.dev/internal/services/runner"
)

// agentRun is a queued run of job ai/site, which names agent site-checker, at a commit of main.
func agentRun(change func(*runner.Run)) runner.Run {
	r := scriptRun(nil)
	r.Job = "ai/site"
	r.SHA = "0123456789abcdef0123456789abcdef01234567"
	r.Definition = jobs.Job{Name: "ai/site", Permissions: jobs.DefaultPermissions,
		Agent: &jobs.Agent{Name: "site-checker", Instructions: "Check the links."}}
	if change != nil {
		change(&r)
	}
	return r
}

func TestDispatchAgentRuns(t *testing.T) {
	repoAgent := runner.JobAgent{ID: id("a9e1"), Name: "site-checker", Repository: true}
	orgAgent := runner.JobAgent{ID: id("a9e2"), Name: "site-checker"}
	tests := []struct {
		name       string
		run        runner.Run
		agent      *runner.JobAgent
		authz      authz
		ended      bool
		wantEvents []string
	}{
		{name: "the repository's agent", run: agentRun(nil), agent: &repoAgent,
			wantEvents: []string{`running #7 as a9e1's task "Run 7 of ai/site at 0123456789ab" on "main": Check the links.`}},
		{name: "the organization's agent, which reads the repository", run: agentRun(nil), agent: &orgAgent,
			wantEvents: []string{`running #7 as a9e2's task "Run 7 of ai/site at 0123456789ab" on "main": Check the links.`}},
		{name: "a tag's run works on no branch", run: agentRun(func(r *runner.Run) { r.Ref = "refs/tags/v1" }), agent: &repoAgent,
			wantEvents: []string{`running #7 as a9e1's task "Run 7 of ai/site at 0123456789ab" on "": Check the links.`}},
		{name: "the organization's agent that may not read the repository", run: agentRun(nil), agent: &orgAgent,
			authz:      authz{denied: []uuid.UUID{orgAgent.ID}},
			wantEvents: []string{"failed: agent site-checker may not read repository api: grant it repo.read there"}},
		{name: "the repository's agent needs no grant there", run: agentRun(nil), agent: &repoAgent, authz: authz{denied: []uuid.UUID{repoAgent.ID}},
			wantEvents: []string{`running #7 as a9e1's task "Run 7 of ai/site at 0123456789ab" on "main": Check the links.`}},
		{name: "no agent of the name", run: agentRun(nil),
			wantEvents: []string{"failed: neither repository api nor its organization has an agent named site-checker"}},
		{name: "a run cancelled meanwhile assigns nothing", run: agentRun(nil), agent: &repoAgent, ended: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &store{queued: []runner.Run{tt.run}, tokens: map[uuid.UUID]time.Time{}, agents: map[string]runner.JobAgent{}, ended: tt.ended}
			if tt.agent != nil {
				s.agents[tt.agent.Name] = *tt.agent
			}
			c := &cluster{}
			svc := runner.NewService(s, c, vault{}, archive{}, &poster{}, tt.authz, settings)
			svc.SetClock(func() time.Time { return now })
			if ok, err := svc.Dispatch(context.Background()); !ok || err != nil {
				t.Fatalf("Dispatch() = %v, %v", ok, err)
			}
			want := append([]string{"claimed #7 until 09:02:00"}, tt.wantEvents...)
			if !slices.Equal(s.events, want) {
				t.Errorf("events = %q, want %q", s.events, want)
			}
			if len(c.started) > 0 || len(s.tokens) > 0 {
				t.Errorf("an agent's run started a pod %v or made a token %v", c.started, s.tokens)
			}
		})
	}
}

// agentRunAt is a run of an agent's job, started ten minutes before now, whose task #12 runs, as change makes it.
func agentRunAt(change func(*runner.AgentRun)) runner.AgentRun {
	r := runner.AgentRun{Running: runner.Running{ID: id("aaaa"), OrgID: id("0001"), RepoID: id("0002"), Number: 7,
		StartedAt: now.Add(-10 * time.Minute)}, Task: 12, State: agents.TaskRunning}
	change(&r)
	return r
}

func TestFollowAgentRuns(t *testing.T) {
	run := agentRunAt
	tests := []struct {
		name      string
		runs      []runner.AgentRun
		taskEnded bool
		orphans   int64
		want      []string
	}{
		{name: "a task that works", runs: []runner.AgentRun{run(func(*runner.AgentRun) {})}},
		{name: "a task that waits to start", runs: []runner.AgentRun{run(func(r *runner.AgentRun) { r.State = agents.TaskQueued })}},
		{name: "a task that succeeded", runs: []runner.AgentRun{run(func(r *runner.AgentRun) {
			r.State, r.Summary = agents.TaskSucceeded, "Every link answers."
		})}, want: []string{"succeeded: Every link answers."}},
		{name: "a task that succeeded saying nothing", runs: []runner.AgentRun{run(func(r *runner.AgentRun) { r.State = agents.TaskSucceeded })},
			want: []string{"succeeded: Succeeded"}},
		{name: "a long summary, cut", runs: []runner.AgentRun{run(func(r *runner.AgentRun) {
			r.State, r.Summary = agents.TaskSucceeded, strings.Repeat("a", 600)
		})}, want: []string{"succeeded: " + strings.Repeat("a", 499) + "…"}},
		{name: "a task that failed", runs: []runner.AgentRun{run(func(r *runner.AgentRun) {
			r.State, r.Reason = agents.TaskFailed, "/docs/setup links nowhere."
		})}, want: []string{"failed: /docs/setup links nowhere."}},
		{name: "a task that reached its cap", runs: []runner.AgentRun{run(func(r *runner.AgentRun) {
			r.State, r.Reason = agents.TaskFailed, "The agent reached its spend cap."
		})}, want: []string{"failed: The agent reached its spend cap."}},
		{name: "a task someone cancelled", runs: []runner.AgentRun{run(func(r *runner.AgentRun) {
			r.State, r.Reason = agents.TaskCancelled, "Not now."
		})}, want: []string{"failed: Its task was cancelled: Not now."}},
		{name: "a task cancelled without a reason", runs: []runner.AgentRun{run(func(r *runner.AgentRun) { r.State = agents.TaskCancelled })},
			want: []string{"failed: Its task was cancelled"}},
		{name: "a task that is gone", runs: []runner.AgentRun{run(func(r *runner.AgentRun) { r.Task, r.State = 0, "" })},
			want: []string{"failed: its task is gone"}},
		{name: "past the installation's timeout", runs: []runner.AgentRun{run(func(r *runner.AgentRun) { r.StartedAt = now.Add(-61 * time.Minute) })},
			want: []string{"cancelled aaaa's task: The job's run timed out after 1h0m0s.", "failed: timed out after 1h0m0s"}},
		{name: "within the job's own timeout", runs: []runner.AgentRun{run(func(r *runner.AgentRun) {
			r.StartedAt, r.Timeout = now.Add(-61*time.Minute), 2*time.Hour
		})}},
		{name: "past the job's own timeout", runs: []runner.AgentRun{run(func(r *runner.AgentRun) {
			r.StartedAt, r.Timeout = now.Add(-11*time.Minute), 10*time.Minute
		})}, want: []string{"cancelled aaaa's task: The job's run timed out after 10m0s.", "failed: timed out after 10m0s"}},
		{name: "past the timeout, as its task ended meanwhile, left to the next pass", runs: []runner.AgentRun{run(func(r *runner.AgentRun) {
			r.StartedAt = now.Add(-61 * time.Minute)
		})}, taskEnded: true},
		{name: "the tasks of runs that ended", orphans: 2, want: []string{"cancelled 2 orphan tasks"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &store{agentRuns: tt.runs, taskEnded: tt.taskEnded, orphans: tt.orphans, tokens: map[uuid.UUID]time.Time{}}
			if err := followAt(s); err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(s.events, tt.want) {
				t.Errorf("events = %q, want %q", s.events, tt.want)
			}
		})
	}
}

// followAt follows s's runs once, at now.
func followAt(s *store) error {
	svc := runner.NewService(s, &cluster{}, vault{}, archive{}, &poster{}, authz{}, settings)
	svc.SetClock(func() time.Time { return now })
	return svc.Follow(context.Background()) //nolint:wrapcheck // a test's own helper
}
