package rest

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"cobranch.dev/internal/services/chats"
)

// Chats is what the API needs of the chats' service.
type Chats interface {
	Branch(ctx context.Context, v chats.Viewer, repoID uuid.UUID, name string) (chats.Subject, error)
	Threads(ctx context.Context, v chats.Viewer, subj chats.Subject, before uuid.UUID, limit int) (chats.Page, error)
	Thread(ctx context.Context, v chats.Viewer, subj chats.Subject, id uuid.UUID) (chats.Thread, error)
	Message(ctx context.Context, v chats.Viewer, subj chats.Subject, threadID, id uuid.UUID) (chats.Message, error)
	StartThread(ctx context.Context, v chats.Viewer, subj chats.Subject, in chats.ThreadInput) (chats.Thread, error)
	Post(ctx context.Context, v chats.Viewer, subj chats.Subject, threadID uuid.UUID, in chats.MessageInput) (chats.Message, error)
	Edit(ctx context.Context, v chats.Viewer, subj chats.Subject, threadID, id uuid.UUID, body string, version int64) (chats.Message, error)
	Delete(ctx context.Context, v chats.Viewer, subj chats.Subject, threadID, id uuid.UUID, version int64) error
	Resolve(ctx context.Context, v chats.Viewer, subj chats.Subject, id uuid.UUID, resolved bool, version int64) (chats.Thread, error)
	Updates(ctx context.Context, v chats.Viewer, subj chats.Subject, after int64, wait time.Duration) ([]chats.Update, error)
}

// defaultChatPage is how many threads a page of a chat holds unless the request names a limit.
const defaultChatPage = 30

// maxWait is the longest a read of a chat's updates waits for one: shorter than a server's shutdown, so a server that
// stops lets every wait end first.
const maxWait = 15 * time.Second

func chatThread(t chats.Thread) ChatThread {
	out := ChatThread{Id: t.ID, Outdated: t.Outdated, StartedBy: principal(t.StartedBy), CreatedAt: t.CreatedAt.UTC(),
		ResolvedBy: principal(t.ResolvedBy), Version: t.Version}
	if a := t.Anchor; a != nil {
		out.Anchor = &ChatAnchor{Sha: a.SHA, Path: a.Path, StartLine: a.StartLine, EndLine: a.EndLine}
	}
	if t.ResolvedAt != nil {
		at := t.ResolvedAt.UTC()
		out.ResolvedAt = &at
	}
	if t.Messages != nil {
		messages := mapAll(t.Messages, chatMessage)
		out.Messages = &messages
	}
	return out
}

func chatMessage(m chats.Message) ChatMessage {
	out := ChatMessage{Id: m.ID, ThreadId: m.ThreadID, Body: m.Body, Html: m.HTML, Author: principal(m.Author), CreatedAt: m.CreatedAt.UTC(),
		DeletedBy: principal(m.DeletedBy), Version: m.Version}
	if m.EditedAt != nil {
		at := m.EditedAt.UTC()
		out.EditedAt = &at
	}
	if m.DeletedAt != nil {
		at := m.DeletedAt.UTC()
		out.DeletedAt = &at
	}
	return out
}

func chatUpdate(u chats.Update) ChatUpdate {
	out := ChatUpdate{Cursor: strconv.FormatInt(u.Cursor, 10), Kind: ChatUpdateKind(u.Kind), Thread: chatThread(u.Thread)}
	if u.Message != nil {
		m := chatMessage(*u.Message)
		out.Message = &m
	}
	return out
}

// versionTag is the ETag of a thread or a message at version: the number, quoted.
func versionTag(version int64) string {
	return `"` + strconv.FormatInt(version, 10) + `"`
}

// chatPath is the path of the subject's chat from the API's base, by the current slugs, with the parts that follow.
func chatPath(subj chats.Subject, parts ...string) string {
	p := BaseURL + "/orgs/" + url.PathEscape(subj.Org) + "/repos/" + url.PathEscape(subj.Repo) + "/branches/" + url.PathEscape(subj.Branch) + "/chat"
	for _, part := range parts {
		p += "/" + url.PathEscape(part)
	}
	return p
}

// chat finds the chat of the branch the path names, for a viewer with repo.read on its repository.
func (a api) chat(ctx context.Context, orgSlug, slug, branch string) (chats.Viewer, chats.Subject, error) {
	v, err := viewer(ctx)
	if err != nil {
		return chats.Viewer{}, chats.Subject{}, err
	}
	repo, err := a.codeRepo(ctx, v, orgSlug, slug)
	if err != nil {
		return chats.Viewer{}, chats.Subject{}, err
	}
	subj, err := a.chats.Branch(ctx, v, repo.ID, branch)
	if err != nil {
		return chats.Viewer{}, chats.Subject{}, fail(err, "branch")
	}
	return v, subj, nil
}

// ListChatThreads lists a page of a branch's chat, and links the next.
func (a api) ListChatThreads(ctx context.Context, req ListChatThreadsRequestObject) (ListChatThreadsResponseObject, error) {
	v, subj, err := a.chat(ctx, req.Org, req.Repo, req.BranchName)
	if err != nil {
		return nil, err
	}
	before, limit := uuid.Nil, defaultChatPage
	if c := req.Params.Cursor; c != nil && *c != "" {
		if before, err = uuid.Parse(*c); err != nil {
			return nil, refuse(http.StatusBadRequest, "cursor.invalid", "The cursor names no page of this chat.")
		}
	}
	if req.Params.Limit != nil {
		limit = *req.Params.Limit
	}
	page, err := a.chats.Threads(ctx, v, subj, before, limit)
	if err != nil {
		return nil, fail(err, "branch")
	}
	out := ListChatThreads200JSONResponse{}
	out.Body.Threads, out.Body.After = mapAll(page.Threads, chatThread), strconv.FormatInt(page.After, 10)
	if page.Next != uuid.Nil {
		next := url.Values{"cursor": {page.Next.String()}}
		if req.Params.Limit != nil {
			next.Set("limit", strconv.Itoa(limit))
		}
		link := `<?` + next.Encode() + `>; rel="next"`
		out.Headers.Link = &link
	}
	return out, nil
}

// StartChatThread starts a thread on a branch's chat for the viewer.
func (a api) StartChatThread(ctx context.Context, req StartChatThreadRequestObject) (StartChatThreadResponseObject, error) {
	v, subj, err := a.chat(ctx, req.Org, req.Repo, req.BranchName)
	if err != nil {
		return nil, err
	}
	in := chats.ThreadInput{Body: req.Body.Body}
	if req.Body.Id != nil {
		in.ID = *req.Body.Id
	}
	if an := req.Body.Anchor; an != nil {
		in.Anchor = &chats.Anchor{SHA: an.Sha, Path: an.Path, StartLine: an.StartLine, EndLine: an.EndLine}
	}
	t, err := a.chats.StartThread(ctx, v, subj, in)
	if err != nil {
		return nil, fail(err, "thread")
	}
	return StartChatThread201JSONResponse{Body: chatThread(t), Headers: StartChatThread201ResponseHeaders{ETag: versionTag(t.Version),
		Location: chatPath(subj, "threads", t.ID.String())}}, nil
}

// GetChatThread answers one of a branch's chat threads with its messages.
func (a api) GetChatThread(ctx context.Context, req GetChatThreadRequestObject) (GetChatThreadResponseObject, error) {
	v, subj, err := a.chat(ctx, req.Org, req.Repo, req.BranchName)
	if err != nil {
		return nil, err
	}
	t, err := a.chats.Thread(ctx, v, subj, req.ThreadID)
	if err != nil {
		return nil, fail(err, "thread")
	}
	tag := versionTag(t.Version)
	if matches(req.Params.IfNoneMatch, tag) {
		return GetChatThread304Response{Headers: GetChatThread304ResponseHeaders{ETag: tag}}, nil
	}
	return GetChatThread200JSONResponse{Body: chatThread(t), Headers: GetChatThread200ResponseHeaders{ETag: tag}}, nil
}

// UpdateChatThread resolves one of a branch's chat threads, or opens it again.
func (a api) UpdateChatThread(ctx context.Context, req UpdateChatThreadRequestObject) (UpdateChatThreadResponseObject, error) {
	v, subj, err := a.chat(ctx, req.Org, req.Repo, req.BranchName)
	if err != nil {
		return nil, err
	}
	match, err := chatMatch(req.Params.IfMatch)
	if err != nil {
		return nil, err
	}
	t, err := a.chats.Resolve(ctx, v, subj, req.ThreadID, req.Body.Resolved, match)
	if err != nil {
		return nil, fail(err, "thread")
	}
	return UpdateChatThread200JSONResponse{Body: chatThread(t), Headers: UpdateChatThread200ResponseHeaders{ETag: versionTag(t.Version)}}, nil
}

// PostChatMessage posts a message to one of a branch's chat threads for the viewer.
func (a api) PostChatMessage(ctx context.Context, req PostChatMessageRequestObject) (PostChatMessageResponseObject, error) {
	v, subj, err := a.chat(ctx, req.Org, req.Repo, req.BranchName)
	if err != nil {
		return nil, err
	}
	in := chats.MessageInput{Body: req.Body.Body}
	if req.Body.Id != nil {
		in.ID = *req.Body.Id
	}
	m, err := a.chats.Post(ctx, v, subj, req.ThreadID, in)
	if err != nil {
		return nil, fail(err, "thread")
	}
	return PostChatMessage201JSONResponse{Body: chatMessage(m), Headers: PostChatMessage201ResponseHeaders{ETag: versionTag(m.Version),
		Location: chatPath(subj, "threads", req.ThreadID.String(), "messages", m.ID.String())}}, nil
}

// GetChatMessage answers one of the messages of one of a branch's chat threads.
func (a api) GetChatMessage(ctx context.Context, req GetChatMessageRequestObject) (GetChatMessageResponseObject, error) {
	v, subj, err := a.chat(ctx, req.Org, req.Repo, req.BranchName)
	if err != nil {
		return nil, err
	}
	m, err := a.chats.Message(ctx, v, subj, req.ThreadID, req.MessageID)
	if err != nil {
		return nil, fail(err, "message")
	}
	tag := versionTag(m.Version)
	if matches(req.Params.IfNoneMatch, tag) {
		return GetChatMessage304Response{Headers: GetChatMessage304ResponseHeaders{ETag: tag}}, nil
	}
	return GetChatMessage200JSONResponse{Body: chatMessage(m), Headers: GetChatMessage200ResponseHeaders{ETag: tag}}, nil
}

// EditChatMessage changes the body of one of the viewer's messages.
func (a api) EditChatMessage(ctx context.Context, req EditChatMessageRequestObject) (EditChatMessageResponseObject, error) {
	v, subj, err := a.chat(ctx, req.Org, req.Repo, req.BranchName)
	if err != nil {
		return nil, err
	}
	match, err := chatMatch(req.Params.IfMatch)
	if err != nil {
		return nil, err
	}
	m, err := a.chats.Edit(ctx, v, subj, req.ThreadID, req.MessageID, req.Body.Body, match)
	if err != nil {
		return nil, fail(err, "message")
	}
	return EditChatMessage200JSONResponse{Body: chatMessage(m), Headers: EditChatMessage200ResponseHeaders{ETag: versionTag(m.Version)}}, nil
}

// DeleteChatMessage deletes a message, keeping its place in its thread.
func (a api) DeleteChatMessage(ctx context.Context, req DeleteChatMessageRequestObject) (DeleteChatMessageResponseObject, error) {
	v, subj, err := a.chat(ctx, req.Org, req.Repo, req.BranchName)
	if err != nil {
		return nil, err
	}
	match, err := chatMatch(req.Params.IfMatch)
	if err != nil {
		return nil, err
	}
	if err := a.chats.Delete(ctx, v, subj, req.ThreadID, req.MessageID, match); err != nil {
		return nil, fail(err, "message")
	}
	return DeleteChatMessage204Response{}, nil
}

// ListChatUpdates lists the changes to a branch's chat after a cursor, waiting for one when there is none yet.
func (a api) ListChatUpdates(ctx context.Context, req ListChatUpdatesRequestObject) (ListChatUpdatesResponseObject, error) {
	v, subj, err := a.chat(ctx, req.Org, req.Repo, req.BranchName)
	if err != nil {
		return nil, err
	}
	after, err := strconv.ParseInt(req.Params.After, 10, 64)
	if err != nil || after < 0 {
		return nil, refuse(http.StatusBadRequest, "cursor.invalid", "after names no place among this chat's updates.")
	}
	wait := time.Duration(0)
	if w := req.Params.Wait; w != nil {
		if *w < 0 || time.Duration(*w)*time.Second > maxWait {
			return nil, refuse(http.StatusBadRequest, "wait.invalid", "A read waits 0 to 15 seconds for an update.")
		}
		wait = time.Duration(*w) * time.Second
	}
	list, err := a.chats.Updates(ctx, v, subj, after, wait)
	if err != nil {
		return nil, fail(err, "branch")
	}
	out := ListChatUpdates200JSONResponse{Updates: mapAll(list, chatUpdate), After: req.Params.After}
	if len(list) > 0 {
		out.After = strconv.FormatInt(list[len(list)-1].Cursor, 10)
	}
	return out, nil
}

// chatMatch is the version of a thread or a message that If-Match names, chats.AnyVersion for *: a refusal with 428
// without the header, and with 412 for a tag that names no version, a weak one included, since If-Match compares
// strongly.
func chatMatch(header *string) (int64, error) {
	if header == nil || strings.TrimSpace(*header) == "" {
		return 0, refuse(http.StatusPreconditionRequired, "precondition.required", "Name its ETag in If-Match, or * for whichever version is there.")
	}
	tag := strings.TrimSpace(*header)
	if tag == "*" {
		return chats.AnyVersion, nil
	}
	version, err := strconv.ParseInt(strings.TrimSuffix(strings.TrimPrefix(tag, `"`), `"`), 10, 64)
	if err != nil || version < 1 {
		return 0, refuse(http.StatusPreconditionFailed, "precondition.failed", "If-Match names no version of it.")
	}
	return version, nil
}
