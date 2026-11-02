package harness

import (
	"encoding/json"
	"fmt"
	"strconv"
	"unicode/utf8"

	"cobranch.dev/internal/services/agents"
)

// byHarness marks an input entry the harness wrote, rather than the task's assigner or a person.
const byHarness = "harness"

// nudge is the input the harness gives a model that ended its turn without calling a tool.
const nudge = "Your last turn called no tool. Carry on with the tools, or call finish to end the task."

// input is an input entry's body: what the task began with, or a message since.
type input struct {
	Text string `json:"text"`
	// By says who wrote it, when the harness did; empty for the task itself.
	By string `json:"by,omitempty"`
}

// result is a result entry's body: a call's output, cut to MaxOutput.
type result struct {
	CallID string `json:"callId"`
	Name   string `json:"name"`
	Text   string `json:"text"`
	Error  bool   `json:"error,omitempty"`
	// Cut says Text is the first MaxOutput bytes of an output of Size bytes.
	Cut  bool `json:"cut,omitempty"`
	Size int  `json:"size,omitempty"`
}

// cut cuts the result's text to MaxOutput bytes, and says so.
func (r result) cut() result {
	if len(r.Text) > MaxOutput {
		r.Size, r.Cut, r.Text = len(r.Text), true, cut(r.Text, MaxOutput)
	}
	return r
}

// forModel is the result as the model sees it, which says what was cut.
func (r result) forModel() Result {
	text := r.Text
	if r.Cut {
		text += "\n\n[Cut: the output was " + strconv.Itoa(r.Size) + " bytes, and this is its first " + strconv.Itoa(len(r.Text)) + ".]"
	}
	return Result{CallID: r.CallID, Name: r.Name, Text: text, Error: r.Error}
}

// state is what a transcript says of its task: whether it ended, how many turns its model took, the calls of the last
// turn that have no result yet, whether that turn called nothing and is the last entry, and the conversation so far.
type state struct {
	ended    bool
	turns    int
	pending  []Call
	idle     bool
	messages []Message
}

// read reads a transcript's entries into its state.
func read(entries []agents.Entry) (state, error) {
	var s state
	var last *Turn
	answered := map[string]bool{}
	for _, e := range entries {
		var err error
		switch e.Kind {
		case agents.EntryInput:
			var in input
			err = json.Unmarshal(e.Body, &in)
			s.user(Message{Text: in.Text})
		case agents.EntryTurn:
			last, answered = &Turn{}, map[string]bool{}
			err = json.Unmarshal(e.Body, last)
			s.turns++
			s.messages = append(s.messages, Message{Role: RoleAssistant, Text: last.Text, Calls: last.Calls, Signature: last.Signature})
		case agents.EntryResult:
			var r result
			err = json.Unmarshal(e.Body, &r)
			answered[r.CallID] = true
			s.user(Message{Results: []Result{r.forModel()}})
		case agents.EntryState:
			var c agents.StateChange
			err = json.Unmarshal(e.Body, &c)
			s.ended = s.ended || c.State == agents.TaskSucceeded || c.State == agents.TaskFailed
		}
		if err != nil {
			return state{}, fmt.Errorf("reading entry %d, %s: %w", e.Seq, e.Kind, err)
		}
		if e.Kind != agents.EntryState {
			s.idle = e.Kind == agents.EntryTurn && len(last.Calls) == 0
		}
	}
	if last != nil {
		for _, c := range last.Calls {
			if !answered[c.ID] {
				s.pending = append(s.pending, c)
			}
		}
	}
	return s, nil
}

// user adds m, of RoleUser, to the conversation: to its last message when that is the user's too, so that the results
// of a turn's calls and the text after them are one message.
func (s *state) user(m Message) {
	n := len(s.messages)
	if n == 0 || s.messages[n-1].Role != RoleUser {
		m.Role = RoleUser
		s.messages = append(s.messages, m)
		return
	}
	last := &s.messages[n-1]
	switch {
	case m.Text == "":
	case last.Text == "":
		last.Text = m.Text
	default:
		last.Text += "\n\n" + m.Text
	}
	last.Results = append(last.Results, m.Results...)
}

// recordable is t as a turn entry can hold it: calls whose arguments aren't JSON carry them as a string, which the
// tool refuses; and a turn too large for an entry keeps its text's first MaxOutput bytes and drops its calls'
// arguments, so that each call fails, asking for smaller ones.
func recordable(t Turn) Turn {
	t.Calls = append([]Call(nil), t.Calls...)
	for i, c := range t.Calls {
		if !json.Valid(c.Arguments) {
			t.Calls[i].Arguments = quoted(string(c.Arguments))
		}
	}
	if raw, err := json.Marshal(t); err == nil && len(raw) <= agents.MaxEntry {
		return t
	}
	t.Text, t.Signature = cut(t.Text, MaxOutput), ""
	for i := range t.Calls {
		t.Calls[i].Arguments, t.Calls[i].Signature, t.Calls[i].Dropped = json.RawMessage(`{}`), "", true
	}
	return t
}

// quoted is s as a JSON string.
func quoted(s string) json.RawMessage {
	raw, err := json.Marshal(s)
	if err != nil {
		return json.RawMessage(`""`) // a string always marshals; nothing else to say
	}
	return raw
}

// cut answers at most the first n bytes of s, ending at a whole character.
func cut(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}
