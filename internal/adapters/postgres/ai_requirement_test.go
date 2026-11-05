package postgres

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"

	"cobranch.dev/internal/services/agents"
	"cobranch.dev/internal/services/code"
	"cobranch.dev/internal/services/git"
)

// aiStatus is the branch's status and its AI reviewer's check, as "status ai-state: reason".
func aiStatus(t *testing.T, f *jobsFixture, repoID uuid.UUID, branch string) string {
	t.Helper()
	b, err := f.c.Branch(context.Background(), repoID, branch)
	if err != nil {
		t.Fatal(err)
	}
	out := string(b.Status)
	for _, c := range b.Checks {
		if c.Kind == code.CheckAI {
			out += " " + string(c.AI)
			if c.Reason != "" {
				out += ": " + c.Reason
			}
		}
	}
	return out
}

// reviewerID is the organization's AI reviewer.
func reviewerID(t *testing.T, f *jobsFixture, orgID uuid.UUID) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := f.pool.QueryRow(context.Background(), "SELECT principal_id FROM agents WHERE org_id = $1 AND purpose = 'review'", orgID).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

// aiSteps change the reviews and review tasks of the website's branch feature, whose base, main, requires the AI
// reviewer's approval.
type aiSteps struct {
	f        *jobsFixture
	t        *testing.T
	a        *AgentStore
	reviewer uuid.UUID
}

// push pushes feature from old to head, ahead of main by ahead.
func (s aiSteps) push(old, head string, ahead int) func() {
	return func() {
		p := refs(s.f.website.ID, s.f.olivia, []git.RefUpdate{{Name: "refs/heads/feature", Old: old, New: head}},
			commit(head, person("Olivia", "olivia@example.com"), shaA))
		p.Facts = []git.Facts{{Branch: "feature", Ahead: ahead}}
		s.f.push(s.t, p)
	}
}

// model puts the reviewer on a model, or on none with both empty.
func (s aiSteps) model(provider, model string) func() {
	return func() {
		if _, err := s.a.SetModel(context.Background(), s.f.olivia, s.f.acme.ID, s.reviewer, agents.AnyVersion, provider, model); err != nil {
			s.t.Fatal(err)
		}
	}
}

// request has Olivia ask for another review of feature.
func (s aiSteps) request() {
	if _, err := s.f.c.RequestReview(context.Background(), s.f.olivia, s.f.website.ID, "feature"); err != nil {
		s.t.Fatal(err)
	}
}

// review has the reviewer review sha.
func (s aiSteps) review(sha string, state code.ReviewState) func() {
	return func() {
		if _, err := s.f.c.SubmitReview(context.Background(), s.reviewer, s.f.website.ID, sha, code.ReviewInput{State: state, Body: "Looked."}); err != nil {
			s.t.Fatal(err)
		}
	}
}

// run starts the queued review task, as the controller does when its first pod starts.
func (s aiSteps) run() {
	ctx := context.Background()
	var id uuid.UUID
	if err := s.f.pool.QueryRow(ctx, "SELECT id FROM agent_tasks WHERE state = 'queued'").Scan(&id); err != nil {
		s.t.Fatal(err)
	}
	if marked, err := NewTaskPodStore(s.f.pool).MarkRunning(ctx, id); !marked || err != nil {
		s.t.Fatalf("MarkRunning() = %t, %v", marked, err)
	}
}

// running is the running review task.
func (s aiSteps) running() uuid.UUID {
	var id uuid.UUID
	if err := s.f.pool.QueryRow(context.Background(), "SELECT id FROM agent_tasks WHERE state = 'running'").Scan(&id); err != nil {
		s.t.Fatal(err)
	}
	return id
}

// end has the harness move the running review task as change says.
func (s aiSteps) end(change agents.StateChange) func() {
	return func() {
		body, err := json.Marshal(map[string]string{"state": string(change.State)})
		if err != nil {
			s.t.Fatal(err)
		}
		if _, _, err := s.a.Append(context.Background(), s.reviewer, s.running(), agents.Entry{Seq: 1, Kind: agents.EntryState, Body: body}, &change); err != nil {
			s.t.Fatal(err)
		}
	}
}

// fail has the controller fail the running review task for reason, as it does when its pod fails.
func (s aiSteps) fail(reason string) func() {
	return func() {
		if failed, err := NewTaskPodStore(s.f.pool).Fail(context.Background(), s.running(), reason); !failed || err != nil {
			s.t.Fatalf("Fail() = %t, %v", failed, err)
		}
	}
}

// spend has the running review task's model proxy report a call that read input tokens, which reaches its cap.
func (s aiSteps) spend(input int64) func() {
	return func() {
		spend, err := s.a.RecordSpend(context.Background(), s.running(), agents.Usage{ID: uuid.New(), Model: "review-1", Input: input})
		if err != nil || !spend.Ended {
			s.t.Fatalf("RecordSpend() = %+v, %v; want the task ended", spend, err)
		}
	}
}

// cancel has Olivia cancel the organization's task.
func (s aiSteps) cancel(number int64) func() {
	return func() {
		if _, err := s.a.Cancel(context.Background(), s.f.olivia, s.f.acme.ID, number, agents.AnyVersion); err != nil {
			s.t.Fatal(err)
		}
	}
}

// requiresAI fails t unless the repository's branch main, read alone and with the others, requires the AI reviewer's
// approval.
func requiresAI(t *testing.T, f *jobsFixture, repoID uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	b, err := f.c.Branch(ctx, repoID, "main")
	if err != nil || b.Requirements == nil || b.Requirements.Reviews == nil || !b.Requirements.Reviews.AI {
		t.Fatalf("Branch(main).Requirements = %+v, %v; want reviews.ai", b.Requirements, err)
	}
	branches, err := f.c.Branches(ctx, repoID)
	if err != nil || len(branches) != 1 || branches[0].Requirements == nil || branches[0].Requirements.Reviews == nil ||
		!branches[0].Requirements.Reviews.AI {
		t.Fatalf("Branches() = %+v, %v; want main with reviews.ai", branches, err)
	}
}

func TestAIRequirementStatus(t *testing.T) {
	t.Parallel()
	f := newJobsFixture(t)
	s := aiSteps{f: f, t: t, a: NewAgentStore(f.pool), reviewer: reviewerID(t, f, f.acme.ID)}
	main := refs(f.website.ID, f.olivia, []git.RefUpdate{{Name: "refs/heads/main", Old: git.ZeroSHA, New: shaA}}, commit(shaA, person("Olivia", "olivia@example.com")))
	main.Requirements = []git.Requirements{{Branch: "main", Reviews: &git.ReviewRequirements{AI: true}}}
	f.push(t, main)
	requiresAI(t, f, f.website.ID)
	exec(context.Background(), t, f.pool, "INSERT INTO model_providers (org_id, provider) VALUES ($1, 'acme-models')", f.acme.ID)
	exec(context.Background(), t, f.pool, "INSERT INTO models (org_id, provider, model) VALUES ($1, 'acme-models', 'review-1')", f.acme.ID)
	exec(context.Background(), t, f.pool, "UPDATE models SET input_price = 3000000, cap = 250000 WHERE org_id = $1", f.acme.ID)
	shaD, failure := strings.Repeat("d", 40), "The model's provider refused the credentials."
	lost, capped := "Its pod was lost.", "It spent $0.30, reaching its cap of $0.25 a task."
	steps := []struct {
		name string
		do   []func()
		want string
	}{
		{name: "a reviewer without a model blocks", do: []func(){s.push(git.ZeroSHA, shaC, 1)}, want: "blocked no_model"},
		{name: "a model for the reviewer works the branches out again", do: []func(){s.model("acme-models", "review-1")}, want: "blocked unreviewed"},
		{name: "a review asked for keeps it Processing", do: []func(){s.request}, want: "processing queued"},
		{name: "its task starts running", do: []func(){s.run}, want: "processing running"},
		{name: "the reviewer approves as its task runs", do: []func(){s.review(shaC, code.ReviewApproved)}, want: "processing running"},
		{name: "its task succeeds", do: []func(){s.end(agents.StateChange{State: agents.TaskSucceeded, Summary: "Approved"})}, want: "ready approved"},
		{name: "another review, cancelled, leaves the approval standing", do: []func(){s.request, s.cancel(2)}, want: "ready approved"},
		{name: "the reviewer requests changes", do: []func(){s.review(shaC, code.ReviewChangesRequested)}, want: "blocked changes_requested"},
		{name: "a newer push queues its review", do: []func(){s.push(shaC, shaD, 2)}, want: "processing queued"},
		{name: "its review fails", do: []func(){s.run, s.end(agents.StateChange{State: agents.TaskFailed, Reason: failure})},
			want: "blocked failed: " + failure},
		{name: "another review, which the controller fails", do: []func(){s.request, s.run, s.fail(lost)}, want: "blocked failed: " + lost},
		{name: "another review, which reaches its cap", do: []func(){s.request, s.run, s.spend(100_000)}, want: "blocked failed: " + capped},
		{name: "taking the reviewer's model away", do: []func(){s.model("", "")}, want: "blocked failed: " + capped},
	}
	for _, step := range steps {
		for _, do := range step.do {
			do()
		}
		if got := aiStatus(t, f, f.website.ID, "feature"); got != step.want {
			t.Fatalf("%s: feature = %q, want %q", step.name, got, step.want)
		}
	}
}

func TestAIRequirementMigration(t *testing.T) {
	t.Parallel()
	f := newJobsFixture(t)
	ctx := context.Background()
	_, err := f.pool.Exec(ctx, `INSERT INTO merge_requirements (repo_id, branch, org_id, annotations, linear_history, ai)
		VALUES ($1, 'main', $2, '{}', false, true)`, f.website.ID, f.acme.ID)
	if err == nil || !strings.Contains(err.Error(), "merge_requirements_ai_reviews") {
		t.Errorf("requiring the AI reviewer's approval without reviews = %v, want the check to refuse it", err)
	}
}
