package rest

import (
	"context"
	"maps"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/google/uuid"

	"cobranch.dev/internal/services/agents"
)

// Agents is what the API needs of the agents' service.
type Agents interface {
	Models
	Agents(ctx context.Context, v agents.Viewer, orgID uuid.UUID) ([]agents.Agent, error)
	Agent(ctx context.Context, v agents.Viewer, orgID, id uuid.UUID) (agents.Agent, error)
	CreateAgent(ctx context.Context, v agents.Viewer, orgID uuid.UUID, in agents.Input) (agents.Agent, error)
	UpdateAgent(ctx context.Context, v agents.Viewer, orgID, id uuid.UUID, version int64, c agents.Change) (agents.Agent, error)
	DeleteAgent(ctx context.Context, v agents.Viewer, orgID, id uuid.UUID, version int64) error
	Tasks(ctx context.Context, v agents.Viewer, orgID uuid.UUID, f agents.TaskFilter, limit int) (agents.TaskPage, error)
	Task(ctx context.Context, v agents.Viewer, orgID uuid.UUID, number int64) (agents.Task, error)
	Assign(ctx context.Context, v agents.Viewer, orgID, agentID uuid.UUID, in agents.TaskInput) (agents.Task, error)
	CancelTask(ctx context.Context, v agents.Viewer, orgID uuid.UUID, number, version int64) (agents.Task, error)
	Work(ctx context.Context, v agents.Viewer) (agents.Work, error)
	Transcript(ctx context.Context, v agents.Viewer, orgID uuid.UUID, number int64, after, limit int) ([]agents.Entry, error)
	Append(ctx context.Context, v agents.Viewer, orgID uuid.UUID, number int64, e agents.Entry) (agents.Entry, bool, error)
}

// defaultTaskPage is how many tasks a page holds unless the request names a limit.
const defaultTaskPage = 30

func agentPrincipal(p *agents.Principal) *Principal {
	if p == nil {
		return nil
	}
	return &Principal{Id: p.ID, DisplayName: p.DisplayName}
}

// optionalID is a pointer to id, or nil for uuid.Nil.
func optionalID(id uuid.UUID) *uuid.UUID {
	if id == uuid.Nil {
		return nil
	}
	return &id
}

// orEmpty is *s, or empty for nil.
func orEmpty(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func agent(a agents.Agent) Agent {
	out := Agent{Id: a.ID, Name: a.Name, Description: a.Description, Instructions: a.Instructions, BuiltIn: a.BuiltIn,
		CreatedBy: agentPrincipal(a.CreatedBy), CreatedAt: a.CreatedAt.UTC(), UpdatedAt: a.UpdatedAt.UTC(), Version: a.Version,
		Grants: make([]AgentGrant, 0, len(a.Grants))}
	for _, g := range a.Grants {
		out.Grants = append(out.Grants, AgentGrant{RoleId: g.RoleID, RoleName: g.RoleName, RepositoryId: optionalID(g.RepoID),
			RepositorySlug: optional(g.RepoSlug)})
	}
	if a.Provider != "" {
		out.Provider, out.Model = &a.Provider, &a.Model
	}
	out.Secrets = make(map[string]uuid.UUID, len(a.Secrets))
	maps.Copy(out.Secrets, a.Secrets)
	return out
}

func task(t agents.Task) Task {
	out := Task{Id: t.ID, Number: t.Number, Agent: *agentPrincipal(&t.Agent), Kind: t.Kind, AssignedBy: agentPrincipal(t.AssignedBy),
		Title: t.Title, Instructions: t.Instructions, RepositoryId: optionalID(t.RepoID), RepositorySlug: optional(t.RepoSlug),
		BranchId: optionalID(t.BranchID), Branch: optional(t.Branch), ThreadId: optionalID(t.ThreadID), State: TaskState(t.State),
		Reason: t.Reason, Summary: t.Summary, CancelledBy: agentPrincipal(t.CancelledBy), CreatedAt: t.CreatedAt.UTC(), Version: t.Version}
	if !t.StartedAt.IsZero() {
		at := t.StartedAt.UTC()
		out.StartedAt = &at
	}
	if !t.EndedAt.IsZero() {
		at := t.EndedAt.UTC()
		out.EndedAt = &at
	}
	return out
}

// agentPath is the path of one of the organization's agents from the API's base.
func agentPath(org string, id uuid.UUID) string {
	return BaseURL + "/orgs/" + url.PathEscape(org) + "/agents/" + id.String()
}

// taskPath is the path of one of the organization's tasks from the API's base.
func taskPath(org string, number int64) string {
	return BaseURL + "/orgs/" + url.PathEscape(org) + "/tasks/" + strconv.FormatInt(number, 10)
}

// agentOrg finds the organization the path names for the viewer, and answers both.
func (a api) agentOrg(ctx context.Context, slug string) (agents.Viewer, uuid.UUID, string, error) {
	v, err := viewer(ctx)
	if err != nil {
		return agents.Viewer{}, uuid.Nil, "", err
	}
	org, err := a.org(ctx, v, slug)
	if err != nil {
		return agents.Viewer{}, uuid.Nil, "", err
	}
	return v, org.ID, org.Slug, nil
}

// ListAgents lists the organization's agents by name.
func (a api) ListAgents(ctx context.Context, req ListAgentsRequestObject) (ListAgentsResponseObject, error) {
	v, orgID, _, err := a.agentOrg(ctx, req.Org)
	if err != nil {
		return nil, err
	}
	list, err := a.agents.Agents(ctx, v, orgID)
	if err != nil {
		return nil, fail(err, "organization")
	}
	return ListAgents200JSONResponse{Agents: mapAll(list, agent)}, nil
}

// taggedAgent is a as the API shows it, with its ETag.
func taggedAgent(a agents.Agent) (Agent, string, error) {
	body := agent(a)
	tag, err := versionTag(a.Version, body)
	return body, tag, err
}

// taggedTask is t as the API shows it, with its ETag.
func taggedTask(t agents.Task) (Task, string, error) {
	body := task(t)
	tag, err := versionTag(t.Version, body)
	return body, tag, err
}

// CreateAgent makes an agent of the organization with its first grants.
func (a api) CreateAgent(ctx context.Context, req CreateAgentRequestObject) (CreateAgentResponseObject, error) {
	v, orgID, slug, err := a.agentOrg(ctx, req.Org)
	if err != nil {
		return nil, err
	}
	in := agents.Input{Name: req.Body.Name, Description: orEmpty(req.Body.Description), Instructions: orEmpty(req.Body.Instructions)}
	if req.Body.Grants != nil {
		for _, g := range *req.Body.Grants {
			in.Grants = append(in.Grants, agents.Grant{RoleID: g.RoleId, RepoID: deref(g.RepositoryId)})
		}
	}
	created, err := a.agents.CreateAgent(ctx, v, orgID, in)
	if err != nil {
		return nil, fail(err, "organization")
	}
	body, tag, err := taggedAgent(created)
	if err != nil {
		return nil, err
	}
	return CreateAgent201JSONResponse{Body: body, Headers: CreateAgent201ResponseHeaders{ETag: tag, Location: agentPath(slug, created.ID)}}, nil
}

// GetAgent answers one of the organization's agents.
func (a api) GetAgent(ctx context.Context, req GetAgentRequestObject) (GetAgentResponseObject, error) {
	v, orgID, _, err := a.agentOrg(ctx, req.Org)
	if err != nil {
		return nil, err
	}
	found, err := a.agents.Agent(ctx, v, orgID, req.AgentID)
	if err != nil {
		return nil, fail(err, "agent")
	}
	body, tag, err := taggedAgent(found)
	if err != nil {
		return nil, err
	}
	if matches(req.Params.IfNoneMatch, tag) {
		return GetAgent304Response{Headers: GetAgent304ResponseHeaders{ETag: tag}}, nil
	}
	return GetAgent200JSONResponse{Body: body, Headers: GetAgent200ResponseHeaders{ETag: tag}}, nil
}

// UpdateAgent changes what the body names about an agent and keeps the rest.
func (a api) UpdateAgent(ctx context.Context, req UpdateAgentRequestObject) (UpdateAgentResponseObject, error) {
	v, orgID, _, err := a.agentOrg(ctx, req.Org)
	if err != nil {
		return nil, err
	}
	match, err := versionMatch(req.Params.IfMatch, agents.AnyVersion)
	if err != nil {
		return nil, err
	}
	c := agents.Change{Name: req.Body.Name, Description: req.Body.Description, Instructions: req.Body.Instructions}
	changed, err := a.agents.UpdateAgent(ctx, v, orgID, req.AgentID, match, c)
	if err != nil {
		return nil, fail(err, "agent")
	}
	body, tag, err := taggedAgent(changed)
	if err != nil {
		return nil, err
	}
	return UpdateAgent200JSONResponse{Body: body, Headers: UpdateAgent200ResponseHeaders{ETag: tag}}, nil
}

// DeleteAgent deletes an agent with its grants and tasks.
func (a api) DeleteAgent(ctx context.Context, req DeleteAgentRequestObject) (DeleteAgentResponseObject, error) {
	v, orgID, _, err := a.agentOrg(ctx, req.Org)
	if err != nil {
		return nil, err
	}
	match, err := versionMatch(req.Params.IfMatch, agents.AnyVersion)
	if err != nil {
		return nil, err
	}
	if err := a.agents.DeleteAgent(ctx, v, orgID, req.AgentID, match); err != nil {
		return nil, fail(err, "agent")
	}
	return DeleteAgent204Response{}, nil
}

// taskFilter reads a list's filter, page start and limit from its query.
func taskFilter(p ListTasksParams) (agents.TaskFilter, int, error) {
	f := agents.TaskFilter{AgentID: deref(p.AgentID), RepoID: deref(p.RepositoryID)}
	if p.State != nil {
		for _, s := range *p.State {
			f.States = append(f.States, agents.TaskState(s))
		}
	}
	if p.Cursor != nil {
		before, err := strconv.ParseInt(*p.Cursor, 10, 64)
		if err != nil || before < 1 {
			return f, 0, refuse(http.StatusBadRequest, "cursor.invalid", "Start from the cursor the previous page's Link names.")
		}
		f.Before = before
	}
	limit := defaultTaskPage
	if p.Limit != nil {
		limit = *p.Limit
	}
	return f, limit, nil
}

// ListTasks lists a page of the organization's tasks, and links the next.
func (a api) ListTasks(ctx context.Context, req ListTasksRequestObject) (ListTasksResponseObject, error) {
	v, orgID, _, err := a.agentOrg(ctx, req.Org)
	if err != nil {
		return nil, err
	}
	f, limit, err := taskFilter(req.Params)
	if err != nil {
		return nil, err
	}
	page, err := a.agents.Tasks(ctx, v, orgID, f, limit)
	if err != nil {
		return nil, fail(err, "organization")
	}
	out := ListTasks200JSONResponse{}
	out.Body.Tasks = mapAll(page.Tasks, task)
	if page.Next != 0 {
		next := url.Values{"cursor": {strconv.FormatInt(page.Next, 10)}}
		for name, id := range map[string]uuid.UUID{"agent": f.AgentID, "repositoryId": f.RepoID} {
			if id != uuid.Nil {
				next.Set(name, id.String())
			}
		}
		for _, s := range f.States {
			next.Add("state", string(s))
		}
		if req.Params.Limit != nil {
			next.Set("limit", strconv.Itoa(limit))
		}
		link := `<?` + next.Encode() + `>; rel="next"`
		out.Headers.Link = &link
	}
	return out, nil
}

// AssignTask assigns a task to one of the organization's agents.
func (a api) AssignTask(ctx context.Context, req AssignTaskRequestObject) (AssignTaskResponseObject, error) {
	v, orgID, slug, err := a.agentOrg(ctx, req.Org)
	if err != nil {
		return nil, err
	}
	b := req.Body
	in := agents.TaskInput{Title: b.Title, Instructions: orEmpty(b.Instructions), RepoID: deref(b.RepositoryId), BranchID: deref(b.BranchId),
		Branch: orEmpty(b.Branch), ThreadID: deref(b.ThreadId)}
	t, err := a.agents.Assign(ctx, v, orgID, b.AgentId, in)
	if err != nil {
		return nil, fail(err, "organization")
	}
	body, tag, err := taggedTask(t)
	if err != nil {
		return nil, err
	}
	return AssignTask201JSONResponse{Body: body, Headers: AssignTask201ResponseHeaders{ETag: tag, Location: taskPath(slug, t.Number)}}, nil
}

// GetTask answers one of the organization's tasks.
func (a api) GetTask(ctx context.Context, req GetTaskRequestObject) (GetTaskResponseObject, error) {
	v, orgID, _, err := a.agentOrg(ctx, req.Org)
	if err != nil {
		return nil, err
	}
	t, err := a.agents.Task(ctx, v, orgID, req.Number)
	if err != nil {
		return nil, fail(err, "task")
	}
	body, tag, err := taggedTask(t)
	if err != nil {
		return nil, err
	}
	if matches(req.Params.IfNoneMatch, tag) {
		return GetTask304Response{Headers: GetTask304ResponseHeaders{ETag: tag}}, nil
	}
	return GetTask200JSONResponse{Body: body, Headers: GetTask200ResponseHeaders{ETag: tag}}, nil
}

// CancelTask cancels one of the organization's tasks that hasn't ended.
func (a api) CancelTask(ctx context.Context, req CancelTaskRequestObject) (CancelTaskResponseObject, error) {
	v, orgID, _, err := a.agentOrg(ctx, req.Org)
	if err != nil {
		return nil, err
	}
	match := agents.AnyVersion
	if h := req.Params.IfMatch; h != nil && strings.TrimSpace(*h) != "" {
		if match, err = versionMatch(h, agents.AnyVersion); err != nil {
			return nil, err
		}
	}
	t, err := a.agents.CancelTask(ctx, v, orgID, req.Number, match)
	if err != nil {
		return nil, fail(err, "task")
	}
	body, tag, err := taggedTask(t)
	if err != nil {
		return nil, err
	}
	return CancelTask200JSONResponse{Body: body, Headers: CancelTask200ResponseHeaders{ETag: tag}}, nil
}
