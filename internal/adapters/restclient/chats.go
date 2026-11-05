package restclient

import (
	"context"
	"fmt"
	"slices"

	"github.com/google/uuid"

	"cobranch.dev/internal/services/harness"
)

// reply is what a call of a chat answered: the value it decoded, nil when it refused, its status and its body.
type reply[T any] struct {
	value  *T
	status int
	body   []byte
}

// Thread lists the messages of a thread of one of the organization's chats, a branch's, an issue's or a private
// conversation, in the order they were posted (harness.Cobranch).
func (c *API) Thread(ctx context.Context, org string, chat harness.Chat, threadID uuid.UUID) ([]harness.ChatMessage, error) {
	r, err := c.thread(ctx, org, chat, threadID)
	if err != nil {
		return nil, fmt.Errorf("reading thread %s: %w", threadID, err)
	}
	if r.value == nil {
		return nil, refused("reading thread "+threadID.String(), r.status, r.body)
	}
	var out []harness.ChatMessage
	if r.value.Messages != nil {
		for _, m := range *r.value.Messages {
			out = append(out, chatMessage(m))
		}
	}
	return out, nil
}

// thread reads one of the chat's threads.
func (c *API) thread(ctx context.Context, org string, chat harness.Chat, id uuid.UUID) (reply[ChatThread], error) {
	switch {
	case chat.Conversation && chat.Agent != uuid.Nil:
		r, err := c.api.GetAgentConversationThreadWithResponse(ctx, org, chat.Agent, id, nil)
		if err != nil {
			return reply[ChatThread]{}, err //nolint:wrapcheck // Thread wraps it
		}
		return reply[ChatThread]{r.JSON200, r.StatusCode(), r.Body}, nil
	case chat.Conversation && chat.Repo != "":
		r, err := c.api.GetRepositoryConversationThreadWithResponse(ctx, org, chat.Repo, id, nil)
		if err != nil {
			return reply[ChatThread]{}, err //nolint:wrapcheck // Thread wraps it
		}
		return reply[ChatThread]{r.JSON200, r.StatusCode(), r.Body}, nil
	case chat.Conversation:
		r, err := c.api.GetOrganizationConversationThreadWithResponse(ctx, org, id, nil)
		if err != nil {
			return reply[ChatThread]{}, err //nolint:wrapcheck // Thread wraps it
		}
		return reply[ChatThread]{r.JSON200, r.StatusCode(), r.Body}, nil
	case chat.Branch != "":
		r, err := c.api.GetChatThreadWithResponse(ctx, org, chat.Repo, chat.Branch, id, nil)
		if err != nil {
			return reply[ChatThread]{}, err //nolint:wrapcheck // Thread wraps it
		}
		return reply[ChatThread]{r.JSON200, r.StatusCode(), r.Body}, nil
	}
	r, err := c.api.GetIssueChatThreadWithResponse(ctx, org, int(chat.Issue), id, nil)
	if err != nil {
		return reply[ChatThread]{}, err //nolint:wrapcheck // Thread wraps it
	}
	return reply[ChatThread]{r.JSON200, r.StatusCode(), r.Body}, nil
}

// StartThread starts thread id of one of the organization's chats with its first message, which names the artifact as
// its card unless it is uuid.Nil; starting it again changes nothing (harness.Cobranch).
func (c *API) StartThread(ctx context.Context, org string, chat harness.Chat, id uuid.UUID, body string, artifact uuid.UUID) error {
	r, err := c.start(ctx, org, chat, ChatThreadInput{Id: &id, Body: body, ArtifactId: card(artifact)})
	if err != nil {
		return fmt.Errorf("starting thread %s: %w", id, err)
	}
	if r.value == nil {
		return refused("starting thread "+id.String(), r.status, r.body)
	}
	return nil
}

// start starts one of the chat's threads.
func (c *API) start(ctx context.Context, org string, chat harness.Chat, in ChatThreadInput) (reply[ChatThread], error) {
	switch {
	case chat.Conversation && chat.Agent != uuid.Nil:
		r, err := c.api.StartAgentConversationThreadWithResponse(ctx, org, chat.Agent, in)
		if err != nil {
			return reply[ChatThread]{}, err //nolint:wrapcheck // StartThread wraps it
		}
		return reply[ChatThread]{r.JSON201, r.StatusCode(), r.Body}, nil
	case chat.Conversation && chat.Repo != "":
		r, err := c.api.StartRepositoryConversationThreadWithResponse(ctx, org, chat.Repo, in)
		if err != nil {
			return reply[ChatThread]{}, err //nolint:wrapcheck // StartThread wraps it
		}
		return reply[ChatThread]{r.JSON201, r.StatusCode(), r.Body}, nil
	case chat.Conversation:
		r, err := c.api.StartOrganizationConversationThreadWithResponse(ctx, org, in)
		if err != nil {
			return reply[ChatThread]{}, err //nolint:wrapcheck // StartThread wraps it
		}
		return reply[ChatThread]{r.JSON201, r.StatusCode(), r.Body}, nil
	case chat.Branch != "":
		r, err := c.api.StartChatThreadWithResponse(ctx, org, chat.Repo, chat.Branch, in)
		if err != nil {
			return reply[ChatThread]{}, err //nolint:wrapcheck // StartThread wraps it
		}
		return reply[ChatThread]{r.JSON201, r.StatusCode(), r.Body}, nil
	}
	r, err := c.api.StartIssueChatThreadWithResponse(ctx, org, int(chat.Issue), in)
	if err != nil {
		return reply[ChatThread]{}, err //nolint:wrapcheck // StartThread wraps it
	}
	return reply[ChatThread]{r.JSON201, r.StatusCode(), r.Body}, nil
}

// Post posts message id to a thread of one of the organization's chats, naming the artifact as its card unless it is
// uuid.Nil; posting it again changes nothing (harness.Cobranch).
func (c *API) Post(ctx context.Context, org string, chat harness.Chat, threadID, id uuid.UUID, body string, artifact uuid.UUID) error {
	r, err := c.post(ctx, org, chat, threadID, ChatMessageInput{Id: &id, Body: body, ArtifactId: card(artifact)})
	if err != nil {
		return fmt.Errorf("posting message %s: %w", id, err)
	}
	if r.value == nil {
		return refused("posting message "+id.String(), r.status, r.body)
	}
	return nil
}

// post posts a message to one of the chat's threads.
func (c *API) post(ctx context.Context, org string, chat harness.Chat, threadID uuid.UUID, in ChatMessageInput) (reply[ChatMessage], error) {
	switch {
	case chat.Conversation && chat.Agent != uuid.Nil:
		r, err := c.api.PostAgentConversationMessageWithResponse(ctx, org, chat.Agent, threadID, in)
		if err != nil {
			return reply[ChatMessage]{}, err //nolint:wrapcheck // Post wraps it
		}
		return reply[ChatMessage]{r.JSON201, r.StatusCode(), r.Body}, nil
	case chat.Conversation && chat.Repo != "":
		r, err := c.api.PostRepositoryConversationMessageWithResponse(ctx, org, chat.Repo, threadID, in)
		if err != nil {
			return reply[ChatMessage]{}, err //nolint:wrapcheck // Post wraps it
		}
		return reply[ChatMessage]{r.JSON201, r.StatusCode(), r.Body}, nil
	case chat.Conversation:
		r, err := c.api.PostOrganizationConversationMessageWithResponse(ctx, org, threadID, in)
		if err != nil {
			return reply[ChatMessage]{}, err //nolint:wrapcheck // Post wraps it
		}
		return reply[ChatMessage]{r.JSON201, r.StatusCode(), r.Body}, nil
	case chat.Branch != "":
		r, err := c.api.PostChatMessageWithResponse(ctx, org, chat.Repo, chat.Branch, threadID, in)
		if err != nil {
			return reply[ChatMessage]{}, err //nolint:wrapcheck // Post wraps it
		}
		return reply[ChatMessage]{r.JSON201, r.StatusCode(), r.Body}, nil
	}
	r, err := c.api.PostIssueChatMessageWithResponse(ctx, org, int(chat.Issue), threadID, in)
	if err != nil {
		return reply[ChatMessage]{}, err //nolint:wrapcheck // Post wraps it
	}
	return reply[ChatMessage]{r.JSON201, r.StatusCode(), r.Body}, nil
}

// card is the artifact a message names as its card; nil for uuid.Nil.
func card(artifact uuid.UUID) *uuid.UUID {
	if artifact == uuid.Nil {
		return nil
	}
	return &artifact
}

// chatMessage is m as the harness reads it.
func chatMessage(m ChatMessage) harness.ChatMessage {
	out := harness.ChatMessage{ID: m.Id, At: m.CreatedAt, Body: m.Body, Deleted: m.DeletedAt != nil, Task: deref(m.TaskNumber, 0)}
	if m.Author != nil {
		out.Author = m.Author.DisplayName
	}
	return out
}

// chatWait is how long a read of a chat's updates waits for one, the most the REST server waits.
const chatWait = 15

// threadsJSON is what a page of a chat's threads answers, and updatesJSON what a read of its updates answers, as the
// generated client decodes them for every chat.
type (
	threadsJSON = struct {
		After   string       `json:"after"`
		Threads []ChatThread `json:"threads"`
	}
	updatesJSON = struct {
		After   string       `json:"after"`
		Updates []ChatUpdate `json:"updates"`
	}
)

// ChatCursor answers where the updates of one of the organization's chats stand now: the after of a page of its
// threads (harness.Cobranch).
func (c *API) ChatCursor(ctx context.Context, org string, chat harness.Chat) (string, error) {
	r, err := c.threads(ctx, org, chat, ListChatThreadsParams{Limit: new(1)})
	if err != nil {
		return "", fmt.Errorf("reading the chat: %w", err)
	}
	if r.value == nil {
		return "", refused("reading the chat", r.status, r.body)
	}
	return r.value.After, nil
}

// threads reads a page of the chat's threads.
func (c *API) threads(ctx context.Context, org string, chat harness.Chat, p ListChatThreadsParams) (reply[threadsJSON], error) {
	switch {
	case chat.Conversation && chat.Agent != uuid.Nil:
		params := ListAgentConversationThreadsParams(p)
		r, err := c.api.ListAgentConversationThreadsWithResponse(ctx, org, chat.Agent, &params)
		if err != nil {
			return reply[threadsJSON]{}, err //nolint:wrapcheck // ChatCursor wraps it
		}
		return reply[threadsJSON]{r.JSON200, r.StatusCode(), r.Body}, nil
	case chat.Conversation && chat.Repo != "":
		params := ListRepositoryConversationThreadsParams(p)
		r, err := c.api.ListRepositoryConversationThreadsWithResponse(ctx, org, chat.Repo, &params)
		if err != nil {
			return reply[threadsJSON]{}, err //nolint:wrapcheck // ChatCursor wraps it
		}
		return reply[threadsJSON]{r.JSON200, r.StatusCode(), r.Body}, nil
	case chat.Conversation:
		params := ListOrganizationConversationThreadsParams(p)
		r, err := c.api.ListOrganizationConversationThreadsWithResponse(ctx, org, &params)
		if err != nil {
			return reply[threadsJSON]{}, err //nolint:wrapcheck // ChatCursor wraps it
		}
		return reply[threadsJSON]{r.JSON200, r.StatusCode(), r.Body}, nil
	case chat.Branch != "":
		r, err := c.api.ListChatThreadsWithResponse(ctx, org, chat.Repo, chat.Branch, &p)
		if err != nil {
			return reply[threadsJSON]{}, err //nolint:wrapcheck // ChatCursor wraps it
		}
		return reply[threadsJSON]{r.JSON200, r.StatusCode(), r.Body}, nil
	}
	params := ListIssueChatThreadsParams(p)
	r, err := c.api.ListIssueChatThreadsWithResponse(ctx, org, int(chat.Issue), &params)
	if err != nil {
		return reply[threadsJSON]{}, err //nolint:wrapcheck // ChatCursor wraps it
	}
	return reply[threadsJSON]{r.JSON200, r.StatusCode(), r.Body}, nil
}

// ChatUpdates waits up to chatWait seconds for updates of one of the organization's chats after the cursor, and
// answers the threads they changed, in order and each once, and the cursor after them (harness.Cobranch).
func (c *API) ChatUpdates(ctx context.Context, org string, chat harness.Chat, after string) ([]uuid.UUID, string, error) {
	r, err := c.updates(ctx, org, chat, ListChatUpdatesParams{After: after, Wait: new(chatWait)})
	if err != nil {
		return nil, "", fmt.Errorf("waiting for the chat's updates: %w", err)
	}
	if r.value == nil {
		return nil, "", refused("waiting for the chat's updates", r.status, r.body)
	}
	var changed []uuid.UUID
	for _, u := range r.value.Updates {
		if !slices.Contains(changed, u.Thread.Id) {
			changed = append(changed, u.Thread.Id)
		}
	}
	return changed, r.value.After, nil
}

// updates waits for the chat's updates.
func (c *API) updates(ctx context.Context, org string, chat harness.Chat, p ListChatUpdatesParams) (reply[updatesJSON], error) {
	switch {
	case chat.Conversation && chat.Agent != uuid.Nil:
		params := ListAgentConversationUpdatesParams(p)
		r, err := c.api.ListAgentConversationUpdatesWithResponse(ctx, org, chat.Agent, &params)
		if err != nil {
			return reply[updatesJSON]{}, err //nolint:wrapcheck // ChatUpdates wraps it
		}
		return reply[updatesJSON]{r.JSON200, r.StatusCode(), r.Body}, nil
	case chat.Conversation && chat.Repo != "":
		params := ListRepositoryConversationUpdatesParams(p)
		r, err := c.api.ListRepositoryConversationUpdatesWithResponse(ctx, org, chat.Repo, &params)
		if err != nil {
			return reply[updatesJSON]{}, err //nolint:wrapcheck // ChatUpdates wraps it
		}
		return reply[updatesJSON]{r.JSON200, r.StatusCode(), r.Body}, nil
	case chat.Conversation:
		params := ListOrganizationConversationUpdatesParams(p)
		r, err := c.api.ListOrganizationConversationUpdatesWithResponse(ctx, org, &params)
		if err != nil {
			return reply[updatesJSON]{}, err //nolint:wrapcheck // ChatUpdates wraps it
		}
		return reply[updatesJSON]{r.JSON200, r.StatusCode(), r.Body}, nil
	case chat.Branch != "":
		r, err := c.api.ListChatUpdatesWithResponse(ctx, org, chat.Repo, chat.Branch, &p)
		if err != nil {
			return reply[updatesJSON]{}, err //nolint:wrapcheck // ChatUpdates wraps it
		}
		return reply[updatesJSON]{r.JSON200, r.StatusCode(), r.Body}, nil
	}
	params := ListIssueChatUpdatesParams(p)
	r, err := c.api.ListIssueChatUpdatesWithResponse(ctx, org, int(chat.Issue), &params)
	if err != nil {
		return reply[updatesJSON]{}, err //nolint:wrapcheck // ChatUpdates wraps it
	}
	return reply[updatesJSON]{r.JSON200, r.StatusCode(), r.Body}, nil
}
