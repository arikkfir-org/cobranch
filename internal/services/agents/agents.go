// Package agents holds an organization's agents and their tasks: an agent is a principal its organization owns, which
// members create and assign tasks to, and a task does no more than both its agent and its assigner may
// (https://github.com/arikkfir-org/cobranch/blob/main/docs/cobranch/designs/agents.md, "Agents" and "Tasks").
package agents

import (
	"errors"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"cobranch.dev/internal/services/auth"
	"cobranch.dev/internal/services/orgs"
)

// The permissions agents take, at the organization.
const (
	// PermCreate creates agents, each with grants its creator holds.
	PermCreate = "agents.create"
	// PermAssign assigns tasks to the organization's agents.
	PermAssign = "agents.assign"
)

// KindAssigned is the kind of a task a principal assigned. Other slices add kinds of their own for the tasks Cobranch
// starts itself.
const KindAssigned = "assigned"

// Limits of what an agent and a task hold.
const (
	MaxName         = 100
	MaxDescription  = 1000
	MaxInstructions = 20000
	MaxTitle        = 200
	MaxGrants       = 20
	// MaxPage is the most tasks a page lists.
	MaxPage = 100
)

// The domain's own errors, beside orgs.ErrNotFound for an agent or a task that isn't there or the viewer may not see.
var (
	// ErrNameTaken is a name another of the organization's agents has, in any case.
	ErrNameTaken = errors.New("another agent of the organization has this name")
	// ErrBuiltIn is a change that a built-in agent refuses: it is never deleted.
	ErrBuiltIn = errors.New("a built-in agent is never deleted")
	// ErrEnded is a cancellation of a task that has ended.
	ErrEnded = errors.New("the task has ended")
	// ErrChanged is a change from a version that is no longer the latest.
	ErrChanged = errors.New("it changed since it was read")
)

// AnyVersion changes whichever version is there (If-Match: *).
const AnyVersion int64 = -1

// Viewer is who an operation runs for: a signed-in user, or the user or API key a credential acts as.
type Viewer = auth.User

// Principal is who created an agent, assigned a task or cancelled it.
type Principal struct {
	ID          uuid.UUID
	DisplayName string
}

// Grant is a role an agent holds: at its organization, or on one of the organization's repositories.
type Grant struct {
	RoleID   uuid.UUID
	RoleName string
	// RepoID is the repository the grant is on; uuid.Nil at the organization.
	RepoID uuid.UUID
	// RepoSlug is that repository's slug; empty at the organization.
	RepoSlug string
}

// Agent is one of an organization's agents, by the principal of kind agent it is.
type Agent struct {
	ID          uuid.UUID
	OrgID       uuid.UUID
	Name        string
	Description string
	// Instructions are the standing instructions, in Markdown, that every task of it starts from.
	Instructions string
	// BuiltIn is whether Cobranch made it for the organization: it is never deleted.
	BuiltIn bool
	// Purpose is what Cobranch made a built-in agent for, such as PurposeReview: the organization has one agent for
	// each. Empty for the agents people make.
	Purpose string
	// CreatedBy is who created it; nil for a built-in agent, and once its creator is gone.
	CreatedBy *Principal
	CreatedAt time.Time
	UpdatedAt time.Time
	// Version counts its changes from 1: it is its ETag.
	Version int64
	// Grants are the roles it holds, those at the organization first.
	Grants []Grant
}

// Input is what a new agent holds: its name, description, instructions and first grants.
type Input struct {
	Name         string
	Description  string
	Instructions string
	// Grants are the roles it starts with, each one its creator holds every permission of where it applies.
	Grants []Grant
}

// Change is a change to an agent: each field that isn't nil replaces its own.
type Change struct {
	Name         *string
	Description  *string
	Instructions *string
}

// TaskState is where a task stands.
type TaskState string

// The task states.
const (
	// TaskQueued waits for the controller to run it.
	TaskQueued TaskState = "queued"
	// TaskRunning runs in its pod.
	TaskRunning TaskState = "running"
	// TaskWaiting waits for an answer in its thread.
	TaskWaiting TaskState = "waiting"
	// TaskSucceeded ended with the agent's summary.
	TaskSucceeded TaskState = "succeeded"
	// TaskFailed ended without finishing, for the reason it gives.
	TaskFailed TaskState = "failed"
	// TaskCancelled ended because someone cancelled it.
	TaskCancelled TaskState = "cancelled"
)

// TaskStates are the task states, in the order a task goes through them.
var TaskStates = []TaskState{TaskQueued, TaskRunning, TaskWaiting, TaskSucceeded, TaskFailed, TaskCancelled}

// Active reports whether a task in the state has yet to end.
func (s TaskState) Active() bool {
	return s == TaskQueued || s == TaskRunning || s == TaskWaiting
}

// Task is a task of one of the organization's agents, which its organization numbers.
type Task struct {
	ID     uuid.UUID
	OrgID  uuid.UUID
	Number int64
	Agent  Principal
	// Kind is what the task is for: KindAssigned, or the kind of a task Cobranch starts itself.
	Kind string
	// AssignedBy is its assigner, whose permissions cap it: the agent itself for a task Cobranch starts, and nil once
	// the assigner is gone, when the task may do nothing.
	AssignedBy   *Principal
	Title        string
	Instructions string
	// RepoID and RepoSlug name the repository it works in; uuid.Nil and empty for none.
	RepoID   uuid.UUID
	RepoSlug string
	// BranchID and Branch name the branch of that repository it works on; uuid.Nil and empty for none.
	BranchID uuid.UUID
	Branch   string
	// ThreadID is the thread of that branch's chat it talks in; uuid.Nil for none.
	ThreadID uuid.UUID
	// IssueID is the issue it works on, for a task its assignment to the issue gave it; uuid.Nil for none.
	IssueID uuid.UUID
	// SHA is the commit it works at, such as the head a review task reviews; empty for none.
	SHA   string
	State TaskState
	// Reason says why it failed or was cancelled; empty otherwise.
	Reason string
	// Summary is what the agent said it did, once it ended.
	Summary     string
	CancelledBy *Principal
	CreatedAt   time.Time
	// StartedAt and EndedAt are zero until it starts, and until it ends.
	StartedAt time.Time
	EndedAt   time.Time
	// Version counts its changes from 1: it is its ETag.
	Version int64
}

// ByCobranch reports whether Cobranch started the task itself, so its agent's own grants alone cap it.
func (t Task) ByCobranch() bool {
	return t.AssignedBy != nil && t.AssignedBy.ID == t.Agent.ID
}

// TaskInput is a task to assign: its title and instructions, and perhaps where it works. A branch takes its repository,
// by its ID or its name, and a thread takes its branch.
type TaskInput struct {
	Title        string
	Instructions string
	RepoID       uuid.UUID
	BranchID     uuid.UUID
	// Branch names the branch, without refs/heads/, when BranchID doesn't.
	Branch   string
	ThreadID uuid.UUID
}

// TaskFilter narrows a list of tasks: each field set keeps only the tasks that match it.
type TaskFilter struct {
	AgentID uuid.UUID
	States  []TaskState
	RepoID  uuid.UUID
	// Before keeps the tasks numbered below it, for the next page; 0 for the newest.
	Before int64
}

// TaskPage is a page of tasks, newest first, and the number the next page starts below; 0 after the last.
type TaskPage struct {
	Tasks []Task
	Next  int64
}

// invalid is a validation error of field.
func invalid(field, code, message string) error {
	return &orgs.ValidationError{Field: field, Code: code, Message: message}
}

// text trims s and refuses it when it runs past most characters, or is empty while required.
func text(s string, required bool, most int, field, code, message string) (string, error) {
	s = strings.TrimSpace(s)
	if (required && s == "") || utf8.RuneCountInString(s) > most || !utf8.ValidString(s) {
		return s, invalid(field, code, message)
	}
	return s, nil
}

// ValidateInput normalizes in, or refuses it: a name of 1 to 100 characters, a description of at most 1000, standing
// instructions of at most 20000, and at most 20 grants, none twice.
func ValidateInput(in Input) (Input, error) {
	var err error
	if in.Name, err = text(in.Name, true, MaxName, "name", "agent.name_invalid", "an agent's name is 1 to 100 characters"); err != nil {
		return in, err
	}
	if in.Description, err = text(in.Description, false, MaxDescription, "description", "agent.description_invalid", "an agent's description is at most 1000 characters"); err != nil {
		return in, err
	}
	if in.Instructions, err = text(in.Instructions, false, MaxInstructions, "instructions", "agent.instructions_invalid", "an agent's instructions are at most 20000 characters"); err != nil {
		return in, err
	}
	if len(in.Grants) > MaxGrants {
		return in, invalid("grants", "agent.grants_invalid", "an agent starts with at most 20 grants")
	}
	for i, g := range in.Grants {
		if slices.ContainsFunc(in.Grants[:i], func(o Grant) bool { return o.RoleID == g.RoleID && o.RepoID == g.RepoID }) {
			return in, invalid("grants", "agent.grants_invalid", "each grant is given once")
		}
	}
	return in, nil
}

// ValidateChange normalizes c, or refuses it as ValidateInput refuses an agent's fields.
func ValidateChange(c Change) (Change, error) {
	in, err := ValidateInput(Input{Name: deref(c.Name, "-"), Description: deref(c.Description, ""), Instructions: deref(c.Instructions, "")})
	if err != nil {
		return c, err
	}
	if c.Name != nil {
		c.Name = &in.Name
	}
	if c.Description != nil {
		c.Description = &in.Description
	}
	if c.Instructions != nil {
		c.Instructions = &in.Instructions
	}
	return c, nil
}

// deref is *p, or otherwise for nil.
func deref(p *string, otherwise string) string {
	if p == nil {
		return otherwise
	}
	return *p
}

// ValidateTask normalizes in, or refuses it: a title of 1 to 200 characters, instructions of at most 20000, a branch
// only with its repository and a thread only with its branch.
func ValidateTask(in TaskInput) (TaskInput, error) {
	var err error
	if in.Title, err = text(in.Title, true, MaxTitle, "title", "task.title_invalid", "a task's title is 1 to 200 characters"); err != nil {
		return in, err
	}
	if in.Instructions, err = text(in.Instructions, false, MaxInstructions, "instructions", "task.instructions_invalid", "a task's instructions are at most 20000 characters"); err != nil {
		return in, err
	}
	in.Branch = strings.TrimPrefix(strings.TrimSpace(in.Branch), "refs/heads/")
	branch := in.BranchID != uuid.Nil || in.Branch != ""
	switch {
	case branch && in.RepoID == uuid.Nil:
		return in, invalid("branch", "task.branch_invalid", "a branch takes its repository")
	case in.ThreadID != uuid.Nil && !branch:
		return in, invalid("threadId", "task.thread_invalid", "a thread takes its branch")
	}
	return in, nil
}
