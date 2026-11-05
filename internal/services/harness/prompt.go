package harness

import (
	"strconv"

	"github.com/google/uuid"
)

// request is what the model is asked for its next turn, from the transcript's state.
func (r *run) request(s state) Request {
	return Request{System: r.system(), Messages: s.messages, Tools: r.specs()}
}

// system is the system prompt: who the agent is and what it works on, how a task ends, the agent's standing
// instructions, and its task's kind's.
func (r *run) system() string {
	t := r.work.Task
	prompt := "You are " + t.Agent.DisplayName + ", an agent of the organization " + r.work.OrgSlug + " on Cobranch, " +
		"working on its task #" + strconv.FormatInt(t.Number, 10)
	if t.AssignedBy != nil && !t.ByCobranch() {
		prompt += ", which " + t.AssignedBy.DisplayName + " assigned to you"
	}
	prompt += "."
	switch {
	case t.Branch != "":
		prompt += " The task works on branch " + t.Branch + " of repository " + t.RepoSlug + "."
	case t.RepoSlug != "":
		prompt += " The task works in repository " + t.RepoSlug + "."
	}
	prompt += "\n\nWork through the tools you have: your messages reach no one but the task's transcript. Once the task " +
		"is done, or you find you can't do it, call finish, which ends the task."
	prompt += r.where()
	if r.work.AgentInstructions != "" {
		prompt += "\n\nYour standing instructions, which every task of yours starts from:\n\n" + r.work.AgentInstructions
	}
	if r.kit.Instructions != "" {
		prompt += "\n\n" + r.kit.Instructions
	}
	return prompt
}

// where tells the model where people follow the task, and where its work goes: its chat's thread, and the clone of its
// repository in the workspace.
func (r *run) where() string {
	var out string
	if chat, ok := r.chat(); ok {
		out += "\n\n" + r.followers(chat) + ": read it with read_thread, post there with post_message, and ask there " +
			"with ask when you can't go on without an answer."
		if ownAgent(chat, r.work.Task.Agent.ID) {
			out += " When they ask about your work, tasks lists what you work on, your queue and what you ended last."
		}
	}
	switch c := r.clone; {
	case c.Commit != "":
		out += "\n\nThe workspace holds a clone of repository " + r.work.Task.RepoSlug + " at commit " + c.Commit + " of branch " +
			c.Branch + ", which the task works at: push nothing."
	case c.Branch != "":
		out += "\n\nThe workspace holds a clone of repository " + r.work.Task.RepoSlug + ", on branch " + c.Branch + ". Commit there, as " +
			r.work.AgentEmail + ", and push branch " + c.Branch + " with git: what you don't push is lost when the task ends."
		if r.work.Task.Branch == "" {
			out += " The branch is the task's own: push no other."
		}
	}
	return out
}

// followers says who follows the task in its chat's thread, and where.
func (r *run) followers(chat Chat) string {
	assigner := "its assigner"
	if a := r.work.Task.AssignedBy; a != nil {
		assigner = a.DisplayName
	}
	switch {
	case ownAgent(chat, r.work.Task.Agent.ID):
		return assigner + " follows the task in a thread of their private conversation with you, which no one else reads"
	case chat.Conversation && chat.Agent != uuid.Nil:
		return assigner + " follows the task in a thread of their private conversation with another agent of the " +
			"organization, which no one else reads"
	case chat.Conversation && chat.Repo != "":
		return assigner + " follows the task in a thread of their private conversation about repository " + chat.Repo +
			", which no one else reads"
	case chat.Conversation:
		return assigner + " follows the task in a thread of their private conversation about the organization, which no " +
			"one else reads"
	case chat.Branch != "":
		return "People follow the task in a thread of branch " + chat.Branch + "'s chat"
	}
	return "People follow the task in a thread of issue #" + strconv.FormatInt(chat.Issue, 10) + "'s chat"
}
