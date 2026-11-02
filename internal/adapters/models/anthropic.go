package models

import (
	"context"
	"encoding/json"
	"strings"

	"cobranch.dev/internal/services/harness"
)

// anthropicVersion is the version of Anthropic's Messages API the adapter speaks.
const anthropicVersion = "2023-06-01"

// emptyTurn stands in for the text of a turn that had neither text nor calls, since the Messages API takes no empty
// message.
const emptyTurn = "(This turn was empty.)"

// anthropic speaks to a model through Anthropic's Messages API.
type anthropic struct {
	caller
	url      string
	settings Settings
}

// anthropicRequest is a request of the Messages API.
type anthropicRequest struct {
	Model     string             `json:"model"`
	MaxTokens int                `json:"max_tokens"`
	System    string             `json:"system,omitempty"`
	Messages  []anthropicMessage `json:"messages"`
	Tools     []anthropicTool    `json:"tools,omitempty"`
}

type anthropicMessage struct {
	Role    string           `json:"role"`
	Content []anthropicBlock `json:"content"`
}

// anthropicBlock is a block of a message's content: text, a tool's use, or its result.
type anthropicBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text,omitempty"`
	ID        string          `json:"id,omitempty"`
	Name      string          `json:"name,omitempty"`
	Input     json.RawMessage `json:"input,omitempty"`
	ToolUseID string          `json:"tool_use_id,omitempty"`
	Content   string          `json:"content,omitempty"`
	IsError   bool            `json:"is_error,omitempty"`
}

type anthropicTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"input_schema"`
}

type anthropicResponse struct {
	Model      string           `json:"model"`
	Content    []anthropicBlock `json:"content"`
	StopReason string           `json:"stop_reason"`
	Usage      struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
}

// chat asks the model for its next turn (adapter).
func (a anthropic) chat(ctx context.Context, r harness.Request) (harness.Turn, answered, error) {
	req := anthropicRequest{Model: a.settings.Model, MaxTokens: a.settings.MaxTokens, System: r.System}
	for _, m := range r.Messages {
		req.Messages = append(req.Messages, anthropicMessageOf(m))
	}
	for _, t := range r.Tools {
		req.Tools = append(req.Tools, anthropicTool{Name: t.Name, Description: t.Description, InputSchema: t.Parameters})
	}
	var resp anthropicResponse
	headers := keyHeader(a.settings.Provider, a.settings.Key)
	headers["anthropic-version"] = anthropicVersion
	if err := a.post(ctx, a.url+"/v1/messages", headers, req, &resp); err != nil {
		return harness.Turn{}, answered{}, err
	}
	meta := answered{model: resp.Model, input: resp.Usage.InputTokens, output: resp.Usage.OutputTokens}
	if resp.StopReason == "refusal" {
		meta.blocked = resp.StopReason
	}
	t := harness.Turn{Stop: resp.StopReason}
	var text []string
	for _, b := range resp.Content {
		switch b.Type {
		case "text":
			text = append(text, b.Text)
		case "tool_use":
			t.Calls = append(t.Calls, harness.Call{ID: b.ID, Name: b.Name, Arguments: object(b.Input)})
		}
	}
	t.Text = strings.Join(text, "\n\n")
	return t, meta, nil
}

// anthropicMessageOf is m as the Messages API takes it: a user's results before its text, and an assistant's text
// before its calls.
func anthropicMessageOf(m harness.Message) anthropicMessage {
	out := anthropicMessage{Role: string(m.Role)}
	for _, r := range m.Results {
		out.Content = append(out.Content, anthropicBlock{Type: "tool_result", ToolUseID: r.CallID, Content: r.Text, IsError: r.Error})
	}
	if m.Text != "" {
		out.Content = append(out.Content, anthropicBlock{Type: "text", Text: m.Text})
	}
	for _, c := range m.Calls {
		out.Content = append(out.Content, anthropicBlock{Type: "tool_use", ID: c.ID, Name: c.Name, Input: object(c.Arguments)})
	}
	if len(out.Content) == 0 {
		out.Content = []anthropicBlock{{Type: "text", Text: emptyTurn}}
	}
	return out
}
