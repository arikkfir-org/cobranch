package models

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"net/url"
	"strings"

	"cobranch.dev/internal/services/harness"
)

// gemini speaks to a model through Google's Gemini API.
type gemini struct {
	caller
	url      string
	settings Settings
}

// geminiRequest is a request of the Gemini API's generateContent.
type geminiRequest struct {
	SystemInstruction *geminiContent   `json:"systemInstruction,omitempty"`
	Contents          []geminiContent  `json:"contents"`
	Tools             []geminiTools    `json:"tools,omitempty"`
	GenerationConfig  geminiGeneration `json:"generationConfig"`
}

type geminiContent struct {
	Role  string       `json:"role,omitempty"`
	Parts []geminiPart `json:"parts"`
}

// geminiPart is a part of a content: text, a function's call, or its response. A thought is the model's own, which
// it isn't sent back; a signature is.
type geminiPart struct {
	Text             string              `json:"text,omitempty"`
	Thought          bool                `json:"thought,omitempty"`
	FunctionCall     *geminiFunctionCall `json:"functionCall,omitempty"`
	FunctionResponse *geminiFunctionCall `json:"functionResponse,omitempty"`
	ThoughtSignature string              `json:"thoughtSignature,omitempty"`
}

// geminiFunctionCall is a function's call, with its arguments, or its response.
type geminiFunctionCall struct {
	ID       string          `json:"id,omitempty"`
	Name     string          `json:"name"`
	Args     json.RawMessage `json:"args,omitempty"`
	Response json.RawMessage `json:"response,omitempty"`
}

type geminiTools struct {
	FunctionDeclarations []geminiFunction `json:"functionDeclarations"`
}

type geminiFunction struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
}

type geminiGeneration struct {
	MaxOutputTokens int `json:"maxOutputTokens"`
}

type geminiResponse struct {
	Candidates []struct {
		Content      geminiContent `json:"content"`
		FinishReason string        `json:"finishReason"`
	} `json:"candidates"`
	PromptFeedback struct {
		BlockReason string `json:"blockReason"`
	} `json:"promptFeedback"`
}

// Next asks the model for its next turn (harness.Model).
func (g gemini) Next(ctx context.Context, r harness.Request) (harness.Turn, error) {
	req := geminiRequest{GenerationConfig: geminiGeneration{MaxOutputTokens: g.settings.MaxTokens}}
	if r.System != "" {
		req.SystemInstruction = &geminiContent{Parts: []geminiPart{{Text: r.System}}}
	}
	for _, m := range r.Messages {
		req.Contents = append(req.Contents, geminiContentOf(m))
	}
	if len(r.Tools) > 0 {
		var declared geminiTools
		for _, t := range r.Tools {
			declared.FunctionDeclarations = append(declared.FunctionDeclarations, geminiFunction{Name: t.Name, Description: t.Description, Parameters: t.Parameters})
		}
		req.Tools = []geminiTools{declared}
	}
	var resp geminiResponse
	endpoint := g.url + "/v1beta/models/" + url.PathEscape(g.settings.Model) + ":generateContent"
	if err := g.post(ctx, endpoint, keyHeader(g.settings.Provider, g.settings.Key), req, &resp); err != nil {
		return harness.Turn{}, err
	}
	if len(resp.Candidates) == 0 {
		return harness.Turn{Stop: "blocked: " + resp.PromptFeedback.BlockReason}, nil
	}
	return geminiTurn(resp.Candidates[0].Content, resp.Candidates[0].FinishReason), nil
}

// geminiTurn is the turn a candidate's content holds. A call without an ID gets one, for its result to name.
func geminiTurn(c geminiContent, stop string) harness.Turn {
	t := harness.Turn{Stop: stop}
	var text []string
	for _, p := range c.Parts {
		switch {
		case p.FunctionCall != nil:
			id := p.FunctionCall.ID
			if id == "" {
				id = "call_" + rand.Text()[:12]
			}
			t.Calls = append(t.Calls, harness.Call{ID: id, Name: p.FunctionCall.Name, Arguments: object(p.FunctionCall.Args), Signature: p.ThoughtSignature})
		case p.Thought:
		default:
			text = append(text, p.Text)
			if t.Signature == "" {
				t.Signature = p.ThoughtSignature
			}
		}
	}
	t.Text = strings.Join(text, "")
	return t
}

// geminiContentOf is m as the Gemini API takes it: a user's results before its text, the model's text before its
// calls, each with the signature it came with.
func geminiContentOf(m harness.Message) geminiContent {
	if m.Role == harness.RoleAssistant {
		out := geminiContent{Role: "model"}
		if m.Text != "" || len(m.Calls) == 0 {
			out.Parts = append(out.Parts, geminiPart{Text: nonEmpty(m.Text), ThoughtSignature: m.Signature})
		}
		for _, c := range m.Calls {
			out.Parts = append(out.Parts, geminiPart{FunctionCall: &geminiFunctionCall{ID: c.ID, Name: c.Name, Args: object(c.Arguments)}, ThoughtSignature: c.Signature})
		}
		return out
	}
	out := geminiContent{Role: "user"}
	for _, r := range m.Results {
		key := "output"
		if r.Error {
			key = "error"
		}
		response, _ := json.Marshal(map[string]string{key: r.Text}) // a map of strings always marshals
		out.Parts = append(out.Parts, geminiPart{FunctionResponse: &geminiFunctionCall{ID: r.CallID, Name: r.Name, Response: response}})
	}
	if m.Text != "" {
		out.Parts = append(out.Parts, geminiPart{Text: m.Text})
	}
	return out
}

// nonEmpty is text, or emptyTurn for none.
func nonEmpty(text string) string {
	if text == "" {
		return emptyTurn
	}
	return text
}
