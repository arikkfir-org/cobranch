package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"cobranch.dev/internal/adapters/postgres/queries"
	"cobranch.dev/internal/services/agents"
	"cobranch.dev/internal/services/code"
	"cobranch.dev/internal/services/git"
	"cobranch.dev/internal/services/jobs"
)

// reasonRetired is why a retired agent's tasks were cancelled.
const reasonRetired = "Cancelled: the agent is retired, since main's /" + git.SettingsFile + " no longer declares it"

// syncAgents brings the repository's agents in line with the /cobranch.yaml the push leaves at the default branch's
// tip, in the push's transaction, when the push moves that branch: it records each agent the file declares, bringing a
// retired one back, and retires the others, cancelling their tasks
// (https://github.com/arikkfir-org/cobranch/blob/main/docs/cobranch/designs/repo-agents.md, "Repository agents"). It
// refuses the push, naming the line, with a *git.RefusedError for an agent that lists a permission beyond the ceiling
// or names a model the organization doesn't have. A file that doesn't parse never reaches the tip: the push is refused
// before.
func syncAgents(ctx context.Context, q *queries.Queries, orgID uuid.UUID, push git.Push) error {
	if !push.MovesDefault() {
		return nil
	}
	declared, ceiling, err := mainAgents(push)
	if err != nil || declared == nil {
		return err
	}
	if err := checkDeclared(ctx, q, orgID, declared, ceiling); err != nil {
		return err
	}
	current, err := q.RepositoryAgents(ctx, &push.RepoID)
	if err != nil {
		return fmt.Errorf("listing the repository's agents: %w", err)
	}
	def, err := q.DefaultModel(ctx, orgID)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("reading the organization's default model: %w", err)
	}
	names := make([]string, 0, len(declared))
	for _, d := range declared {
		if d.Provider == "" && def.Provider != "" {
			d.Provider, d.Model = def.Provider, def.Model
		}
		if err := recordAgent(ctx, q, orgID, push, current, d); err != nil {
			return err
		}
		names = append(names, d.Name)
	}
	return retireAgents(ctx, q, orgID, push, names)
}

// mainAgents reads the agents the push's /cobranch.yaml at the default branch's tip declares, and the ceiling it
// names: none without the file or once the push deletes the branch; nil when the file doesn't parse, which leaves the
// agents as they are.
func mainAgents(push git.Push) ([]agents.Declared, string, error) {
	i := slices.IndexFunc(push.Settings, func(t git.TipSettings) bool { return t.Branch == git.DefaultBranch })
	switch {
	case i < 0 || push.Settings[i].Blob == "":
		return []agents.Declared{}, jobs.DefaultCeiling, nil
	case push.Settings[i].Error != "":
		return nil, "", nil
	}
	var settings code.Settings
	if err := json.Unmarshal(push.Settings[i].Settings, &settings); err != nil {
		return nil, "", fmt.Errorf("decoding the settings of %s: %w", git.DefaultBranch, err)
	}
	if settings.Agents == nil {
		settings.Agents = []agents.Declared{}
	}
	return settings.Agents, settings.Repository.Ceiling, nil
}

// checkDeclared refuses agents that list a permission beyond the repository role ceiling names, or name a model the
// organization doesn't have, naming the line of the first.
func checkDeclared(ctx context.Context, q *queries.Queries, orgID uuid.UUID, declared []agents.Declared, ceiling string) error {
	if len(declared) == 0 {
		return nil
	}
	allowed, err := q.RepositoryRolePermissions(ctx, ceiling)
	if errors.Is(err, pgx.ErrNoRows) {
		return refused(declared[0].Line, "repository.jobs.ceiling names no repository role, "+ceiling+", which the agents stay within")
	}
	if err != nil {
		return fmt.Errorf("reading the ceiling: %w", err)
	}
	for _, d := range declared {
		for _, p := range d.Permissions {
			if !slices.Contains(allowed, p) {
				return refused(d.Line, fmt.Sprintf("agent %s lists %s, beyond the ceiling, %s", d.Name, p, ceiling))
			}
		}
		if d.Provider == "" {
			continue
		}
		has, err := q.HasModel(ctx, orgID, d.Provider, d.Model)
		if err != nil {
			return fmt.Errorf("finding model %s/%s: %w", d.Provider, d.Model, err)
		}
		if !has {
			return refused(d.Line, fmt.Sprintf("agent %s runs on %s/%s, which isn't one of the organization's models", d.Name, d.Provider, d.Model))
		}
	}
	return nil
}

// refused is a push refused for what main's /cobranch.yaml says at line.
func refused(line int, message string) error {
	return &git.RefusedError{Message: (&code.SettingsError{Line: line, Message: message}).Error()}
}

// recordAgent records the agent d, as main's file now declares it: a new principal of kind agent of the repository
// when it has none of d's name, which the pusher declared, and otherwise the one it has, changed.
func recordAgent(ctx context.Context, q *queries.Queries, orgID uuid.UUID, push git.Push, current []queries.RepositoryAgentsRow, d agents.Declared) error {
	secrets, err := json.Marshal(d.Secrets)
	if err != nil {
		return fmt.Errorf("encoding agent %s's secrets: %w", d.Name, err)
	}
	if d.Secrets == nil {
		secrets = []byte("{}")
	}
	if i := slices.IndexFunc(current, func(r queries.RepositoryAgentsRow) bool { return r.Name == d.Name }); i >= 0 {
		_, err := q.UpdateRepositoryAgent(ctx, queries.UpdateRepositoryAgentParams{Description: d.Description, Instructions: d.Instructions,
			Image: d.Image, Provider: optional(d.Provider), Model: optional(d.Model), Permissions: d.Permissions, Secrets: secrets,
			Line: int32(d.Line), ID: current[i].PrincipalID, RepoID: &push.RepoID}) //nolint:gosec // a line of a settings file
		if err != nil {
			return fmt.Errorf("updating agent %s: %w", d.Name, err)
		}
		return nil
	}
	id, err := uuid.NewV7()
	if err != nil {
		return fmt.Errorf("making agent %s's ID: %w", d.Name, err)
	}
	var creator *uuid.UUID
	if push.PusherID != uuid.Nil {
		creator = &push.PusherID
	}
	if err := q.InsertAgentPrincipal(ctx, queries.InsertAgentPrincipalParams{ID: id, Name: d.Name, OrgID: &orgID, CreatedBy: creator}); err != nil {
		return fmt.Errorf("inserting agent %s's principal: %w", d.Name, err)
	}
	if err := q.InsertRepositoryAgent(ctx, queries.InsertRepositoryAgentParams{ID: id, OrgID: orgID, RepoID: &push.RepoID, Name: d.Name,
		Description: d.Description, Instructions: d.Instructions, Image: d.Image, Provider: optional(d.Provider), Model: optional(d.Model),
		Permissions: d.Permissions, Secrets: secrets, Line: int32(d.Line), CreatedBy: creator}); err != nil { //nolint:gosec // a line of a settings file
		return fmt.Errorf("inserting agent %s: %w", d.Name, err)
	}
	return orgEvent(ctx, q, &orgID, push.PusherID, "agent.created", id, map[string]any{"name": d.Name, "repository": push.RepoID})
}

// retireAgents retires the repository's agents main's file no longer declares, those but names, and cancels their
// active tasks.
func retireAgents(ctx context.Context, q *queries.Queries, orgID uuid.UUID, push git.Push, names []string) error {
	retired, err := q.RetireRepositoryAgents(ctx, &push.RepoID, names)
	if err != nil {
		return fmt.Errorf("retiring the agents %s's file no longer declares: %w", git.DefaultBranch, err)
	}
	for _, id := range retired {
		if err := orgEvent(ctx, q, &orgID, push.PusherID, "agent.retired", id, map[string]any{"repository": push.RepoID}); err != nil {
			return err
		}
	}
	if len(retired) == 0 {
		return nil
	}
	cancelled, err := q.CancelAgentTasks(ctx, reasonRetired, retired)
	if err != nil {
		return fmt.Errorf("cancelling the retired agents' tasks: %w", err)
	}
	for _, t := range cancelled {
		if err := orgEvent(ctx, q, &orgID, push.PusherID, "task.cancelled", t.ID, map[string]any{"number": t.Number}); err != nil {
			return err
		}
	}
	return nil
}
