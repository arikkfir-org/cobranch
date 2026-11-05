package harness

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"

	"github.com/google/uuid"

	"cobranch.dev/internal/services/agents"
)

// askSpec describes ask, the chat's tool that waits for people, to the model.
var askSpec = Spec{
	Name: "ask",
	Description: "Asks a question in the thread of the chat this task talks in, and waits for the answer: what people post " +
		"there after it comes back as this call's result. Ask only what you can't go on without. The task waits meanwhile, " +
		"however long the answer takes, and its pod may stop and start again with a fresh workspace, so push what you " +
		"would keep first.",
	Parameters: json.RawMessage(`{"type":"object","properties":{` +
		`"question":{"type":"string","description":"The question, in Markdown"}},"required":["question"]}`),
}

// askArgs are ask's arguments.
type askArgs struct {
	Question string `json:"question"`
}

// askThread posts the question to the task's thread, moves the task to waiting, waits for the messages others post
// after it, and moves the task back to running. The call's key names the question, so a call run again in the task's
// next pod, once someone answered, posts nothing more and finds the answer. A pod that stops while it waits records
// nothing: the task waits on.
func (r *run) askThread(ctx context.Context, c Call) (string, error) {
	var a askArgs
	if err := decode(c.Arguments, &a); err != nil {
		return "", err
	}
	question, err := r.post(ctx, c.Key, a.Question, uuid.Nil)
	if err != nil {
		return "", err
	}
	if err := r.moveTo(ctx, agents.TaskWaiting); err != nil {
		return "", err
	}
	answers, err := r.await(ctx, question)
	if ctx.Err() != nil {
		return "", fmt.Errorf("waiting for an answer: %w", ctx.Err())
	}
	if moved := r.moveTo(ctx, agents.TaskRunning); moved != nil {
		return "", errors.Join(err, moved)
	}
	if err != nil {
		return "", err
	}
	return "Answered, in the thread after your question:\n\n" + r.showThread(answers), nil
}

// moveTo records that the task moves to state, unless it is there already.
func (r *run) moveTo(ctx context.Context, state agents.TaskState) error {
	if r.state() == state {
		return nil
	}
	return r.append(ctx, agents.EntryState, agents.StateChange{State: state})
}

// state is the task's state: as its transcript last changed it, or as it was when the harness took it.
func (r *run) state() agents.TaskState {
	for i := len(r.entries) - 1; i >= 0; i-- {
		var c agents.StateChange
		if r.entries[i].Kind == agents.EntryState && json.Unmarshal(r.entries[i].Body, &c) == nil {
			return c.State
		}
	}
	return r.work.Task.State
}

// await answers the messages others posted in the task's thread after the question, once there are any: it reads the
// thread, and while that holds none, waits for the chat's next change to it.
func (r *run) await(ctx context.Context, question uuid.UUID) ([]ChatMessage, error) {
	chat, _ := r.chat()
	for {
		after, err := r.cobranch.ChatCursor(ctx, r.work.OrgSlug, chat)
		if err != nil {
			return nil, fmt.Errorf("following the chat: %w", err)
		}
		messages, err := r.cobranch.Thread(ctx, r.work.OrgSlug, chat, r.thread)
		if err != nil {
			return nil, fmt.Errorf("reading the thread: %w", err)
		}
		answers, err := r.answers(messages, question)
		if err != nil || len(answers) > 0 {
			return answers, err
		}
		if err := r.awaitChange(ctx, chat, after); err != nil {
			return nil, err
		}
	}
}

// awaitChange waits until the chat's updates after the cursor change the task's thread.
func (r *run) awaitChange(ctx context.Context, chat Chat, after string) error {
	for {
		changed, next, err := r.cobranch.ChatUpdates(ctx, r.work.OrgSlug, chat, after)
		if err != nil {
			return fmt.Errorf("waiting for the chat's updates: %w", err)
		}
		if slices.Contains(changed, r.thread) {
			return nil
		}
		after = next
	}
}

// answers are the messages of the thread after the question, the thread's first when it started the thread, that
// others posted and nobody deleted.
func (r *run) answers(messages []ChatMessage, question uuid.UUID) ([]ChatMessage, error) {
	at := slices.IndexFunc(messages, func(m ChatMessage) bool { return m.ID == question })
	if at < 0 && question == r.thread && len(messages) > 0 {
		at = 0
	}
	if at < 0 {
		return nil, errors.New("the question isn't in the thread any more")
	}
	var out []ChatMessage
	for _, m := range messages[at+1:] {
		if !m.Deleted && m.Task != r.work.Task.Number {
			out = append(out, m)
		}
	}
	return out, nil
}
