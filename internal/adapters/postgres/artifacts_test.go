package postgres

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/google/uuid"

	"cobranch.dev/internal/services/artifacts"
	"cobranch.dev/internal/services/auth"
)

// artifactFixture is Acme's agentFixture with an ArtifactStore and the OrgStore the authorizer reads: Scout, a Member,
// develops website, and Max assigns it tasks.
type artifactFixture struct {
	*agentFixture
	store *ArtifactStore
	orgs  *OrgStore
}

func newArtifactFixture(t *testing.T) *artifactFixture {
	t.Helper()
	f := &artifactFixture{agentFixture: newAgentFixture(t)}
	f.store, f.orgs = NewArtifactStore(f.pool), NewOrgStore(f.pool)
	return f
}

// create writes a Markdown document of website's titled title that actor makes and owns, perhaps in task, which its
// repository's readers reach as far as reach.
func (f *artifactFixture) create(t *testing.T, actor uuid.UUID, title string, reach artifacts.Reach, task uuid.UUID) artifacts.Artifact {
	t.Helper()
	id := v7()
	a := artifacts.Artifact{ID: id, OrgID: f.acme.ID, RepoID: f.website.ID, Title: title, Kind: artifacts.KindDocument, RepositoryAccess: reach,
		OrganizationAccess: artifacts.ReachNone, TaskID: task}
	if task != uuid.Nil {
		a.Agent.ID = actor
	}
	got, err := f.store.Create(context.Background(), actor, artifacts.New{Artifact: a,
		First:  artifacts.VersionInput{ID: id, MediaType: artifacts.MediaMarkdown, Body: "# " + title + "\n", Note: "first"},
		Owners: []artifacts.NewGrant{{PrincipalID: actor, RoleID: auth.RoleArtifactOwner}}})
	if err != nil {
		t.Fatal(err)
	}
	return got
}

// holds fails t, naming what, unless principal holds want on the artifact.
func (f *artifactFixture) holds(t *testing.T, what string, principal, id uuid.UUID, want ...string) {
	t.Helper()
	got, err := f.orgs.Permissions(context.Background(), principal, auth.ScopeArtifact, id)
	if err != nil || !slices.Equal(got, want) {
		t.Errorf("%s: holds %v (%v), want %v", what, got, err, want)
	}
}

// audiences sets the artifact's audiences as Olivia.
func (f *artifactFixture) audiences(t *testing.T, a artifacts.Artifact, repository, organization artifacts.Reach) {
	t.Helper()
	if _, err := f.store.Update(context.Background(), f.olivia, a.ID, artifacts.AnyVersion, artifacts.Changes{Title: a.Title,
		RepositoryAccess: repository, OrganizationAccess: organization}); err != nil {
		t.Fatal(err)
	}
}

func TestArtifactStoreWrites(t *testing.T) {
	t.Parallel()
	f := newArtifactFixture(t)
	plan := f.create(t, f.olivia, "Plan", artifacts.ReachNone, uuid.Nil)
	f.checkCreated(t, plan)
	f.checkChanged(t, plan)
	f.checkVersions(t, plan)
	f.checkDeleted(t, plan)
}

// checkCreated checks what Create made of plan, and that it refuses a taken ID and another organization's repository.
func (f *artifactFixture) checkCreated(t *testing.T, plan artifacts.Artifact) {
	t.Helper()
	ctx := context.Background()
	if plan.Head != 1 || plan.Version != 1 || plan.OrgSlug != f.acme.Slug || plan.RepoSlug != "website" || plan.MediaType != artifacts.MediaMarkdown ||
		plan.CreatedBy.ID != f.olivia || plan.CreatedBy.DisplayName == "" || plan.Agent.ID != uuid.Nil || plan.Branch != "" {
		t.Errorf("Create() = %+v", plan)
	}
	dup := artifacts.New{Artifact: plan, First: artifacts.VersionInput{ID: v7(), MediaType: artifacts.MediaMarkdown, Body: "x"}}
	if _, err := f.store.Create(ctx, f.olivia, dup); !errors.Is(err, artifacts.ErrExists) {
		t.Errorf("Create() of a taken ID = %v, want %v", err, artifacts.ErrExists)
	}
	dup.Artifact.ID, dup.First.ID = v7(), plan.ID
	if _, err := f.store.Create(ctx, f.olivia, dup); !errors.Is(err, artifacts.ErrExists) {
		t.Errorf("Create() of a taken version ID = %v, want %v", err, artifacts.ErrExists)
	}
	dup.Artifact.OrgID, dup.First.ID = uuid.New(), v7()
	if _, err := f.store.Create(ctx, f.olivia, dup); !errors.Is(err, artifacts.ErrNotFound) {
		t.Errorf("Create() in another organization's repository = %v, want %v", err, artifacts.ErrNotFound)
	}
}

// checkChanged has Max retitle plan and open it to the repository's readers, at its first version and then at an old one.
func (f *artifactFixture) checkChanged(t *testing.T, plan artifacts.Artifact) {
	t.Helper()
	ctx := context.Background()
	changed, err := f.store.Update(ctx, f.max, plan.ID, 1, artifacts.Changes{Title: "Roadmap", Summary: "Q1", RepositoryAccess: artifacts.ReachView,
		OrganizationAccess: artifacts.ReachNone})
	if err != nil || changed.Title != "Roadmap" || changed.Summary != "Q1" || changed.Version != 2 || changed.UpdatedBy.ID != f.max ||
		changed.RepositoryAccess != artifacts.ReachView {
		t.Fatalf("Update() = %+v, %v", changed, err)
	}
	if _, err := f.store.Update(ctx, f.max, plan.ID, 1, artifacts.Changes{Title: "x", RepositoryAccess: "none", OrganizationAccess: "none"}); !errors.Is(err, artifacts.ErrChanged) {
		t.Errorf("Update() of an old version = %v, want %v", err, artifacts.ErrChanged)
	}
}

// checkVersions has Max add plan's second version, then reads its versions and the audit log they left.
func (f *artifactFixture) checkVersions(t *testing.T, plan artifacts.Artifact) {
	t.Helper()
	ctx := context.Background()
	second := artifacts.VersionInput{ID: v7(), MediaType: artifacts.MediaHTML, Body: "<p>Roadmap</p>", Note: "as a page"}
	added, err := f.store.AddVersion(ctx, f.max, plan.ID, 2, second, uuid.Nil)
	if err != nil || added.Head != 2 || added.Version != 3 || added.MediaType != artifacts.MediaHTML || added.UpdatedBy.ID != f.max {
		t.Fatalf("AddVersion() = %+v, %v", added, err)
	}
	if _, err := f.store.AddVersion(ctx, f.max, plan.ID, 2, artifacts.VersionInput{ID: v7(), MediaType: artifacts.MediaHTML, Body: "x"}, uuid.Nil); !errors.Is(err, artifacts.ErrChanged) {
		t.Errorf("AddVersion() at an old version = %v, want %v", err, artifacts.ErrChanged)
	}
	if _, err := f.store.AddVersion(ctx, f.max, plan.ID, artifacts.AnyVersion, second, uuid.Nil); !errors.Is(err, artifacts.ErrExists) {
		t.Errorf("AddVersion() of a taken ID = %v, want %v", err, artifacts.ErrExists)
	}
	list, err := f.store.Versions(ctx, plan.ID)
	if err != nil || len(list) != 2 || list[0].Number != 2 || list[0].Size != 14 || list[0].Body != "" || list[0].Author.ID != f.max ||
		list[1].Number != 1 || list[1].Note != "first" {
		t.Errorf("Versions() = %+v, %v", list, err)
	}
	if first, err := f.store.Version(ctx, plan.ID, 1); err != nil || first.Body != "# Plan\n" || first.ID != plan.ID {
		t.Errorf("Version(1) = %+v, %v", first, err)
	}
	if byID, err := f.store.VersionByID(ctx, second.ID); err != nil || byID.Number != 2 || byID.Body != "<p>Roadmap</p>" || byID.ArtifactID != plan.ID {
		t.Errorf("VersionByID() = %+v, %v", byID, err)
	}
	if _, err := f.store.Version(ctx, plan.ID, 3); !errors.Is(err, artifacts.ErrNotFound) {
		t.Errorf("Version(3) = %v, want %v", err, artifacts.ErrNotFound)
	}
	if n := count(t, f.pool, "SELECT count(*) FROM audit_log WHERE table_name = 'artifact_versions' AND (before ? 'body' OR after ? 'body')"); n != 0 {
		t.Errorf("%d audit rows hold a body", n)
	}
	if n := count(t, f.pool, "SELECT count(*) FROM audit_log WHERE table_name IN ('artifacts', 'artifact_versions') AND principal_id = $1", f.max); n != 3 {
		t.Errorf("%d audit rows name Max, want 3", n)
	}
}

// checkDeleted has Olivia delete plan, at an old version and then at its own, and checks that nothing of it remains.
func (f *artifactFixture) checkDeleted(t *testing.T, plan artifacts.Artifact) {
	t.Helper()
	ctx := context.Background()
	if err := f.store.Delete(ctx, f.olivia, plan.ID, 2); !errors.Is(err, artifacts.ErrChanged) {
		t.Errorf("Delete() of an old version = %v, want %v", err, artifacts.ErrChanged)
	}
	if err := f.store.Delete(ctx, f.olivia, plan.ID, 3); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.Get(ctx, plan.ID); !errors.Is(err, artifacts.ErrNotFound) {
		t.Errorf("Get() of a deleted one = %v", err)
	}
	if n := count(t, f.pool, "SELECT count(*) FROM artifact_versions WHERE artifact_id = $1", plan.ID); n != 0 {
		t.Errorf("%d versions of the deleted one remain", n)
	}
	if n := count(t, f.pool, "SELECT count(*) FROM grants WHERE scope = 'artifact' AND scope_id = $1", plan.ID); n != 0 {
		t.Errorf("%d grants on the deleted one remain", n)
	}
	if n := count(t, f.pool, "SELECT count(*) FROM events WHERE kind LIKE 'artifact.%' AND org_id = $1", f.acme.ID); n != 4 {
		t.Errorf("%d artifact events, want 4", n)
	}
}

func TestArtifactStoreTasks(t *testing.T) {
	t.Parallel()
	f := newArtifactFixture(t)
	ctx := context.Background()
	task := f.assigned(t, "Report", true, uuid.Nil)
	a := f.create(t, f.scout.ID, "Findings", artifacts.ReachNone, task.ID)
	if a.Agent.ID != f.scout.ID || a.Agent.DisplayName != "Scout" || a.TaskID != task.ID || a.TaskNumber != task.Number || a.Branch != "feature" {
		t.Errorf("Create() in a task = %+v", a)
	}
	if _, err := f.store.AddVersion(ctx, f.scout.ID, a.ID, artifacts.AnyVersion, artifacts.VersionInput{ID: v7(), MediaType: artifacts.MediaSVG,
		Body: "<svg/>"}, task.ID); err != nil {
		t.Fatal(err)
	}
	list, err := f.store.Versions(ctx, a.ID)
	if err != nil || len(list) != 2 || list[0].TaskNumber != task.Number || list[0].TaskID != task.ID {
		t.Errorf("Versions() = %+v, %v", list, err)
	}
	other := f.assigned(t, "Elsewhere", false, uuid.Nil)
	if b := f.create(t, f.scout.ID, "Notes", artifacts.ReachNone, other.ID); b.Branch != "" || b.TaskNumber != other.Number {
		t.Errorf("Create() in a task on no branch = %+v", b)
	}
	if p, err := f.store.Principal(ctx, f.acme.ID, f.scout.ID); err != nil || p.Kind != auth.KindAgent || p.DisplayName != "Scout" {
		t.Errorf("Principal(Scout) = %+v, %v", p, err)
	}
	if _, err := f.store.Principal(ctx, f.acme.ID, f.nia); !errors.Is(err, artifacts.ErrNotFound) {
		t.Errorf("Principal(a stranger) = %v, want %v", err, artifacts.ErrNotFound)
	}
}

func TestArtifactPermissions(t *testing.T) {
	t.Parallel()
	f := newArtifactFixture(t)
	ctx := context.Background()
	plan := f.create(t, f.olivia, "Plan", artifacts.ReachNone, uuid.Nil)
	all := []string{"artifacts.manage", "artifacts.read", "artifacts.write"}
	f.holds(t, "the Owner", f.olivia, plan.ID, all...)
	f.holds(t, "a Member who reads no repository", f.max, plan.ID)
	f.holds(t, "a Developer of website, Scout", f.scout.ID, plan.ID)

	f.audiences(t, plan, artifacts.ReachView, artifacts.ReachNone)
	f.holds(t, "a Member, when website's readers view it", f.max, plan.ID)
	f.holds(t, "Scout, when website's readers view it", f.scout.ID, plan.ID, "artifacts.read")
	f.audiences(t, plan, artifacts.ReachEdit, artifacts.ReachView)
	f.holds(t, "a Member, when Acme's readers view it", f.max, plan.ID, "artifacts.read")
	f.holds(t, "Scout, when website's readers edit it", f.scout.ID, plan.ID, "artifacts.read", "artifacts.write")
	for permission, want := range map[string][]uuid.UUID{"artifacts.read": {plan.ID}, "artifacts.write": nil} {
		if got, err := f.store.With(ctx, f.max, f.website.ID, permission); err != nil || !slices.Equal(got, want) {
			t.Errorf("With(Max, %s) = %v, %v, want %v", permission, got, err, want)
		}
	}
	if got, err := f.store.With(ctx, f.scout.ID, f.website.ID, "artifacts.write"); err != nil || !slices.Equal(got, []uuid.UUID{plan.ID}) {
		t.Errorf("With(Scout, artifacts.write) = %v, %v", got, err)
	}
	f.audiences(t, plan, artifacts.ReachNone, artifacts.ReachNone)

	if err := f.store.Share(ctx, f.olivia, plan, f.max, auth.RoleArtifactEditor); err != nil {
		t.Fatal(err)
	}
	if err := f.store.Share(ctx, f.olivia, plan, f.max, auth.RoleArtifactViewer); err != nil {
		t.Fatal(err)
	}
	f.holds(t, "a Member shared it as its viewer, in place of its editor", f.max, plan.ID, "artifacts.read")
	grants, err := f.store.Grants(ctx, plan.ID)
	if err != nil || len(grants) != 2 || !slices.ContainsFunc(grants, func(g artifacts.Grant) bool {
		return g.Principal.ID == f.max && g.Role == artifacts.RoleViewer && g.Principal.Kind == auth.KindUser
	}) {
		t.Errorf("Grants() = %+v, %v", grants, err)
	}
	if err := f.store.Unshare(ctx, f.olivia, plan, f.max); err != nil {
		t.Fatal(err)
	}
	if err := f.store.Unshare(ctx, f.olivia, plan, f.max); !errors.Is(err, artifacts.ErrNotFound) {
		t.Errorf("a second Unshare() = %v, want %v", err, artifacts.ErrNotFound)
	}
	f.holds(t, "a Member unshared", f.max, plan.ID)

	held, err := f.orgs.Permissions(ctx, f.scout.ID, auth.ScopeRepository, f.website.ID)
	if err != nil || !slices.Contains(held, "artifacts.create") || slices.Contains(held, "artifacts.manage") {
		t.Errorf("a Developer holds %v on website (%v), want artifacts.create alone of the artifact permissions", held, err)
	}
}

func TestArtifactGrantsGoWithTheMembership(t *testing.T) {
	t.Parallel()
	f := newArtifactFixture(t)
	ctx := context.Background()
	plan := f.create(t, f.olivia, "Plan", artifacts.ReachNone, uuid.Nil)
	if err := f.store.Share(ctx, f.olivia, plan, f.max, auth.RoleArtifactEditor); err != nil {
		t.Fatal(err)
	}
	if err := f.store.Share(ctx, f.olivia, plan, f.scout.ID, auth.RoleArtifactViewer); err != nil {
		t.Fatal(err)
	}
	if err := f.orgs.Leave(ctx, f.olivia, f.acme.ID, f.max, now); err != nil {
		t.Fatal(err)
	}
	if n := count(t, f.pool, "SELECT count(*) FROM grants WHERE scope = 'artifact' AND principal_id = $1", f.max); n != 0 {
		t.Errorf("%d grants on artifacts remain to the member removed", n)
	}
	f.holds(t, "the agent", f.scout.ID, plan.ID, "artifacts.read")
}
