package harness

import "strconv"

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
	if r.work.AgentInstructions != "" {
		prompt += "\n\nYour standing instructions, which every task of yours starts from:\n\n" + r.work.AgentInstructions
	}
	if r.kit.Instructions != "" {
		prompt += "\n\n" + r.kit.Instructions
	}
	return prompt
}
