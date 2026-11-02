package graphql

import (
	"context"
	"strconv"
	"time"

	"github.com/google/uuid"

	"cobranch.dev/internal/services/chats"
	"cobranch.dev/internal/services/code"
	"cobranch.dev/internal/services/orgs"
)

// Chats is what the API needs of the chats' service.
type Chats interface {
	Branch(ctx context.Context, v chats.Viewer, repoID uuid.UUID, name string) (chats.Subject, error)
	Issue(ctx context.Context, v chats.Viewer, orgID uuid.UUID, number int) (chats.Subject, error)
	Threads(ctx context.Context, v chats.Viewer, subj chats.Subject, before uuid.UUID, limit int) (chats.Page, error)
	StartThread(ctx context.Context, v chats.Viewer, subj chats.Subject, in chats.ThreadInput) (chats.Thread, error)
	Post(ctx context.Context, v chats.Viewer, subj chats.Subject, threadID uuid.UUID, in chats.MessageInput) (chats.Message, error)
	Edit(ctx context.Context, v chats.Viewer, subj chats.Subject, threadID, id uuid.UUID, body string, version int64) (chats.Message, error)
	Delete(ctx context.Context, v chats.Viewer, subj chats.Subject, threadID, id uuid.UUID, version int64) error
	Resolve(ctx context.Context, v chats.Viewer, subj chats.Subject, id uuid.UUID, resolved bool, version int64) (chats.Thread, error)
	Follow(ctx context.Context, v chats.Viewer, subj chats.Subject, after int64) (<-chan chats.Update, error)
}

// updateKinds are the kinds of a chat's updates as the API shows them.
var updateKinds = map[chats.UpdateKind]ChatUpdateKind{
	chats.ThreadStarted: ChatUpdateKindThreadStarted, chats.MessagePosted: ChatUpdateKindMessagePosted,
	chats.MessageEdited: ChatUpdateKindMessageEdited, chats.MessageDeleted: ChatUpdateKindMessageDeleted,
	chats.ThreadResolved: ChatUpdateKindThreadResolved, chats.ThreadReopened: ChatUpdateKindThreadReopened,
}

func chatThread(t chats.Thread) *ChatThread {
	out := &ChatThread{ID: t.ID.String(), Outdated: t.Outdated, StartedBy: commitPrincipal(t.StartedBy), CreatedAt: t.CreatedAt.UTC(),
		ResolvedAt: utc(t.ResolvedAt), ResolvedBy: commitPrincipal(t.ResolvedBy), Version: int(t.Version)}
	if t.ReviewID != nil {
		id := t.ReviewID.String()
		out.ReviewID = &id
	}
	if a := t.Anchor; a != nil {
		out.Anchor = &ChatAnchor{Sha: a.SHA, Path: a.Path, StartLine: a.StartLine, EndLine: a.EndLine}
	}
	if t.Messages != nil {
		out.Messages = mapAll(t.Messages, chatMessage)
	}
	return out
}

func chatMessage(m chats.Message) *ChatMessage {
	return &ChatMessage{ID: m.ID.String(), ThreadID: m.ThreadID.String(), Body: m.Body, HTML: m.HTML, Author: commitPrincipal(m.Author),
		CreatedAt: m.CreatedAt.UTC(), EditedAt: utc(m.EditedAt), DeletedAt: utc(m.DeletedAt), DeletedBy: commitPrincipal(m.DeletedBy), Version: int(m.Version)}
}

// utc is *at in UTC, or nil for nil.
func utc(at *time.Time) *time.Time {
	if at == nil {
		return nil
	}
	in := at.UTC()
	return &in
}

func chatUpdate(u chats.Update) *ChatUpdate {
	out := &ChatUpdate{Cursor: strconv.FormatInt(u.Cursor, 10), Kind: updateKinds[u.Kind], Thread: chatThread(u.Thread)}
	if u.Message != nil {
		out.Message = chatMessage(*u.Message)
	}
	return out
}

// chatOf is the viewer and the chat of the branch of the repository that a mutation or a subscription names.
func (r *Resolver) chatOf(ctx context.Context, repositoryID, branch string) (chats.Viewer, chats.Subject, error) {
	v, repoID, err := codeRepo(ctx, repositoryID)
	if err != nil {
		return chats.Viewer{}, chats.Subject{}, err
	}
	subj, err := r.Chats.Branch(ctx, v, repoID, branch)
	if err != nil {
		return chats.Viewer{}, chats.Subject{}, fail(ctx, err, "branch", "finding the chat")
	}
	return v, subj, nil
}

// issueChatOf is the viewer and the chat of the organization's issue that a field, a mutation or a subscription
// names.
func (r *Resolver) issueChatOf(ctx context.Context, organizationID string, number int) (chats.Viewer, chats.Subject, error) {
	v, orgID, err := issueOrg(ctx, organizationID)
	if err != nil {
		return chats.Viewer{}, chats.Subject{}, err
	}
	subj, err := r.Chats.Issue(ctx, v, orgID, number)
	if err != nil {
		return chats.Viewer{}, chats.Subject{}, fail(ctx, err, "issue", "finding the chat")
	}
	return v, subj, nil
}

// chatPage reads a page of the subject's chat, of at most first threads, from the one before after names.
func (r *Resolver) chatPage(ctx context.Context, v chats.Viewer, subj chats.Subject, first int, after *string) (*ChatPage, error) {
	before := uuid.Nil
	if after != nil && *after != "" {
		var err error
		if before, err = uuid.Parse(*after); err != nil {
			return nil, fail(ctx, &orgs.ValidationError{Field: "after", Code: "cursor.invalid", Message: "after names no page of this chat"}, "", "")
		}
	}
	page, err := r.Chats.Threads(ctx, v, subj, before, first)
	if err != nil {
		return nil, fail(ctx, err, subjectKind(subj), "reading the chat")
	}
	out := &ChatPage{Threads: mapAll(page.Threads, chatThread), After: strconv.FormatInt(page.After, 10)}
	if page.Next != uuid.Nil {
		next := page.Next.String()
		out.NextCursor = &next
	}
	return out, nil
}

// subjectKind is what the subject is, as a refusal names what isn't there.
func subjectKind(subj chats.Subject) string {
	if subj.OfIssue() {
		return "issue"
	}
	return "branch"
}

// startThread starts a thread on the subject's chat for the viewer.
func (r *Resolver) startThread(ctx context.Context, v chats.Viewer, subj chats.Subject, input ChatThreadInput) (*ChatThread, error) {
	in, err := chatInput(ctx, input)
	if err != nil {
		return nil, err
	}
	t, err := r.Chats.StartThread(ctx, v, subj, in)
	if err != nil {
		return nil, fail(ctx, err, "thread", "starting the thread")
	}
	return chatThread(t), nil
}

// postMessage posts a message to one of the subject's chat threads for the viewer.
func (r *Resolver) postMessage(ctx context.Context, v chats.Viewer, subj chats.Subject, thread string, input ChatMessageInput) (*ChatMessage, error) {
	threadID, err := parseID(ctx, thread, "thread")
	if err != nil {
		return nil, err
	}
	in := chats.MessageInput{Body: input.Body}
	if input.ID != nil {
		if in.ID, err = pickedID(ctx, *input.ID); err != nil {
			return nil, err
		}
	}
	m, err := r.Chats.Post(ctx, v, subj, threadID, in)
	if err != nil {
		return nil, fail(ctx, err, "thread", "posting the message")
	}
	return chatMessage(m), nil
}

// editMessage changes the body of one of the viewer's messages in the subject's chat.
func (r *Resolver) editMessage(ctx context.Context, v chats.Viewer, subj chats.Subject, thread, message, body string, expectedVersion *int) (*ChatMessage, error) {
	threadID, messageID, version, err := chatTarget(ctx, thread, message, expectedVersion)
	if err != nil {
		return nil, err
	}
	m, err := r.Chats.Edit(ctx, v, subj, threadID, messageID, body, version)
	if err != nil {
		return nil, fail(ctx, err, "message", "editing the message")
	}
	return chatMessage(m), nil
}

// deleteMessage deletes a message of the subject's chat, keeping its place in its thread.
func (r *Resolver) deleteMessage(ctx context.Context, v chats.Viewer, subj chats.Subject, thread, message string, expectedVersion *int) (bool, error) {
	threadID, messageID, version, err := chatTarget(ctx, thread, message, expectedVersion)
	if err != nil {
		return false, err
	}
	if err := r.Chats.Delete(ctx, v, subj, threadID, messageID, version); err != nil {
		return false, fail(ctx, err, "message", "deleting the message")
	}
	return true, nil
}

// resolveThread resolves one of the subject's chat threads, or opens it again.
func (r *Resolver) resolveThread(ctx context.Context, v chats.Viewer, subj chats.Subject, thread string, resolved bool, expectedVersion *int) (*ChatThread, error) {
	threadID, err := parseID(ctx, thread, "thread")
	if err != nil {
		return nil, err
	}
	version, err := chatVersion(ctx, expectedVersion)
	if err != nil {
		return nil, err
	}
	t, err := r.Chats.Resolve(ctx, v, subj, threadID, resolved, version)
	if err != nil {
		return nil, fail(ctx, err, "thread", "resolving the thread")
	}
	return chatThread(t), nil
}

// followChat sends the subject's chat's updates after cursor after as they come.
func (r *Resolver) followChat(ctx context.Context, v chats.Viewer, subj chats.Subject, after string) (<-chan *ChatUpdate, error) {
	from, err := cursor(ctx, after)
	if err != nil {
		return nil, err
	}
	updates, err := r.Chats.Follow(ctx, v, subj, from)
	if err != nil {
		return nil, fail(ctx, err, subjectKind(subj), "following the chat")
	}
	return follow(ctx, updates), nil
}

// chatInput is in as the chats' service takes it.
func chatInput(ctx context.Context, in ChatThreadInput) (chats.ThreadInput, error) {
	out := chats.ThreadInput{Body: in.Body}
	if in.ID != nil {
		id, err := pickedID(ctx, *in.ID)
		if err != nil {
			return chats.ThreadInput{}, err
		}
		out.ID = id
	}
	if a := in.Anchor; a != nil {
		out.Anchor = anchorOf(a)
	}
	return out, nil
}

// anchorOf is a as the services take it.
func anchorOf(a *ChatAnchorInput) *code.Anchor {
	return &code.Anchor{SHA: a.Sha, Path: a.Path, StartLine: a.StartLine, EndLine: a.EndLine}
}

// pickedID is an ID a poster picked, or a BAD_USER_INPUT error for one that is no UUID.
func pickedID(ctx context.Context, id string) (uuid.UUID, error) {
	parsed, err := uuid.Parse(id)
	if err != nil {
		return uuid.Nil, fail(ctx, &orgs.ValidationError{Field: "id", Code: "id.invalid", Message: "an ID a poster picks is a UUIDv7"}, "", "")
	}
	return parsed, nil
}

// chatVersion is the version a change names as expectedVersion: chats.AnyVersion when it names none.
func chatVersion(ctx context.Context, expectedVersion *int) (int64, error) {
	if expectedVersion == nil {
		return chats.AnyVersion, nil
	}
	if *expectedVersion < 1 {
		return 0, fail(ctx, &orgs.ValidationError{Field: "expectedVersion", Code: "version.invalid", Message: "a version, from 1"}, "", "")
	}
	return int64(*expectedVersion), nil
}

// chatTarget is the thread, the message and the version a change of a message names.
func chatTarget(ctx context.Context, thread, message string, expectedVersion *int) (uuid.UUID, uuid.UUID, int64, error) {
	threadID, err := parseID(ctx, thread, "thread")
	if err != nil {
		return uuid.Nil, uuid.Nil, 0, err
	}
	messageID, err := parseID(ctx, message, "message")
	if err != nil {
		return uuid.Nil, uuid.Nil, 0, err
	}
	version, err := chatVersion(ctx, expectedVersion)
	return threadID, messageID, version, err
}

// cursor is a chat's cursor as after names it, or a BAD_USER_INPUT error for one that names none.
func cursor(ctx context.Context, after string) (int64, error) {
	c, err := strconv.ParseInt(after, 10, 64)
	if err != nil || c < 0 {
		return 0, fail(ctx, &orgs.ValidationError{Field: "after", Code: "cursor.invalid", Message: "after names no place among this chat's updates"}, "", "")
	}
	return c, nil
}

// follow sends the updates as the API shows them until updates closes or ctx ends, then closes the channel it returns.
func follow(ctx context.Context, updates <-chan chats.Update) <-chan *ChatUpdate {
	out := make(chan *ChatUpdate)
	go func() {
		defer close(out)
		for u := range updates {
			select {
			case out <- chatUpdate(u):
			case <-ctx.Done():
				return
			}
		}
	}()
	return out
}
