package graphql

import (
	"context"

	"github.com/google/uuid"

	"cobranch.dev/internal/services/chats"
	"cobranch.dev/internal/services/orgs"
)

// conversationOf is the viewer and the viewer's own conversation that a mutation or a subscription names: about the
// organization, or one of its repositories, or with one of its agents.
func (r *Resolver) conversationOf(ctx context.Context, s ConversationSubject) (chats.Viewer, chats.Subject, error) {
	if s.RepositoryID != nil && s.AgentID != nil {
		return chats.Viewer{}, chats.Subject{}, fail(ctx, &orgs.ValidationError{Field: "subject", Code: "conversation.invalid",
			Message: "a conversation is about a repository or with an agent, not both"}, "", "")
	}
	repoID, agentID := uuid.Nil, uuid.Nil
	var err error
	if s.RepositoryID != nil {
		if repoID, err = parseID(ctx, *s.RepositoryID, "repository"); err != nil {
			return chats.Viewer{}, chats.Subject{}, err
		}
	}
	if s.AgentID != nil {
		if agentID, err = parseID(ctx, *s.AgentID, "agent"); err != nil {
			return chats.Viewer{}, chats.Subject{}, err
		}
	}
	return r.conversation(ctx, s.OrganizationID, repoID, agentID)
}

// conversation is the viewer and their own conversation about the organization organizationID names, about its
// repository repoID when set, or with its agent agentID when set.
func (r *Resolver) conversation(ctx context.Context, organizationID string, repoID, agentID uuid.UUID) (chats.Viewer, chats.Subject, error) {
	v, err := viewer(ctx)
	if err != nil {
		return chats.Viewer{}, chats.Subject{}, err
	}
	orgID, err := parseID(ctx, organizationID, "organization")
	if err != nil {
		return chats.Viewer{}, chats.Subject{}, err
	}
	var subj chats.Subject
	kind := chats.KindOrganization
	switch {
	case repoID != uuid.Nil:
		kind = chats.KindRepository
		subj, err = r.Chats.Repository(ctx, v.User, orgID, repoID)
	case agentID != uuid.Nil:
		kind = chats.KindAgent
		subj, err = r.Chats.Agent(ctx, v.User, orgID, agentID)
	default:
		subj, err = r.Chats.Organization(ctx, v.User, orgID)
	}
	if err != nil {
		return chats.Viewer{}, chats.Subject{}, fail(ctx, err, string(kind), "finding the conversation")
	}
	return v.User, subj, nil
}
