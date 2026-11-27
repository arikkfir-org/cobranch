package harness

import (
	"context"

	"cobranch.dev/internal/services/agents"
)

// JobRun is the harness's Kind for agents.KindJob: a task that is a run of a job naming the agent. It works quietly at
// the run's commit, which it checks as the job's instructions, the task's own, ask; it pushes nothing, and its
// outcome ends the run, its summary or reason becoming the job's annotation on the commit
// (https://github.com/arikkfir-org/cobranch/blob/main/docs/cobranch/designs/repo-agents.md, "Jobs that name an
// agent").
type JobRun struct{}

var _ Kind = JobRun{}

// jobRunInstructions tell the model what a job's run asks of it, after the agent's own instructions.
const jobRunInstructions = "This task is a run of one of the repository's jobs, which checks the commit the workspace " +
	"holds: do what the task's instructions ask of it, and push nothing. Nobody follows the task as it goes, and nobody " +
	"answers questions. Finish succeeded when the check passes, or failed when it doesn't, with the reason: the run ends " +
	"as the task does, and the job's annotation on the commit shows your summary or your reason, cut at 500 characters, " +
	"so lead with what matters."

// Prepare gives a job's run its instructions, quietly (Kind).
func (JobRun) Prepare(context.Context, agents.Work) (Kit, error) {
	return Kit{Instructions: jobRunInstructions, Quiet: true}, nil
}
