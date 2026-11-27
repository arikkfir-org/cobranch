package agents

import (
	"context"

	"github.com/google/uuid"

	"cobranch.dev/internal/services/live"
)

// WithNotifier has the service's followers read an organization's agents, model providers, models and tasks again as
// soon as n says they may have changed; without it, they read them once a minute.
func (s *Service) WithNotifier(n live.Notifier) *Service {
	s.notifier = n
	return s
}

// FollowModelProviders sends the providers the organization added, as ModelProviders lists them, at once, then again
// each time they change, until ctx ends or a read fails, and then closes the channel. Settings · Models follows them
// this way, so a provider connected elsewhere shows.
func (s *Service) FollowModelProviders(ctx context.Context, v Viewer, orgID uuid.UUID) (<-chan []ModelProvider, error) {
	ctx, span := tracer.Start(ctx, "agents.Service.FollowModelProviders", onOrg(orgID))
	defer span.End()
	list, err := live.Follow(ctx, s.notifier, live.Poll, func(ctx context.Context) ([]ModelProvider, error) {
		return s.ModelProviders(ctx, v, orgID)
	}, orgID)
	return list, failed(span, "following the model providers", err)
}

// FollowModels sends the organization's models, as Models lists them, at once, then again each time they change,
// until ctx ends or a read fails, and then closes the channel. Settings · Models follows them this way, so a model
// added, repriced or removed elsewhere, or an agent put on one, shows.
func (s *Service) FollowModels(ctx context.Context, v Viewer, orgID uuid.UUID) (<-chan []Model, error) {
	ctx, span := tracer.Start(ctx, "agents.Service.FollowModels", onOrg(orgID))
	defer span.End()
	list, err := live.Follow(ctx, s.notifier, live.Poll, func(ctx context.Context) ([]Model, error) {
		return s.Models(ctx, v, orgID)
	}, orgID)
	return list, failed(span, "following the models", err)
}

// FollowAgents sends the organization's agents, as Agents lists them, at once, then again each time they change, until
// ctx ends or a read fails, and then closes the channel. Settings · Agents follows them this way, so an agent created,
// changed, granted a role or deleted elsewhere shows.
func (s *Service) FollowAgents(ctx context.Context, v Viewer, orgID uuid.UUID) (<-chan []Agent, error) {
	ctx, span := tracer.Start(ctx, "agents.Service.FollowAgents", onOrg(orgID))
	defer span.End()
	list, err := live.Follow(ctx, s.notifier, live.Poll, func(ctx context.Context) ([]Agent, error) {
		return s.Agents(ctx, v, orgID)
	}, orgID)
	return list, failed(span, "following the agents", err)
}

// FollowTasks sends the page of the organization's tasks that Tasks answers for the filter and limit at once, then
// again each time it changes (a task assigned, started, ended or cancelled, or what one spent), until ctx ends or a
// read fails, and then closes the channel. The Agents and Activity pages and the sidebar follow the tasks at work.
func (s *Service) FollowTasks(ctx context.Context, v Viewer, orgID uuid.UUID, f TaskFilter, limit int) (<-chan TaskPage, error) {
	ctx, span := tracer.Start(ctx, "agents.Service.FollowTasks", onOrg(orgID))
	defer span.End()
	page, err := live.Follow(ctx, s.notifier, live.Poll, func(ctx context.Context) (TaskPage, error) {
		return s.Tasks(ctx, v, orgID, f, limit)
	}, orgID)
	return page, failed(span, "following the tasks", err)
}
