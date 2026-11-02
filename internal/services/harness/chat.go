package harness

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/google/uuid"
)

// The tools of the chat a task talks in, which the harness runs itself through Cobranch's API.
var (
	readThreadSpec = Spec{
		Name: "read_thread",
		Description: "Reads the thread of the chat this task talks in: its messages, oldest first, with who posted each " +
			"and when, yours marked. People read that thread and answer you there.",
		Parameters: json.RawMessage(`{"type":"object","properties":{},"required":[]}`),
	}
	postMessageSpec = Spec{
		Name: "post_message",
		Description: "Posts a message, in Markdown, to the thread of the chat this task talks in; the first one starts the " +
			"thread. Post what people should know, such as what you found, did or pushed: nothing else you write reaches " +
			"anyone but the task's transcript.",
		Parameters: json.RawMessage(`{"type":"object","properties":{` +
			`"body":{"type":"string","description":"The message, in Markdown"}},"required":["body"]}`),
	}
)

// chatTool is one of the chat's tools.
type chatTool struct {
	spec Spec
	run  func(ctx context.Context, c Call) (string, error)
}

// Spec describes the tool (Tool).
func (t chatTool) Spec() Spec { return t.spec }

// Run runs the call (Tool).
func (t chatTool) Run(ctx context.Context, c Call) (string, error) { return t.run(ctx, c) }

// chat is the chat the task talks in: its branch's, or else its issue's; false for a task with neither.
func (r *run) chat() (Chat, bool) {
	t := r.work.Task
	switch {
	case t.Branch != "":
		return Chat{Repo: t.RepoSlug, Branch: t.Branch}, true
	case r.work.IssueNumber != 0:
		return Chat{Issue: r.work.IssueNumber}, true
	}
	return Chat{}, false
}

// chatTools are the tools of the chat the task talks in; none for a task without one.
func (r *run) chatTools() []Tool {
	if _, ok := r.chat(); !ok {
		return nil
	}
	return []Tool{chatTool{spec: readThreadSpec, run: r.readThread}, chatTool{spec: postMessageSpec, run: r.postMessage},
		chatTool{spec: askSpec, run: r.askThread}}
}

// readThread answers the messages of the task's thread, the newest that fit in a result.
func (r *run) readThread(ctx context.Context, c Call) (string, error) {
	if err := decode(c.Arguments, &struct{}{}); err != nil {
		return "", err
	}
	if r.thread == uuid.Nil {
		return "The task has no thread yet: post_message starts one.", nil
	}
	chat, _ := r.chat()
	messages, err := r.cobranch.Thread(ctx, r.work.OrgSlug, chat, r.thread)
	if err != nil {
		return "", fmt.Errorf("reading the thread: %w", err)
	}
	return r.showThread(messages), nil
}

// showThread is the thread's messages as the model reads them, oldest first: the newest that fit in MaxOutput, saying
// how many older ones it left out.
func (r *run) showThread(messages []ChatMessage) string {
	if len(messages) == 0 {
		return "The thread has no messages."
	}
	const room = MaxOutput - 256
	shown, size := make([]string, 0, len(messages)), 0
	for i := len(messages) - 1; i >= 0; i-- {
		m := r.showMessage(messages[i])
		if size+len(m) > room && len(shown) > 0 {
			break
		}
		shown, size = append(shown, cut(m, room)), size+len(m)+2
	}
	var b strings.Builder
	if left := len(messages) - len(shown); left > 0 {
		b.WriteString("[" + strconv.Itoa(left) + " older messages left out]\n\n")
	}
	for i := len(shown) - 1; i >= 0; i-- {
		b.WriteString(shown[i])
		if i > 0 {
			b.WriteString("\n\n")
		}
	}
	return b.String()
}

// showMessage is a message as the model reads it: who posted it, marking the task's own, and when, then its body.
func (r *run) showMessage(m ChatMessage) string {
	who := m.Author
	switch {
	case m.Task != 0 && m.Task == r.work.Task.Number:
		who = "you"
	case who == "":
		who = "someone since gone"
	}
	if m.Task != 0 && m.Task != r.work.Task.Number {
		who += " (an agent, on task #" + strconv.FormatInt(m.Task, 10) + ")"
	}
	head := "From " + who + " at " + m.At.UTC().Format("2006-01-02 15:04 UTC") + ", message " + m.ID.String() + ":\n"
	if m.Deleted {
		return head + "(deleted)"
	}
	return head + m.Body
}

// postArgs are post_message's arguments.
type postArgs struct {
	Body string `json:"body"`
}

// postMessage posts the message to the task's thread, starting it when the task has none.
func (r *run) postMessage(ctx context.Context, c Call) (string, error) {
	var a postArgs
	if err := decode(c.Arguments, &a); err != nil {
		return "", err
	}
	id, err := r.post(ctx, c.Key, a.Body)
	if err != nil {
		return "", err
	}
	if id == r.thread {
		return "Posted, starting the task's thread.", nil
	}
	return "Posted.", nil
}

// post posts body to the task's thread, starting it when the task has none, and answers the ID that key names: the
// message's, or the thread's it started. Since key names it, a call run again after a restart posts nothing more.
func (r *run) post(ctx context.Context, key, body string) (uuid.UUID, error) {
	if strings.TrimSpace(body) == "" {
		return uuid.Nil, errors.New("the message is empty")
	}
	id, err := uuid.Parse(key)
	if err != nil {
		return uuid.Nil, fmt.Errorf("the call has no key to name its message: %w", err)
	}
	chat, _ := r.chat()
	switch r.thread {
	case id: // this call started the thread before a restart
		return id, nil
	case uuid.Nil:
		if err := r.cobranch.StartThread(ctx, r.work.OrgSlug, chat, id, body); err != nil {
			return uuid.Nil, fmt.Errorf("starting the thread: %w", err)
		}
		r.thread = id
		return id, nil
	}
	if err := r.cobranch.Post(ctx, r.work.OrgSlug, chat, r.thread, id, body); err != nil {
		return uuid.Nil, fmt.Errorf("posting the message: %w", err)
	}
	return id, nil
}

// decode reads a call's arguments into args, refusing fields it doesn't take.
func decode(raw json.RawMessage, args any) error {
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(args); err != nil {
		return fmt.Errorf("reading the arguments: %w", err)
	}
	return nil
}
