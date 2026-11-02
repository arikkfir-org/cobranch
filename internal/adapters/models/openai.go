package models

import (
	"context"
	"encoding/json"
	"fmt"

	"cobranch.dev/internal/services/harness"
)

// openAI speaks to a model through OpenAI's Chat Completions API, which xAI and DeepSeek speak too, at their own
// addresses.
type openAI struct {
	caller
	url      string
	settings Settings
}

// openAIRequest is a request of the Chat Completions API. It leaves the most tokens a turn takes to the provider,
// since they don't agree on how to name it.
type openAIRequest struct {
	Model    string          `json:"model"`
	Messages []openAIMessage `json:"messages"`
	Tools    []openAITool    `json:"tools,omitempty"`
}

type openAIMessage struct {
	Role       string           `json:"role"`
	Content    *string          `json:"content"`
	ToolCalls  []openAIToolCall `json:"tool_calls,omitempty"`
	ToolCallID string           `json:"tool_call_id,omitempty"`
}

type openAIToolCall struct {
	ID       string         `json:"id"`
	Type     string         `json:"type"`
	Function openAIFunction `json:"function"`
}

// openAIFunction is a function's call, whose arguments are a string of JSON, or a function a tool declares.
type openAIFunction struct {
	Name        string          `json:"name"`
	Arguments   string          `json:"arguments,omitempty"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
}

type openAITool struct {
	Type     string         `json:"type"`
	Function openAIFunction `json:"function"`
}

type openAIResponse struct {
	Model   string `json:"model"`
	Choices []struct {
		Message      openAIMessage `json:"message"`
		FinishReason string        `json:"finish_reason"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
	} `json:"usage"`
}

// chat asks the model for its next turn (adapter).
func (o openAI) chat(ctx context.Context, r harness.Request) (harness.Turn, answered, error) {
	req := openAIRequest{Model: o.settings.Model, Messages: []openAIMessage{{Role: "system", Content: &r.System}}}
	for _, m := range r.Messages {
		req.Messages = append(req.Messages, openAIMessagesOf(m)...)
	}
	for _, t := range r.Tools {
		req.Tools = append(req.Tools, openAITool{Type: "function", Function: openAIFunction{Name: t.Name, Description: t.Description, Parameters: t.Parameters}})
	}
	var resp openAIResponse
	if err := o.post(ctx, o.url+"/chat/completions", map[string]string{"Authorization": "Bearer " + o.settings.Key}, req, &resp); err != nil {
		return harness.Turn{}, answered{}, err
	}
	meta := answered{model: resp.Model, input: resp.Usage.PromptTokens, output: resp.Usage.CompletionTokens}
	if len(resp.Choices) == 0 {
		return harness.Turn{}, meta, fmt.Errorf("%s answered no choice", o.provider)
	}
	choice := resp.Choices[0]
	if choice.FinishReason == "content_filter" {
		meta.blocked = choice.FinishReason
	}
	t := harness.Turn{Stop: choice.FinishReason}
	if choice.Message.Content != nil {
		t.Text = *choice.Message.Content
	}
	for _, c := range choice.Message.ToolCalls {
		args := json.RawMessage(c.Function.Arguments)
		if !json.Valid(args) {
			args = quote(c.Function.Arguments)
		}
		t.Calls = append(t.Calls, harness.Call{ID: c.ID, Name: c.Function.Name, Arguments: args})
	}
	return t, meta, nil
}

// openAIMessagesOf is m as the Chat Completions API takes it: a user's results each a tool's message, before its text;
// an assistant's text and calls one message.
func openAIMessagesOf(m harness.Message) []openAIMessage {
	if m.Role == harness.RoleAssistant {
		out := openAIMessage{Role: "assistant"}
		if m.Text != "" || len(m.Calls) == 0 {
			out.Content = &m.Text
		}
		for _, c := range m.Calls {
			out.ToolCalls = append(out.ToolCalls, openAIToolCall{ID: c.ID, Type: "function",
				Function: openAIFunction{Name: c.Name, Arguments: string(object(c.Arguments))}})
		}
		return []openAIMessage{out}
	}
	var out []openAIMessage
	for _, r := range m.Results {
		text := outcome(r)
		out = append(out, openAIMessage{Role: "tool", ToolCallID: r.CallID, Content: &text})
	}
	if m.Text != "" {
		out = append(out, openAIMessage{Role: "user", Content: &m.Text})
	}
	return out
}

// quote is s as a JSON string.
func quote(s string) json.RawMessage {
	raw, err := json.Marshal(s)
	if err != nil {
		return json.RawMessage(`""`) // a string always marshals; nothing else to say
	}
	return raw
}
