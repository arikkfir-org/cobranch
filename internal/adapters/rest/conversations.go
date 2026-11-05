package rest

import (
	"context"

	"github.com/google/uuid"

	"cobranch.dev/internal/services/chats"
)

// organizationConversation finds the viewer's own conversation about the organization the path names, for a viewer
// with org.read there.
func (a api) organizationConversation(ctx context.Context, orgSlug string) (chats.Viewer, chats.Subject, error) {
	v, err := viewer(ctx)
	if err != nil {
		return chats.Viewer{}, chats.Subject{}, err
	}
	org, err := a.org(ctx, v, orgSlug)
	if err != nil {
		return chats.Viewer{}, chats.Subject{}, err
	}
	subj, err := a.chats.Organization(ctx, v, org.ID)
	if err != nil {
		return chats.Viewer{}, chats.Subject{}, fail(err, "organization")
	}
	return v, subj, nil
}

// repositoryConversation finds the viewer's own conversation about the repository the path names, for a viewer with
// repo.read on it.
func (a api) repositoryConversation(ctx context.Context, orgSlug, slug string) (chats.Viewer, chats.Subject, error) {
	v, err := viewer(ctx)
	if err != nil {
		return chats.Viewer{}, chats.Subject{}, err
	}
	repo, err := a.codeRepo(ctx, v, orgSlug, slug)
	if err != nil {
		return chats.Viewer{}, chats.Subject{}, err
	}
	subj, err := a.chats.Repository(ctx, v, repo.OrgID, repo.ID)
	if err != nil {
		return chats.Viewer{}, chats.Subject{}, fail(err, "repository")
	}
	return v, subj, nil
}

// agentConversation finds the viewer's own conversation with the organization's agent the path names, for a viewer
// with org.read there.
func (a api) agentConversation(ctx context.Context, orgSlug string, agentID uuid.UUID) (chats.Viewer, chats.Subject, error) {
	v, err := viewer(ctx)
	if err != nil {
		return chats.Viewer{}, chats.Subject{}, err
	}
	org, err := a.org(ctx, v, orgSlug)
	if err != nil {
		return chats.Viewer{}, chats.Subject{}, err
	}
	subj, err := a.chats.Agent(ctx, v, org.ID, agentID)
	if err != nil {
		return chats.Viewer{}, chats.Subject{}, fail(err, "agent")
	}
	return v, subj, nil
}

// ListOrganizationConversationThreads lists a page of the viewer's conversation about an organization, and links the next.
func (a api) ListOrganizationConversationThreads(ctx context.Context, req ListOrganizationConversationThreadsRequestObject) (ListOrganizationConversationThreadsResponseObject, error) {
	v, subj, err := a.organizationConversation(ctx, req.Org)
	if err != nil {
		return nil, err
	}
	out := ListOrganizationConversationThreads200JSONResponse{}
	out.Body.Threads, out.Body.After, out.Headers.Link, err = a.chatPage(ctx, v, subj, req.Params.Cursor, req.Params.Limit)
	if err != nil {
		return nil, err
	}
	return out, nil
}

// StartOrganizationConversationThread starts a thread on the viewer's conversation about an organization for the viewer.
func (a api) StartOrganizationConversationThread(ctx context.Context, req StartOrganizationConversationThreadRequestObject) (StartOrganizationConversationThreadResponseObject, error) {
	v, subj, err := a.organizationConversation(ctx, req.Org)
	if err != nil {
		return nil, err
	}
	body, tag, location, err := a.startThread(ctx, v, subj, *req.Body)
	if err != nil {
		return nil, err
	}
	return StartOrganizationConversationThread201JSONResponse{Body: body, Headers: StartOrganizationConversationThread201ResponseHeaders{ETag: tag, Location: location}}, nil
}

// GetOrganizationConversationThread answers one of the threads of the viewer's conversation about an organization with its messages.
func (a api) GetOrganizationConversationThread(ctx context.Context, req GetOrganizationConversationThreadRequestObject) (GetOrganizationConversationThreadResponseObject, error) {
	v, subj, err := a.organizationConversation(ctx, req.Org)
	if err != nil {
		return nil, err
	}
	body, tag, err := a.thread(ctx, v, subj, req.ThreadID)
	if err != nil {
		return nil, err
	}
	if matches(req.Params.IfNoneMatch, tag) {
		return GetOrganizationConversationThread304Response{Headers: GetOrganizationConversationThread304ResponseHeaders{ETag: tag}}, nil
	}
	return GetOrganizationConversationThread200JSONResponse{Body: body, Headers: GetOrganizationConversationThread200ResponseHeaders{ETag: tag}}, nil
}

// UpdateOrganizationConversationThread resolves one of the threads of the viewer's conversation about an organization, or opens it again.
func (a api) UpdateOrganizationConversationThread(ctx context.Context, req UpdateOrganizationConversationThreadRequestObject) (UpdateOrganizationConversationThreadResponseObject, error) {
	v, subj, err := a.organizationConversation(ctx, req.Org)
	if err != nil {
		return nil, err
	}
	body, tag, err := a.resolveThread(ctx, v, subj, req.ThreadID, req.Body.Resolved, req.Params.IfMatch)
	if err != nil {
		return nil, err
	}
	return UpdateOrganizationConversationThread200JSONResponse{Body: body, Headers: UpdateOrganizationConversationThread200ResponseHeaders{ETag: tag}}, nil
}

// PostOrganizationConversationMessage posts a message to one of the threads of the viewer's conversation about an organization for the viewer.
func (a api) PostOrganizationConversationMessage(ctx context.Context, req PostOrganizationConversationMessageRequestObject) (PostOrganizationConversationMessageResponseObject, error) {
	v, subj, err := a.organizationConversation(ctx, req.Org)
	if err != nil {
		return nil, err
	}
	body, tag, location, err := a.postMessage(ctx, v, subj, req.ThreadID, *req.Body)
	if err != nil {
		return nil, err
	}
	return PostOrganizationConversationMessage201JSONResponse{Body: body, Headers: PostOrganizationConversationMessage201ResponseHeaders{ETag: tag, Location: location}}, nil
}

// GetOrganizationConversationMessage answers one of the messages of one of the threads of the viewer's conversation about an organization.
func (a api) GetOrganizationConversationMessage(ctx context.Context, req GetOrganizationConversationMessageRequestObject) (GetOrganizationConversationMessageResponseObject, error) {
	v, subj, err := a.organizationConversation(ctx, req.Org)
	if err != nil {
		return nil, err
	}
	body, tag, err := a.message(ctx, v, subj, req.ThreadID, req.MessageID)
	if err != nil {
		return nil, err
	}
	if matches(req.Params.IfNoneMatch, tag) {
		return GetOrganizationConversationMessage304Response{Headers: GetOrganizationConversationMessage304ResponseHeaders{ETag: tag}}, nil
	}
	return GetOrganizationConversationMessage200JSONResponse{Body: body, Headers: GetOrganizationConversationMessage200ResponseHeaders{ETag: tag}}, nil
}

// EditOrganizationConversationMessage changes the body of one of the viewer's messages in their conversation about an organization.
func (a api) EditOrganizationConversationMessage(ctx context.Context, req EditOrganizationConversationMessageRequestObject) (EditOrganizationConversationMessageResponseObject, error) {
	v, subj, err := a.organizationConversation(ctx, req.Org)
	if err != nil {
		return nil, err
	}
	body, tag, err := a.editMessage(ctx, v, subj, req.ThreadID, req.MessageID, req.Body.Body, req.Params.IfMatch)
	if err != nil {
		return nil, err
	}
	return EditOrganizationConversationMessage200JSONResponse{Body: body, Headers: EditOrganizationConversationMessage200ResponseHeaders{ETag: tag}}, nil
}

// DeleteOrganizationConversationMessage deletes a message in their conversation about an organization, keeping its place in its thread.
func (a api) DeleteOrganizationConversationMessage(ctx context.Context, req DeleteOrganizationConversationMessageRequestObject) (DeleteOrganizationConversationMessageResponseObject, error) {
	v, subj, err := a.organizationConversation(ctx, req.Org)
	if err != nil {
		return nil, err
	}
	if err := a.deleteMessage(ctx, v, subj, req.ThreadID, req.MessageID, req.Params.IfMatch); err != nil {
		return nil, err
	}
	return DeleteOrganizationConversationMessage204Response{}, nil
}

// ListOrganizationConversationUpdates lists the changes to the viewer's conversation about an organization after a cursor, waiting for one when there is none yet.
func (a api) ListOrganizationConversationUpdates(ctx context.Context, req ListOrganizationConversationUpdatesRequestObject) (ListOrganizationConversationUpdatesResponseObject, error) {
	v, subj, err := a.organizationConversation(ctx, req.Org)
	if err != nil {
		return nil, err
	}
	updates, after, err := a.chatUpdates(ctx, v, subj, req.Params.After, req.Params.Wait)
	if err != nil {
		return nil, err
	}
	return ListOrganizationConversationUpdates200JSONResponse{Updates: updates, After: after}, nil
}

// ListRepositoryConversationThreads lists a page of the viewer's conversation about a repository, and links the next.
func (a api) ListRepositoryConversationThreads(ctx context.Context, req ListRepositoryConversationThreadsRequestObject) (ListRepositoryConversationThreadsResponseObject, error) {
	v, subj, err := a.repositoryConversation(ctx, req.Org, req.Repo)
	if err != nil {
		return nil, err
	}
	out := ListRepositoryConversationThreads200JSONResponse{}
	out.Body.Threads, out.Body.After, out.Headers.Link, err = a.chatPage(ctx, v, subj, req.Params.Cursor, req.Params.Limit)
	if err != nil {
		return nil, err
	}
	return out, nil
}

// StartRepositoryConversationThread starts a thread on the viewer's conversation about a repository for the viewer.
func (a api) StartRepositoryConversationThread(ctx context.Context, req StartRepositoryConversationThreadRequestObject) (StartRepositoryConversationThreadResponseObject, error) {
	v, subj, err := a.repositoryConversation(ctx, req.Org, req.Repo)
	if err != nil {
		return nil, err
	}
	body, tag, location, err := a.startThread(ctx, v, subj, *req.Body)
	if err != nil {
		return nil, err
	}
	return StartRepositoryConversationThread201JSONResponse{Body: body, Headers: StartRepositoryConversationThread201ResponseHeaders{ETag: tag, Location: location}}, nil
}

// GetRepositoryConversationThread answers one of the threads of the viewer's conversation about a repository with its messages.
func (a api) GetRepositoryConversationThread(ctx context.Context, req GetRepositoryConversationThreadRequestObject) (GetRepositoryConversationThreadResponseObject, error) {
	v, subj, err := a.repositoryConversation(ctx, req.Org, req.Repo)
	if err != nil {
		return nil, err
	}
	body, tag, err := a.thread(ctx, v, subj, req.ThreadID)
	if err != nil {
		return nil, err
	}
	if matches(req.Params.IfNoneMatch, tag) {
		return GetRepositoryConversationThread304Response{Headers: GetRepositoryConversationThread304ResponseHeaders{ETag: tag}}, nil
	}
	return GetRepositoryConversationThread200JSONResponse{Body: body, Headers: GetRepositoryConversationThread200ResponseHeaders{ETag: tag}}, nil
}

// UpdateRepositoryConversationThread resolves one of the threads of the viewer's conversation about a repository, or opens it again.
func (a api) UpdateRepositoryConversationThread(ctx context.Context, req UpdateRepositoryConversationThreadRequestObject) (UpdateRepositoryConversationThreadResponseObject, error) {
	v, subj, err := a.repositoryConversation(ctx, req.Org, req.Repo)
	if err != nil {
		return nil, err
	}
	body, tag, err := a.resolveThread(ctx, v, subj, req.ThreadID, req.Body.Resolved, req.Params.IfMatch)
	if err != nil {
		return nil, err
	}
	return UpdateRepositoryConversationThread200JSONResponse{Body: body, Headers: UpdateRepositoryConversationThread200ResponseHeaders{ETag: tag}}, nil
}

// PostRepositoryConversationMessage posts a message to one of the threads of the viewer's conversation about a repository for the viewer.
func (a api) PostRepositoryConversationMessage(ctx context.Context, req PostRepositoryConversationMessageRequestObject) (PostRepositoryConversationMessageResponseObject, error) {
	v, subj, err := a.repositoryConversation(ctx, req.Org, req.Repo)
	if err != nil {
		return nil, err
	}
	body, tag, location, err := a.postMessage(ctx, v, subj, req.ThreadID, *req.Body)
	if err != nil {
		return nil, err
	}
	return PostRepositoryConversationMessage201JSONResponse{Body: body, Headers: PostRepositoryConversationMessage201ResponseHeaders{ETag: tag, Location: location}}, nil
}

// GetRepositoryConversationMessage answers one of the messages of one of the threads of the viewer's conversation about a repository.
func (a api) GetRepositoryConversationMessage(ctx context.Context, req GetRepositoryConversationMessageRequestObject) (GetRepositoryConversationMessageResponseObject, error) {
	v, subj, err := a.repositoryConversation(ctx, req.Org, req.Repo)
	if err != nil {
		return nil, err
	}
	body, tag, err := a.message(ctx, v, subj, req.ThreadID, req.MessageID)
	if err != nil {
		return nil, err
	}
	if matches(req.Params.IfNoneMatch, tag) {
		return GetRepositoryConversationMessage304Response{Headers: GetRepositoryConversationMessage304ResponseHeaders{ETag: tag}}, nil
	}
	return GetRepositoryConversationMessage200JSONResponse{Body: body, Headers: GetRepositoryConversationMessage200ResponseHeaders{ETag: tag}}, nil
}

// EditRepositoryConversationMessage changes the body of one of the viewer's messages in their conversation about a repository.
func (a api) EditRepositoryConversationMessage(ctx context.Context, req EditRepositoryConversationMessageRequestObject) (EditRepositoryConversationMessageResponseObject, error) {
	v, subj, err := a.repositoryConversation(ctx, req.Org, req.Repo)
	if err != nil {
		return nil, err
	}
	body, tag, err := a.editMessage(ctx, v, subj, req.ThreadID, req.MessageID, req.Body.Body, req.Params.IfMatch)
	if err != nil {
		return nil, err
	}
	return EditRepositoryConversationMessage200JSONResponse{Body: body, Headers: EditRepositoryConversationMessage200ResponseHeaders{ETag: tag}}, nil
}

// DeleteRepositoryConversationMessage deletes a message in their conversation about a repository, keeping its place in its thread.
func (a api) DeleteRepositoryConversationMessage(ctx context.Context, req DeleteRepositoryConversationMessageRequestObject) (DeleteRepositoryConversationMessageResponseObject, error) {
	v, subj, err := a.repositoryConversation(ctx, req.Org, req.Repo)
	if err != nil {
		return nil, err
	}
	if err := a.deleteMessage(ctx, v, subj, req.ThreadID, req.MessageID, req.Params.IfMatch); err != nil {
		return nil, err
	}
	return DeleteRepositoryConversationMessage204Response{}, nil
}

// ListRepositoryConversationUpdates lists the changes to the viewer's conversation about a repository after a cursor, waiting for one when there is none yet.
func (a api) ListRepositoryConversationUpdates(ctx context.Context, req ListRepositoryConversationUpdatesRequestObject) (ListRepositoryConversationUpdatesResponseObject, error) {
	v, subj, err := a.repositoryConversation(ctx, req.Org, req.Repo)
	if err != nil {
		return nil, err
	}
	updates, after, err := a.chatUpdates(ctx, v, subj, req.Params.After, req.Params.Wait)
	if err != nil {
		return nil, err
	}
	return ListRepositoryConversationUpdates200JSONResponse{Updates: updates, After: after}, nil
}

// ListAgentConversationThreads lists a page of the viewer's conversation with an agent, and links the next.
func (a api) ListAgentConversationThreads(ctx context.Context, req ListAgentConversationThreadsRequestObject) (ListAgentConversationThreadsResponseObject, error) {
	v, subj, err := a.agentConversation(ctx, req.Org, req.AgentID)
	if err != nil {
		return nil, err
	}
	out := ListAgentConversationThreads200JSONResponse{}
	out.Body.Threads, out.Body.After, out.Headers.Link, err = a.chatPage(ctx, v, subj, req.Params.Cursor, req.Params.Limit)
	if err != nil {
		return nil, err
	}
	return out, nil
}

// StartAgentConversationThread starts a thread on the viewer's conversation with an agent for the viewer.
func (a api) StartAgentConversationThread(ctx context.Context, req StartAgentConversationThreadRequestObject) (StartAgentConversationThreadResponseObject, error) {
	v, subj, err := a.agentConversation(ctx, req.Org, req.AgentID)
	if err != nil {
		return nil, err
	}
	body, tag, location, err := a.startThread(ctx, v, subj, *req.Body)
	if err != nil {
		return nil, err
	}
	return StartAgentConversationThread201JSONResponse{Body: body, Headers: StartAgentConversationThread201ResponseHeaders{ETag: tag, Location: location}}, nil
}

// GetAgentConversationThread answers one of the threads of the viewer's conversation with an agent with its messages.
func (a api) GetAgentConversationThread(ctx context.Context, req GetAgentConversationThreadRequestObject) (GetAgentConversationThreadResponseObject, error) {
	v, subj, err := a.agentConversation(ctx, req.Org, req.AgentID)
	if err != nil {
		return nil, err
	}
	body, tag, err := a.thread(ctx, v, subj, req.ThreadID)
	if err != nil {
		return nil, err
	}
	if matches(req.Params.IfNoneMatch, tag) {
		return GetAgentConversationThread304Response{Headers: GetAgentConversationThread304ResponseHeaders{ETag: tag}}, nil
	}
	return GetAgentConversationThread200JSONResponse{Body: body, Headers: GetAgentConversationThread200ResponseHeaders{ETag: tag}}, nil
}

// UpdateAgentConversationThread resolves one of the threads of the viewer's conversation with an agent, or opens it again.
func (a api) UpdateAgentConversationThread(ctx context.Context, req UpdateAgentConversationThreadRequestObject) (UpdateAgentConversationThreadResponseObject, error) {
	v, subj, err := a.agentConversation(ctx, req.Org, req.AgentID)
	if err != nil {
		return nil, err
	}
	body, tag, err := a.resolveThread(ctx, v, subj, req.ThreadID, req.Body.Resolved, req.Params.IfMatch)
	if err != nil {
		return nil, err
	}
	return UpdateAgentConversationThread200JSONResponse{Body: body, Headers: UpdateAgentConversationThread200ResponseHeaders{ETag: tag}}, nil
}

// PostAgentConversationMessage posts a message to one of the threads of the viewer's conversation with an agent for the viewer.
func (a api) PostAgentConversationMessage(ctx context.Context, req PostAgentConversationMessageRequestObject) (PostAgentConversationMessageResponseObject, error) {
	v, subj, err := a.agentConversation(ctx, req.Org, req.AgentID)
	if err != nil {
		return nil, err
	}
	body, tag, location, err := a.postMessage(ctx, v, subj, req.ThreadID, *req.Body)
	if err != nil {
		return nil, err
	}
	return PostAgentConversationMessage201JSONResponse{Body: body, Headers: PostAgentConversationMessage201ResponseHeaders{ETag: tag, Location: location}}, nil
}

// GetAgentConversationMessage answers one of the messages of one of the threads of the viewer's conversation with an agent.
func (a api) GetAgentConversationMessage(ctx context.Context, req GetAgentConversationMessageRequestObject) (GetAgentConversationMessageResponseObject, error) {
	v, subj, err := a.agentConversation(ctx, req.Org, req.AgentID)
	if err != nil {
		return nil, err
	}
	body, tag, err := a.message(ctx, v, subj, req.ThreadID, req.MessageID)
	if err != nil {
		return nil, err
	}
	if matches(req.Params.IfNoneMatch, tag) {
		return GetAgentConversationMessage304Response{Headers: GetAgentConversationMessage304ResponseHeaders{ETag: tag}}, nil
	}
	return GetAgentConversationMessage200JSONResponse{Body: body, Headers: GetAgentConversationMessage200ResponseHeaders{ETag: tag}}, nil
}

// EditAgentConversationMessage changes the body of one of the viewer's messages in their conversation with an agent.
func (a api) EditAgentConversationMessage(ctx context.Context, req EditAgentConversationMessageRequestObject) (EditAgentConversationMessageResponseObject, error) {
	v, subj, err := a.agentConversation(ctx, req.Org, req.AgentID)
	if err != nil {
		return nil, err
	}
	body, tag, err := a.editMessage(ctx, v, subj, req.ThreadID, req.MessageID, req.Body.Body, req.Params.IfMatch)
	if err != nil {
		return nil, err
	}
	return EditAgentConversationMessage200JSONResponse{Body: body, Headers: EditAgentConversationMessage200ResponseHeaders{ETag: tag}}, nil
}

// DeleteAgentConversationMessage deletes a message in their conversation with an agent, keeping its place in its thread.
func (a api) DeleteAgentConversationMessage(ctx context.Context, req DeleteAgentConversationMessageRequestObject) (DeleteAgentConversationMessageResponseObject, error) {
	v, subj, err := a.agentConversation(ctx, req.Org, req.AgentID)
	if err != nil {
		return nil, err
	}
	if err := a.deleteMessage(ctx, v, subj, req.ThreadID, req.MessageID, req.Params.IfMatch); err != nil {
		return nil, err
	}
	return DeleteAgentConversationMessage204Response{}, nil
}

// ListAgentConversationUpdates lists the changes to the viewer's conversation with an agent after a cursor, waiting for one when there is none yet.
func (a api) ListAgentConversationUpdates(ctx context.Context, req ListAgentConversationUpdatesRequestObject) (ListAgentConversationUpdatesResponseObject, error) {
	v, subj, err := a.agentConversation(ctx, req.Org, req.AgentID)
	if err != nil {
		return nil, err
	}
	updates, after, err := a.chatUpdates(ctx, v, subj, req.Params.After, req.Params.Wait)
	if err != nil {
		return nil, err
	}
	return ListAgentConversationUpdates200JSONResponse{Updates: updates, After: after}, nil
}
