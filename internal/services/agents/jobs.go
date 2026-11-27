package agents

// KindJob is the kind of a task a job's run is: a job that names an agent runs as one of the agent's tasks, at the
// run's commit, assigned by the job, and the run ends as the task ends
// (https://github.com/arikkfir-org/cobranch/blob/main/docs/cobranch/designs/repo-agents.md, "Jobs that name an agent").
const KindJob = "job"
