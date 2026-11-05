package harness

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"cobranch.dev/internal/services/agents"
)

// How many of the agent's tasks the tasks tool lists: those it works on and those queued, and those it ended last.
const (
	tasksActive = 50
	tasksEnded  = 10
)

// tasksSpec describes tasks, the tool of a task asked in a conversation with its own agent, to the model.
var tasksSpec = Spec{
	Name: "tasks",
	Description: "Lists your tasks, for the person who talks with you here: what you work on now, the head of your " +
		"queue in the order you will take it and how many follow, and what you ended last. Each names its number, " +
		"state, title, who asked for it and where it works; one that someone else asked for in private shows only its " +
		"number, state and times.",
	Parameters: json.RawMessage(`{"type":"object","properties":{},"required":[]}`),
}

// tasksTools are the tools of a task asked in a conversation with its own agent, which is about the agent and its work:
// tasks. None for any other task.
func (r *run) tasksTools() []Tool {
	if chat, ok := r.chat(); !ok || !ownAgent(chat, r.work.Task.Agent.ID) {
		return nil
	}
	return []Tool{chatTool{spec: tasksSpec, run: r.listTasks}}
}

// listTasks answers the agent's tasks: those it works on, oldest first, the head of its queue, as it takes them, with
// how many follow, and those it ended last.
func (r *run) listTasks(ctx context.Context, c Call) (string, error) {
	if err := decode(c.Arguments, &struct{}{}); err != nil {
		return "", err
	}
	org, agent := r.work.OrgSlug, r.work.Task.Agent.ID
	active, err := r.cobranch.Tasks(ctx, org, agent, []agents.TaskState{agents.TaskRunning, agents.TaskWaiting}, tasksActive)
	if err != nil {
		return "", fmt.Errorf("listing the tasks you work on: %w", err)
	}
	queued, waiting, err := r.cobranch.Queue(ctx, org, agent, tasksActive)
	if err != nil {
		return "", fmt.Errorf("listing your queue: %w", err)
	}
	ended, err := r.cobranch.Tasks(ctx, org, agent, []agents.TaskState{agents.TaskSucceeded, agents.TaskFailed, agents.TaskCancelled}, tasksEnded)
	if err != nil {
		return "", fmt.Errorf("listing the tasks you ended: %w", err)
	}
	slices.Reverse(active)
	var b strings.Builder
	r.writeTasks(&b, "Working on now", active, false, 0)
	r.writeTasks(&b, "Your queue, in the order you will take it", queued, true, waiting-len(queued))
	r.writeTasks(&b, "Ended last", ended, false, 0)
	return strings.TrimSpace(b.String()), nil
}

// writeTasks writes a section of the tasks tool's output: its heading and a line per task, numbered with places for a
// queue, then how many more follow those listed; or that it holds none.
func (r *run) writeTasks(b *strings.Builder, heading string, tasks []agents.Task, places bool, more int) {
	b.WriteString(heading + ":\n")
	if len(tasks) == 0 {
		b.WriteString("none\n\n")
		return
	}
	for i, t := range tasks {
		b.WriteString("- ")
		if places {
			b.WriteString(strconv.Itoa(i+1) + ". ")
		}
		b.WriteString(r.showTask(t) + "\n")
	}
	if more > 0 {
		b.WriteString("- and " + strconv.Itoa(more) + " more after them\n")
	}
	b.WriteString("\n")
}

// showTask is a line of the tasks tool's output: the task's number, state and when it got there, then, unless it is
// private to someone else, its title, who asked for it and where it works.
func (r *run) showTask(t agents.Task) string {
	line := "#" + strconv.FormatInt(t.Number, 10)
	if t.Number == r.work.Task.Number {
		line += " (this one)"
	}
	line += " " + string(t.State) + " since " + since(t).UTC().Format("2006-01-02 15:04 UTC")
	if t.Private && t.AssignedBy == nil {
		return line + ", asked for in private by someone else"
	}
	line += ": " + t.Title
	switch {
	case t.AssignedBy == nil:
	case t.ByCobranch():
		line += ", by Cobranch"
	default:
		line += ", asked by " + t.AssignedBy.DisplayName
	}
	switch {
	case t.Branch != "":
		line += ", on branch " + t.Branch + " of " + t.RepoSlug
	case t.RepoSlug != "":
		line += ", in " + t.RepoSlug
	}
	if t.Private {
		line += " (private to who asked)"
	}
	return line
}

// since is when the task got to its state: when it ended, started or was queued.
func since(t agents.Task) time.Time {
	switch {
	case !t.EndedAt.IsZero():
		return t.EndedAt
	case !t.StartedAt.IsZero():
		return t.StartedAt
	}
	return t.CreatedAt
}

// ownAgent reports whether the agent is the one the task's chat is a conversation with.
func ownAgent(chat Chat, agent uuid.UUID) bool {
	return chat.Conversation && chat.Agent == agent
}
