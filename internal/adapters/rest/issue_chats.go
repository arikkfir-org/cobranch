package rest

import (
	"context"

	"cobranch.dev/internal/services/chats"
)

// issueChat finds the chat of the organization's issue the path names, its comments, for a viewer with issues.read
// there.
func (a api) issueChat(ctx context.Context, orgSlug string, number int) (chats.Viewer, chats.Subject, error) {
	v, err := viewer(ctx)
	if err != nil {
		return chats.Viewer{}, chats.Subject{}, err
	}
	org, err := a.org(ctx, v, orgSlug)
	if err != nil {
		return chats.Viewer{}, chats.Subject{}, err
	}
	subj, err := a.chats.Issue(ctx, v, org.ID, number)
	if err != nil {
		return chats.Viewer{}, chats.Subject{}, fail(err, "issue")
	}
	return v, subj, nil
}

// ListIssueChatThreads lists a page of an issue's chat, and links the next.
func (a api) ListIssueChatThreads(ctx context.Context, req ListIssueChatThreadsRequestObject) (ListIssueChatThreadsResponseObject, error) {
	v, subj, err := a.issueChat(ctx, req.Org, req.Number)
	if err != nil {
		return nil, err
	}
	out := ListIssueChatThreads200JSONResponse{}
	out.Body.Threads, out.Body.After, out.Headers.Link, err = a.chatPage(ctx, v, subj, req.Params.Cursor, req.Params.Limit)
	if err != nil {
		return nil, err
	}
	return out, nil
}

// StartIssueChatThread starts a thread on an issue's chat for the viewer.
func (a api) StartIssueChatThread(ctx context.Context, req StartIssueChatThreadRequestObject) (StartIssueChatThreadResponseObject, error) {
	v, subj, err := a.issueChat(ctx, req.Org, req.Number)
	if err != nil {
		return nil, err
	}
	body, tag, location, err := a.startThread(ctx, v, subj, *req.Body)
	if err != nil {
		return nil, err
	}
	return StartIssueChatThread201JSONResponse{Body: body, Headers: StartIssueChatThread201ResponseHeaders{ETag: tag, Location: location}}, nil
}

// GetIssueChatThread answers one of an issue's chat threads with its messages.
func (a api) GetIssueChatThread(ctx context.Context, req GetIssueChatThreadRequestObject) (GetIssueChatThreadResponseObject, error) {
	v, subj, err := a.issueChat(ctx, req.Org, req.Number)
	if err != nil {
		return nil, err
	}
	body, tag, err := a.thread(ctx, v, subj, req.ThreadID)
	if err != nil {
		return nil, err
	}
	if matches(req.Params.IfNoneMatch, tag) {
		return GetIssueChatThread304Response{Headers: GetIssueChatThread304ResponseHeaders{ETag: tag}}, nil
	}
	return GetIssueChatThread200JSONResponse{Body: body, Headers: GetIssueChatThread200ResponseHeaders{ETag: tag}}, nil
}

// UpdateIssueChatThread resolves one of an issue's chat threads, or opens it again.
func (a api) UpdateIssueChatThread(ctx context.Context, req UpdateIssueChatThreadRequestObject) (UpdateIssueChatThreadResponseObject, error) {
	v, subj, err := a.issueChat(ctx, req.Org, req.Number)
	if err != nil {
		return nil, err
	}
	body, tag, err := a.resolveThread(ctx, v, subj, req.ThreadID, req.Body.Resolved, req.Params.IfMatch)
	if err != nil {
		return nil, err
	}
	return UpdateIssueChatThread200JSONResponse{Body: body, Headers: UpdateIssueChatThread200ResponseHeaders{ETag: tag}}, nil
}

// PostIssueChatMessage posts a message to one of an issue's chat threads for the viewer.
func (a api) PostIssueChatMessage(ctx context.Context, req PostIssueChatMessageRequestObject) (PostIssueChatMessageResponseObject, error) {
	v, subj, err := a.issueChat(ctx, req.Org, req.Number)
	if err != nil {
		return nil, err
	}
	body, tag, location, err := a.postMessage(ctx, v, subj, req.ThreadID, *req.Body)
	if err != nil {
		return nil, err
	}
	return PostIssueChatMessage201JSONResponse{Body: body, Headers: PostIssueChatMessage201ResponseHeaders{ETag: tag, Location: location}}, nil
}

// GetIssueChatMessage answers one of the messages of one of an issue's chat threads.
func (a api) GetIssueChatMessage(ctx context.Context, req GetIssueChatMessageRequestObject) (GetIssueChatMessageResponseObject, error) {
	v, subj, err := a.issueChat(ctx, req.Org, req.Number)
	if err != nil {
		return nil, err
	}
	body, tag, err := a.message(ctx, v, subj, req.ThreadID, req.MessageID)
	if err != nil {
		return nil, err
	}
	if matches(req.Params.IfNoneMatch, tag) {
		return GetIssueChatMessage304Response{Headers: GetIssueChatMessage304ResponseHeaders{ETag: tag}}, nil
	}
	return GetIssueChatMessage200JSONResponse{Body: body, Headers: GetIssueChatMessage200ResponseHeaders{ETag: tag}}, nil
}

// EditIssueChatMessage changes the body of one of the viewer's messages on an issue.
func (a api) EditIssueChatMessage(ctx context.Context, req EditIssueChatMessageRequestObject) (EditIssueChatMessageResponseObject, error) {
	v, subj, err := a.issueChat(ctx, req.Org, req.Number)
	if err != nil {
		return nil, err
	}
	body, tag, err := a.editMessage(ctx, v, subj, req.ThreadID, req.MessageID, req.Body.Body, req.Params.IfMatch)
	if err != nil {
		return nil, err
	}
	return EditIssueChatMessage200JSONResponse{Body: body, Headers: EditIssueChatMessage200ResponseHeaders{ETag: tag}}, nil
}

// DeleteIssueChatMessage deletes a message on an issue, keeping its place in its thread.
func (a api) DeleteIssueChatMessage(ctx context.Context, req DeleteIssueChatMessageRequestObject) (DeleteIssueChatMessageResponseObject, error) {
	v, subj, err := a.issueChat(ctx, req.Org, req.Number)
	if err != nil {
		return nil, err
	}
	if err := a.deleteMessage(ctx, v, subj, req.ThreadID, req.MessageID, req.Params.IfMatch); err != nil {
		return nil, err
	}
	return DeleteIssueChatMessage204Response{}, nil
}

// ListIssueChatUpdates lists the changes to an issue's chat after a cursor, waiting for one when there is none yet.
func (a api) ListIssueChatUpdates(ctx context.Context, req ListIssueChatUpdatesRequestObject) (ListIssueChatUpdatesResponseObject, error) {
	v, subj, err := a.issueChat(ctx, req.Org, req.Number)
	if err != nil {
		return nil, err
	}
	updates, after, err := a.chatUpdates(ctx, v, subj, req.Params.After, req.Params.Wait)
	if err != nil {
		return nil, err
	}
	return ListIssueChatUpdates200JSONResponse{Updates: updates, After: after}, nil
}
