package harness_test

import (
	"context"
	"strings"
	"testing"

	"cobranch.dev/internal/services/agents"
	"cobranch.dev/internal/services/harness"
)

// TestJobRun checks that a job's run works quietly, told to push nothing and that its outcome becomes the job's
// annotation, whatever the task holds.
func TestJobRun(t *testing.T) {
	for _, tt := range []struct {
		name string
		work agents.Work
	}{
		{name: "a run at a branch's commit", work: agents.Work{Task: agents.Task{Kind: agents.KindJob, RepoSlug: "website", Branch: "main",
			SHA: "0123456789abcdef0123456789abcdef01234567", Instructions: "Check the links."}}},
		{name: "a tag's run, on no branch", work: agents.Work{Task: agents.Task{Kind: agents.KindJob, RepoSlug: "website",
			SHA: "0123456789abcdef0123456789abcdef01234567"}}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			kit, err := harness.JobRun{}.Prepare(context.Background(), tt.work)
			if err != nil || !kit.Quiet || kit.End != nil || len(kit.Tools) != 0 || kit.Unfinished != nil {
				t.Fatalf("Prepare() = %+v, %v, want a quiet kit", kit, err)
			}
			for _, want := range []string{"push nothing", "annotation", "500 characters"} {
				if !strings.Contains(kit.Instructions, want) {
					t.Errorf("instructions = %q, want them to say %q", kit.Instructions, want)
				}
			}
		})
	}
}
