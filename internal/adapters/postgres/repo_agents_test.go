package postgres

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"

	"cobranch.dev/internal/services/agents"
	"cobranch.dev/internal/services/auth"
	"cobranch.dev/internal/services/git"
)

// checkerFile declares website's checker, on Acme's opus in an image of its own.
const checkerFile = "agents:\n  checker:\n    image: python:3.14.0\n    model: anthropic/opus\n    permissions: [repo.read, annotations.write]\n"

// agentsFile declares checker and website's writer, on Acme's default model.
const agentsFile = checkerFile + "  writer:\n    description: Writes\n"

// repoAgents lists website's agents as "name repository image provider/model permissions retired", by name.
func (f *agentFixture) repoAgents(t *testing.T) []string {
	t.Helper()
	list, err := f.a.Agents(context.Background(), f.acme.ID)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, a := range list {
		if !a.Declared() {
			continue
		}
		line := fmt.Sprintf("%s %s %s %s/%s %s line %d", a.Name, a.RepoSlug, a.Image, a.Provider, a.Model, strings.Join(a.Permissions, ","), a.Line)
		if a.RetiredAt != nil {
			line += " retired"
		}
		out = append(out, line)
	}
	return out
}

// mainTo is Olivia's push of main from old to sha with file at main's tip; none without file.
func (f *agentFixture) mainTo(t *testing.T, old, sha, file string) git.Push {
	t.Helper()
	p := pushOf(f.website.ID, f.olivia, "", []git.RefUpdate{{Name: "refs/heads/main", Old: old, New: sha}})
	p.Settings = []git.TipSettings{{Branch: "main"}}
	if file != "" {
		p.Settings = []git.TipSettings{tip(t, "main", "blob-"+sha[:1]+fmt.Sprint(len(file)), file)}
	}
	return p
}

// addModels gives Acme Anthropic's opus and haiku, its default.
func (f *agentFixture) addModels(t *testing.T) {
	t.Helper()
	ctx := context.Background()
	key := f.addSecret(t, uuid.Nil, "anthropic-key")
	if _, _, err := f.a.SetModelProvider(ctx, f.olivia, f.acme.ID, "anthropic", key, agents.NoVersion); err != nil {
		t.Fatal(err)
	}
	for _, m := range []struct {
		name string
		def  bool
	}{{"opus", false}, {"haiku", true}} {
		if _, _, err := f.a.PutModel(ctx, f.olivia, f.acme.ID, "anthropic", m.name, agents.ModelInput{Default: m.def}, agents.NoVersion); err != nil {
			t.Fatal(err)
		}
	}
}

// writerID is the ID of website's writer.
func (f *agentFixture) writerID(t *testing.T) uuid.UUID {
	t.Helper()
	list, err := f.a.Agents(context.Background(), f.acme.ID)
	if err != nil {
		t.Fatal(err)
	}
	return list[slices.IndexFunc(list, func(a agents.Agent) bool { return a.Name == "writer" && a.Declared() })].ID
}

func TestGitStoreSyncsRepositoryAgents(t *testing.T) {
	t.Parallel()
	f := newAgentFixture(t)
	ctx := context.Background()
	f.addModels(t)
	both := []string{"checker website python:3.14.0 anthropic/opus annotations.write,repo.read line 2",
		"writer website  anthropic/haiku messages.post,repo.read line 6"}
	retired := []string{both[0], "writer website  / messages.post,repo.read line 9 retired"}
	steps := []struct {
		name    string
		push    git.Push
		refused string
		want    []string
	}{
		{name: "main declares two agents", push: f.mainTo(t, f.shaD, shaB, agentsFile), want: both},
		{name: "a branch's file changes nothing", want: both,
			push: func() git.Push {
				p := pushOf(f.website.ID, f.olivia, "", []git.RefUpdate{{Name: "refs/heads/feature", Old: shaC, New: shaA}})
				p.Settings = []git.TipSettings{tip(t, "feature", "blob-f", "agents:\n  rogue:\n    permissions: [repo.push]\n")}
				return p
			}()},
		{name: "a permission beyond the ceiling", push: f.mainTo(t, shaB, f.shaD, strings.Replace(agentsFile, "annotations.write", "repo.push", 1)),
			refused: "/cobranch.yaml: line 2: agent checker lists repo.push, beyond the ceiling, Automation", want: both},
		{name: "a higher ceiling", want: []string{"checker website python:3.14.0 anthropic/opus repo.push,repo.read line 5", "writer website  anthropic/haiku messages.post,repo.read line 9"},
			push: f.mainTo(t, shaB, f.shaD, "repository:\n  jobs:\n    ceiling: Developer\n"+strings.Replace(agentsFile, "annotations.write", "repo.push", 1))},
		{name: "a ceiling that names no role", push: f.mainTo(t, f.shaD, shaB, "repository:\n  jobs:\n    ceiling: Nobody\n"+agentsFile),
			refused: "/cobranch.yaml: line 5: repository.jobs.ceiling names no repository role, Nobody, which the agents stay within"},
		{name: "a model Acme doesn't have", push: f.mainTo(t, f.shaD, shaB, strings.Replace(agentsFile, "anthropic/opus", "anthropic/mythos", 1)),
			refused: "/cobranch.yaml: line 2: agent checker runs on anthropic/mythos, which isn't one of the organization's models"},
		{name: "main drops writer", push: f.mainTo(t, f.shaD, shaB, checkerFile), want: retired},
		{name: "main declares writer again", push: f.mainTo(t, shaB, f.shaD, agentsFile), want: both},
		{name: "main drops the file", push: f.mainTo(t, f.shaD, shaB, ""),
			want: []string{"checker website python:3.14.0 / annotations.write,repo.read line 2 retired", "writer website  / messages.post,repo.read line 6 retired"}},
	}
	var writer uuid.UUID
	for _, s := range steps {
		err := f.g.Record(ctx, s.push)
		var refused *git.RefusedError
		switch {
		case s.refused != "":
			if !errors.As(err, &refused) || refused.Message != s.refused {
				t.Fatalf("%s: Record() = %v, want refused: %s", s.name, err, s.refused)
			}
			continue
		case err != nil:
			t.Fatalf("%s: Record() = %v", s.name, err)
		}
		if got := f.repoAgents(t); !slices.Equal(got, s.want) {
			t.Errorf("%s: agents = %q, want %q", s.name, got, s.want)
		}
		if id := f.writerID(t); writer == uuid.Nil {
			writer = id
		} else if id != writer {
			t.Errorf("%s: writer is %s, want the same principal, %s", s.name, id, writer)
		}
	}
	// Retired, checker no longer holds opus in use.
	if err := f.a.RemoveModel(ctx, f.olivia, f.acme.ID, "anthropic", "opus", agents.AnyVersion); err != nil {
		t.Errorf("RemoveModel(opus) once its agent is retired = %v, want nil", err)
	}
}

// TestRepositoryAgentPermissions checks what a repository's agent holds: what it declares, on its repository alone,
// until it is retired, and Guest at its organization; and that retiring it cancels its tasks.
func TestRepositoryAgentPermissions(t *testing.T) {
	t.Parallel()
	f := newAgentFixture(t)
	ctx := context.Background()
	if err := f.g.Record(ctx, f.mainTo(t, f.shaD, shaB, "agents:\n  checker:\n    permissions: [repo.read, annotations.write]\n")); err != nil {
		t.Fatal(err)
	}
	list, err := f.a.Agents(ctx, f.acme.ID)
	if err != nil {
		t.Fatal(err)
	}
	checker := list[slices.IndexFunc(list, agents.Agent.Declared)]
	task, err := f.a.Assign(ctx, f.olivia, agents.Task{ID: v7(), OrgID: f.acme.ID, Agent: agents.Principal{ID: checker.ID}, Kind: agents.KindAssigned,
		AssignedBy: &agents.Principal{ID: f.olivia}, Title: "Check", RepoID: f.website.ID})
	if err != nil {
		t.Fatal(err)
	}
	tests := []heldCase{
		{name: "declared", website: "annotations.write,repo.read", acme: "org.read", readable: fmt.Sprint([]uuid.UUID{f.website.ID}),
			state: "queued"},
		{name: "retired", push: f.mainTo(t, shaB, f.shaD, "agents:\n"), acme: "org.read", readable: "[]", state: "cancelled", reason: reasonRetired},
	}
	for _, tt := range tests {
		if tt.push.ID != uuid.Nil {
			if err := f.g.Record(ctx, tt.push); err != nil {
				t.Fatal(err)
			}
		}
		f.checkHeld(t, checker.ID, task.Number, tt)
	}
}

// heldCase is what a repository's agent holds after a push: on website, on secret and at Acme, the repositories it
// reads, and the state of its task and why.
type heldCase struct {
	name                    string
	push                    git.Push
	website, secret, acme   string
	readable, state, reason string
}

// checkHeld checks that agent holds what tt says, and that its task number stands as tt says.
func (f *agentFixture) checkHeld(t *testing.T, agent uuid.UUID, number int64, tt heldCase) {
	t.Helper()
	ctx := context.Background()
	holds := func(scope auth.Scope, id uuid.UUID) string {
		got, err := f.orgFixture.s.Permissions(ctx, agent, scope, id)
		if err != nil {
			t.Fatal(err)
		}
		return strings.Join(got, ",")
	}
	for _, c := range []struct {
		where     string
		got, want string
	}{{"on website", holds(auth.ScopeRepository, f.website.ID), tt.website}, {"on secret", holds(auth.ScopeRepository, f.secret.ID), tt.secret},
		{"at Acme", holds(auth.ScopeOrganization, f.acme.ID), tt.acme}} {
		if c.got != c.want {
			t.Errorf("%s: %s = %q, want %q", tt.name, c.where, c.got, c.want)
		}
	}
	readable, err := f.orgFixture.s.Repositories(ctx, agent, f.acme.ID, "repo.read")
	if err != nil {
		t.Fatal(err)
	}
	if got := fmt.Sprint(readable); got != tt.readable {
		t.Errorf("%s: reads %s, want %s", tt.name, got, tt.readable)
	}
	got, err := f.a.Task(ctx, f.acme.ID, number)
	if err != nil {
		t.Fatal(err)
	}
	if string(got.State) != tt.state || got.Reason != tt.reason {
		t.Errorf("%s: task %s (%s), want %s (%s)", tt.name, got.State, got.Reason, tt.state, tt.reason)
	}
}
