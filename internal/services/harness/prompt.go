package harness

import "strconv"

// request is what the model is asked for its next turn, from the transcript's state.
func (r *run) request(s state) Request {
	return Request{System: r.system(), Messages: s.messages, Tools: r.specs()}
}

// system is the system prompt: who the agent is and what it works on, how a task ends, and the agent's standing
// instructions.
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
	return prompt
}

// where tells the model where people follow the task, and where its work goes: its chat's thread, and the clone of its
// repository in the workspace.
func (r *run) where() string {
	var out string
	if chat, ok := r.chat(); ok {
		name := "branch " + chat.Branch + "'s chat"
		if chat.Branch == "" {
			name = "issue #" + strconv.FormatInt(chat.Issue, 10) + "'s chat"
		}
		out += "\n\nPeople follow the task in a thread of " + name + ": read it with read_thread, and post there with " +
			"post_message."
	}
	if b := r.clone.Branch; b != "" {
		out += "\n\nThe workspace holds a clone of repository " + r.work.Task.RepoSlug + ", on branch " + b + ". Commit there, as " +
			r.work.AgentEmail + ", and push branch " + b + " with git: what you don't push is lost when the task ends."
		if r.work.Task.Branch == "" {
			out += " The branch is the task's own: push no other."
		}
	}
	return out
}
