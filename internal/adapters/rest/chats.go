package rest

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
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
	Issue(ctx context.Context, v chats.Viewer, orgID uuid.UUID, number int) (chats.Subject, error)
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
		ResolvedBy: principal(t.ResolvedBy), Version: t.Version, ReviewId: t.ReviewID}
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
	if m.Task != nil {
		out.TaskId, out.TaskNumber = &m.Task.ID, &m.Task.Number
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

// versionTag is the ETag of a thread, a message, an agent or a task at version as body shows it, quoted: the version,
// then a digest of body. More than its own changes shape how it reads (a branch's head, the slugs its links name, the
// names of its people and roles), so the digest changes with any of them, and the version leads for If-Match.
func versionTag(version int64, body any) (string, error) {
	shown, err := json.Marshal(body)
	if err != nil {
		return "", fmt.Errorf("digesting version %d: %w", version, err)
	}
	sum := sha256.Sum256(shown)
	return `"` + strconv.FormatInt(version, 10) + "-" + hex.EncodeToString(sum[:12]) + `"`, nil
}

// taggedThread is t as the API shows it, with its ETag.
func taggedThread(t chats.Thread) (ChatThread, string, error) {
	body := chatThread(t)
	tag, err := versionTag(t.Version, body)
	return body, tag, err
}

// taggedMessage is m as the API shows it, with its ETag.
func taggedMessage(m chats.Message) (ChatMessage, string, error) {
	body := chatMessage(m)
	tag, err := versionTag(m.Version, body)
	return body, tag, err
}

// chatPath is the path of the subject's chat from the API's base, by the current slugs, with the parts that follow.
func chatPath(subj chats.Subject, parts ...string) string {
	p := BaseURL + "/orgs/" + url.PathEscape(subj.Org) + "/repos/" + url.PathEscape(subj.Repo) + "/branches/" + url.PathEscape(subj.Branch) + "/chat"
	if subj.OfIssue() {
		p = BaseURL + "/orgs/" + url.PathEscape(subj.Org) + "/issues/" + strconv.Itoa(subj.Number) + "/chat"
	}
	for _, part := range parts {
		p += "/" + url.PathEscape(part)
	}
	return p
}

// subjectKind is what the subject is, as a refusal names what isn't there.
func subjectKind(subj chats.Subject) string {
	if subj.OfIssue() {
		return "issue"
	}
	return "branch"
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

// chatPage lists a page of the subject's chat from the one the cursor names, of at most limit threads, and links the
// next, relative to this one.
func (a api) chatPage(ctx context.Context, v chats.Viewer, subj chats.Subject, cursor *string, limit *int) ([]ChatThread, string, *string, error) {
	before, size := uuid.Nil, defaultChatPage
	if cursor != nil && *cursor != "" {
		var err error
		if before, err = uuid.Parse(*cursor); err != nil {
			return nil, "", nil, refuse(http.StatusBadRequest, "cursor.invalid", "The cursor names no page of this chat.")
		}
	}
	if limit != nil {
		size = *limit
	}
	page, err := a.chats.Threads(ctx, v, subj, before, size)
	if err != nil {
		return nil, "", nil, fail(err, subjectKind(subj))
	}
	var link *string
	if page.Next != uuid.Nil {
		next := url.Values{"cursor": {page.Next.String()}}
		if limit != nil {
			next.Set("limit", strconv.Itoa(size))
		}
		header := `<?` + next.Encode() + `>; rel="next"`
		link = &header
	}
	return mapAll(page.Threads, chatThread), strconv.FormatInt(page.After, 10), link, nil
}

// startThread starts a thread on the subject's chat for the viewer, and answers it with its ETag and its location.
func (a api) startThread(ctx context.Context, v chats.Viewer, subj chats.Subject, req ChatThreadInput) (ChatThread, string, string, error) {
	in := chats.ThreadInput{Body: req.Body}
	if req.Id != nil {
		in.ID = *req.Id
	}
	if an := req.Anchor; an != nil {
		in.Anchor = &chats.Anchor{SHA: an.Sha, Path: an.Path, StartLine: an.StartLine, EndLine: an.EndLine}
	}
	t, err := a.chats.StartThread(ctx, v, subj, in)
	if err != nil {
		return ChatThread{}, "", "", fail(err, "thread")
	}
	body, tag, err := taggedThread(t)
	return body, tag, chatPath(subj, "threads", t.ID.String()), err
}

// thread answers one of the subject's chat threads with its ETag.
func (a api) thread(ctx context.Context, v chats.Viewer, subj chats.Subject, id uuid.UUID) (ChatThread, string, error) {
	t, err := a.chats.Thread(ctx, v, subj, id)
	if err != nil {
		return ChatThread{}, "", fail(err, "thread")
	}
	return taggedThread(t)
}

// resolveThread resolves one of the subject's chat threads, or opens it again, from the version If-Match names.
func (a api) resolveThread(ctx context.Context, v chats.Viewer, subj chats.Subject, id uuid.UUID, resolved bool, ifMatch *string) (ChatThread, string, error) {
	match, err := versionMatch(ifMatch, chats.AnyVersion)
	if err != nil {
		return ChatThread{}, "", err
	}
	t, err := a.chats.Resolve(ctx, v, subj, id, resolved, match)
	if err != nil {
		return ChatThread{}, "", fail(err, "thread")
	}
	return taggedThread(t)
}

// postMessage posts a message to one of the subject's chat threads for the viewer, and answers it with its ETag and
// its location.
func (a api) postMessage(ctx context.Context, v chats.Viewer, subj chats.Subject, threadID uuid.UUID, req ChatMessageInput) (ChatMessage, string, string, error) {
	in := chats.MessageInput{Body: req.Body}
	if req.Id != nil {
		in.ID = *req.Id
	}
	m, err := a.chats.Post(ctx, v, subj, threadID, in)
	if err != nil {
		return ChatMessage{}, "", "", fail(err, "thread")
	}
	body, tag, err := taggedMessage(m)
	return body, tag, chatPath(subj, "threads", threadID.String(), "messages", m.ID.String()), err
}

// message answers one of the messages of one of the subject's chat threads with its ETag.
func (a api) message(ctx context.Context, v chats.Viewer, subj chats.Subject, threadID, id uuid.UUID) (ChatMessage, string, error) {
	m, err := a.chats.Message(ctx, v, subj, threadID, id)
	if err != nil {
		return ChatMessage{}, "", fail(err, "message")
	}
	return taggedMessage(m)
}

// editMessage changes the body of one of the viewer's messages from the version If-Match names.
func (a api) editMessage(ctx context.Context, v chats.Viewer, subj chats.Subject, threadID, id uuid.UUID, text string, ifMatch *string) (ChatMessage, string, error) {
	match, err := versionMatch(ifMatch, chats.AnyVersion)
	if err != nil {
		return ChatMessage{}, "", err
	}
	m, err := a.chats.Edit(ctx, v, subj, threadID, id, text, match)
	if err != nil {
		return ChatMessage{}, "", fail(err, "message")
	}
	return taggedMessage(m)
}

// deleteMessage deletes a message from the version If-Match names, keeping its place in its thread.
func (a api) deleteMessage(ctx context.Context, v chats.Viewer, subj chats.Subject, threadID, id uuid.UUID, ifMatch *string) error {
	match, err := versionMatch(ifMatch, chats.AnyVersion)
	if err != nil {
		return err
	}
	if err := a.chats.Delete(ctx, v, subj, threadID, id, match); err != nil {
		return fail(err, "message")
	}
	return nil
}

// chatUpdates lists the changes to the subject's chat after cursor after, waiting up to wait seconds for one when there
// is none yet, and where the next read starts.
func (a api) chatUpdates(ctx context.Context, v chats.Viewer, subj chats.Subject, after string, wait *int) ([]ChatUpdate, string, error) {
	from, err := strconv.ParseInt(after, 10, 64)
	if err != nil || from < 0 {
		return nil, "", refuse(http.StatusBadRequest, "cursor.invalid", "after names no place among this chat's updates.")
	}
	waits := time.Duration(0)
	if wait != nil {
		if *wait < 0 || time.Duration(*wait)*time.Second > maxWait {
			return nil, "", refuse(http.StatusBadRequest, "wait.invalid", "A read waits 0 to 15 seconds for an update.")
		}
		waits = time.Duration(*wait) * time.Second
	}
	list, err := a.chats.Updates(ctx, v, subj, from, waits)
	if err != nil {
		return nil, "", fail(err, subjectKind(subj))
	}
	if len(list) > 0 {
		after = strconv.FormatInt(list[len(list)-1].Cursor, 10)
	}
	return mapAll(list, chatUpdate), after, nil
}

// ListChatThreads lists a page of a branch's chat, and links the next.
func (a api) ListChatThreads(ctx context.Context, req ListChatThreadsRequestObject) (ListChatThreadsResponseObject, error) {
	v, subj, err := a.chat(ctx, req.Org, req.Repo, req.BranchName)
	if err != nil {
		return nil, err
	}
	out := ListChatThreads200JSONResponse{}
	out.Body.Threads, out.Body.After, out.Headers.Link, err = a.chatPage(ctx, v, subj, req.Params.Cursor, req.Params.Limit)
	if err != nil {
		return nil, err
	}
	return out, nil
}

// StartChatThread starts a thread on a branch's chat for the viewer.
func (a api) StartChatThread(ctx context.Context, req StartChatThreadRequestObject) (StartChatThreadResponseObject, error) {
	v, subj, err := a.chat(ctx, req.Org, req.Repo, req.BranchName)
	if err != nil {
		return nil, err
	}
	body, tag, location, err := a.startThread(ctx, v, subj, *req.Body)
	if err != nil {
		return nil, err
	}
	return StartChatThread201JSONResponse{Body: body, Headers: StartChatThread201ResponseHeaders{ETag: tag, Location: location}}, nil
}

// GetChatThread answers one of a branch's chat threads with its messages.
func (a api) GetChatThread(ctx context.Context, req GetChatThreadRequestObject) (GetChatThreadResponseObject, error) {
	v, subj, err := a.chat(ctx, req.Org, req.Repo, req.BranchName)
	if err != nil {
		return nil, err
	}
	body, tag, err := a.thread(ctx, v, subj, req.ThreadID)
	if err != nil {
		return nil, err
	}
	if matches(req.Params.IfNoneMatch, tag) {
		return GetChatThread304Response{Headers: GetChatThread304ResponseHeaders{ETag: tag}}, nil
	}
	return GetChatThread200JSONResponse{Body: body, Headers: GetChatThread200ResponseHeaders{ETag: tag}}, nil
}

// UpdateChatThread resolves one of a branch's chat threads, or opens it again.
func (a api) UpdateChatThread(ctx context.Context, req UpdateChatThreadRequestObject) (UpdateChatThreadResponseObject, error) {
	v, subj, err := a.chat(ctx, req.Org, req.Repo, req.BranchName)
	if err != nil {
		return nil, err
	}
	body, tag, err := a.resolveThread(ctx, v, subj, req.ThreadID, req.Body.Resolved, req.Params.IfMatch)
	if err != nil {
		return nil, err
	}
	return UpdateChatThread200JSONResponse{Body: body, Headers: UpdateChatThread200ResponseHeaders{ETag: tag}}, nil
}

// PostChatMessage posts a message to one of a branch's chat threads for the viewer.
func (a api) PostChatMessage(ctx context.Context, req PostChatMessageRequestObject) (PostChatMessageResponseObject, error) {
	v, subj, err := a.chat(ctx, req.Org, req.Repo, req.BranchName)
	if err != nil {
		return nil, err
	}
	body, tag, location, err := a.postMessage(ctx, v, subj, req.ThreadID, *req.Body)
	if err != nil {
		return nil, err
	}
	return PostChatMessage201JSONResponse{Body: body, Headers: PostChatMessage201ResponseHeaders{ETag: tag, Location: location}}, nil
}

// GetChatMessage answers one of the messages of one of a branch's chat threads.
func (a api) GetChatMessage(ctx context.Context, req GetChatMessageRequestObject) (GetChatMessageResponseObject, error) {
	v, subj, err := a.chat(ctx, req.Org, req.Repo, req.BranchName)
	if err != nil {
		return nil, err
	}
	body, tag, err := a.message(ctx, v, subj, req.ThreadID, req.MessageID)
	if err != nil {
		return nil, err
	}
	if matches(req.Params.IfNoneMatch, tag) {
		return GetChatMessage304Response{Headers: GetChatMessage304ResponseHeaders{ETag: tag}}, nil
	}
	return GetChatMessage200JSONResponse{Body: body, Headers: GetChatMessage200ResponseHeaders{ETag: tag}}, nil
}

// EditChatMessage changes the body of one of the viewer's messages.
func (a api) EditChatMessage(ctx context.Context, req EditChatMessageRequestObject) (EditChatMessageResponseObject, error) {
	v, subj, err := a.chat(ctx, req.Org, req.Repo, req.BranchName)
	if err != nil {
		return nil, err
	}
	body, tag, err := a.editMessage(ctx, v, subj, req.ThreadID, req.MessageID, req.Body.Body, req.Params.IfMatch)
	if err != nil {
		return nil, err
	}
	return EditChatMessage200JSONResponse{Body: body, Headers: EditChatMessage200ResponseHeaders{ETag: tag}}, nil
}

// DeleteChatMessage deletes a message, keeping its place in its thread.
func (a api) DeleteChatMessage(ctx context.Context, req DeleteChatMessageRequestObject) (DeleteChatMessageResponseObject, error) {
	v, subj, err := a.chat(ctx, req.Org, req.Repo, req.BranchName)
	if err != nil {
		return nil, err
	}
	if err := a.deleteMessage(ctx, v, subj, req.ThreadID, req.MessageID, req.Params.IfMatch); err != nil {
		return nil, err
	}
	return DeleteChatMessage204Response{}, nil
}

// ListChatUpdates lists the changes to a branch's chat after a cursor, waiting for one when there is none yet.
func (a api) ListChatUpdates(ctx context.Context, req ListChatUpdatesRequestObject) (ListChatUpdatesResponseObject, error) {
	v, subj, err := a.chat(ctx, req.Org, req.Repo, req.BranchName)
	if err != nil {
		return nil, err
	}
	updates, after, err := a.chatUpdates(ctx, v, subj, req.Params.After, req.Params.Wait)
	if err != nil {
		return nil, err
	}
	return ListChatUpdates200JSONResponse{Updates: updates, After: after}, nil
}

// versionMatch is the version of a thread, a message, an agent or a task that If-Match names, anyVersion for *: a
// refusal with 428 without the header, and with 412 for a tag that names no version, a weak one included, since
// If-Match compares strongly. It compares the version alone, which its ETag leads with: more than its own changes
// shape how it reads, and a change made from before one of those still applies.
func versionMatch(header *string, anyVersion int64) (int64, error) {
	if header == nil || strings.TrimSpace(*header) == "" {
		return 0, refuse(http.StatusPreconditionRequired, "precondition.required", "Name its ETag in If-Match, or * for whichever version is there.")
	}
	tag := strings.TrimSpace(*header)
	if tag == "*" {
		return anyVersion, nil
	}
	number, _, _ := strings.Cut(strings.TrimSuffix(strings.TrimPrefix(tag, `"`), `"`), "-")
	version, err := strconv.ParseInt(number, 10, 64)
	if err != nil || version < 1 {
		return 0, refuse(http.StatusPreconditionFailed, "precondition.failed", "If-Match names no version of it.")
	}
	return version, nil
}
