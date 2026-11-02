package restclient

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"cobranch.dev/internal/services/harness"
)

// Thread lists the messages of a thread of one of the organization's chats, a branch's or an issue's, in the order
// they were posted (harness.Cobranch).
func (c *API) Thread(ctx context.Context, org string, chat harness.Chat, threadID uuid.UUID) ([]harness.ChatMessage, error) {
	var (
		thread *ChatThread
		status int
		body   []byte
	)
	if chat.Branch != "" {
		resp, err := c.api.GetChatThreadWithResponse(ctx, org, chat.Repo, chat.Branch, threadID, nil)
		if err != nil {
			return nil, fmt.Errorf("reading thread %s: %w", threadID, err)
		}
		thread, status, body = resp.JSON200, resp.StatusCode(), resp.Body
	} else {
		resp, err := c.api.GetIssueChatThreadWithResponse(ctx, org, int(chat.Issue), threadID, nil)
		if err != nil {
			return nil, fmt.Errorf("reading thread %s: %w", threadID, err)
		}
		thread, status, body = resp.JSON200, resp.StatusCode(), resp.Body
	}
	if thread == nil {
		return nil, refused("reading thread "+threadID.String(), status, body)
	}
	var out []harness.ChatMessage
	if thread.Messages != nil {
		for _, m := range *thread.Messages {
			out = append(out, chatMessage(m))
		}
	}
	return out, nil
}

// StartThread starts thread id of one of the organization's chats with its first message; starting it again changes
// nothing (harness.Cobranch).
func (c *API) StartThread(ctx context.Context, org string, chat harness.Chat, id uuid.UUID, body string) error {
	in := ChatThreadInput{Id: &id, Body: body}
	var (
		started bool
		status  int
		answer  []byte
	)
	if chat.Branch != "" {
		resp, err := c.api.StartChatThreadWithResponse(ctx, org, chat.Repo, chat.Branch, in)
		if err != nil {
			return fmt.Errorf("starting thread %s: %w", id, err)
		}
		started, status, answer = resp.JSON201 != nil, resp.StatusCode(), resp.Body
	} else {
		resp, err := c.api.StartIssueChatThreadWithResponse(ctx, org, int(chat.Issue), in)
		if err != nil {
			return fmt.Errorf("starting thread %s: %w", id, err)
		}
		started, status, answer = resp.JSON201 != nil, resp.StatusCode(), resp.Body
	}
	if !started {
		return refused("starting thread "+id.String(), status, answer)
	}
	return nil
}

// Post posts message id to a thread of one of the organization's chats; posting it again changes nothing
// (harness.Cobranch).
func (c *API) Post(ctx context.Context, org string, chat harness.Chat, threadID, id uuid.UUID, body string) error {
	in := ChatMessageInput{Id: &id, Body: body}
	var (
		posted bool
		status int
		answer []byte
	)
	if chat.Branch != "" {
		resp, err := c.api.PostChatMessageWithResponse(ctx, org, chat.Repo, chat.Branch, threadID, in)
		if err != nil {
			return fmt.Errorf("posting message %s: %w", id, err)
		}
		posted, status, answer = resp.JSON201 != nil, resp.StatusCode(), resp.Body
	} else {
		resp, err := c.api.PostIssueChatMessageWithResponse(ctx, org, int(chat.Issue), threadID, in)
		if err != nil {
			return fmt.Errorf("posting message %s: %w", id, err)
		}
		posted, status, answer = resp.JSON201 != nil, resp.StatusCode(), resp.Body
	}
	if !posted {
		return refused("posting message "+id.String(), status, answer)
	}
	return nil
}

// chatMessage is m as the harness reads it.
func chatMessage(m ChatMessage) harness.ChatMessage {
	out := harness.ChatMessage{ID: m.Id, At: m.CreatedAt, Body: m.Body, Deleted: m.DeletedAt != nil, Task: deref(m.TaskNumber, 0)}
	if m.Author != nil {
		out.Author = m.Author.DisplayName
	}
	return out
}
