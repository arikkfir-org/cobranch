package postgres

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"cobranch.dev/internal/services/agents"
	"cobranch.dev/internal/services/auth"
	"cobranch.dev/internal/services/issues"
	"cobranch.dev/internal/services/orgs"
	"cobranch.dev/internal/services/repos"
	"cobranch.dev/internal/services/secrets"
)

func TestChangesHub(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		// change changes what Settings · Models shows of Acme through the stores, key being the secret that holds
		// anthropic's key.
		change func(ctx context.Context, t *testing.T, f *agentFixture, key uuid.UUID)
	}{
		{name: "a provider connected", change: func(ctx context.Context, t *testing.T, f *agentFixture, key uuid.UUID) {
			if _, _, err := f.a.SetModelProvider(ctx, f.olivia, f.acme.ID, "openai", key, agents.NoVersion); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "a model added", change: func(ctx context.Context, t *testing.T, f *agentFixture, _ uuid.UUID) {
			if _, _, err := f.a.PutModel(ctx, f.olivia, f.acme.ID, "anthropic", "b-model", agents.ModelInput{}, agents.NoVersion); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "an agent put on a model", change: func(ctx context.Context, t *testing.T, f *agentFixture, _ uuid.UUID) {
			if _, err := f.a.SetModel(ctx, f.olivia, f.acme.ID, f.scout.ID, f.scout.Version, "anthropic", "a-model"); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "the provider's secret deleted", change: func(ctx context.Context, t *testing.T, f *agentFixture, key uuid.UUID) {
			if err := NewSecretStore(f.pool).Delete(ctx, f.olivia, key, secrets.AnyVersion); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "the provider removed", change: func(ctx context.Context, t *testing.T, f *agentFixture, _ uuid.UUID) {
			if err := f.a.RemoveModelProvider(ctx, f.olivia, f.acme.ID, "anthropic", agents.AnyVersion); err != nil {
				t.Fatal(err)
			}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			hearChangeOf(t, tt.change)
		})
	}
}

// hearChangeOf connects anthropic to Acme with a secret of its own and adds a-model, watches Acme and another
// organization on a hub of the fixture's database, makes change, and checks that it wakes Acme's watcher alone.
func hearChangeOf(t *testing.T, change func(ctx context.Context, t *testing.T, f *agentFixture, key uuid.UUID)) {
	t.Helper()
	f := newAgentFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	key := f.addSecret(t, uuid.Nil, "anthropic-key")
	if _, _, err := f.a.SetModelProvider(ctx, f.olivia, f.acme.ID, "anthropic", key, agents.NoVersion); err != nil {
		t.Fatal(err)
	}
	if _, _, err := f.a.PutModel(ctx, f.olivia, f.acme.ID, "anthropic", "a-model", agents.ModelInput{}, agents.NoVersion); err != nil {
		t.Fatal(err)
	}
	hub := ListenChanges(ctx, f.pool)
	acme, stopAcme := hub.Watch(f.acme.ID)
	defer stopAcme()
	other, stopOther := hub.Watch(uuid.New())
	defer stopOther()
	// Every watcher wakes once the hub starts to listen.
	for _, wake := range []<-chan struct{}{acme, other} {
		select {
		case <-wake:
		case <-time.After(10 * time.Second):
			t.Fatal("no wake once the hub listened")
		}
	}
	change(ctx, t, f, key)
	select {
	case <-acme:
	case <-time.After(10 * time.Second):
		t.Fatal("the change didn't wake the organization's watcher")
	}
	select {
	case <-other:
		t.Error("a change of Acme's woke another organization's watcher")
	case <-time.After(200 * time.Millisecond):
	}
}

// settingsChange changes what Acme's Settings show through the stores, and answers what it should wake: Acme, and a
// repository it names.
type settingsChange struct {
	name   string
	change func(ctx context.Context, t *testing.T, f *settingsFixture) []uuid.UUID
}

// settingsChanges are a change to each table that Settings' lists show.
func settingsChanges() []settingsChange {
	return []settingsChange{
		{name: "someone invited", change: func(ctx context.Context, t *testing.T, f *settingsFixture) []uuid.UUID {
			inv := orgs.Invitation{ID: uuid.New(), OrgID: f.acme.ID, Email: "nia@example.com", RoleID: auth.RoleGuest, InvitedBy: f.olivia,
				State: orgs.StatePending, CreatedAt: now, ExpiresAt: now.Add(time.Hour)}
			must(t, f.s.Invite(ctx, f.olivia, inv))
			return []uuid.UUID{f.acme.ID}
		}},
		{name: "someone asks to join", change: func(ctx context.Context, t *testing.T, f *settingsFixture) []uuid.UUID {
			req := orgs.JoinRequest{ID: uuid.New(), OrgID: f.acme.ID, UserID: f.nia, CreatedAt: now, ExpiresAt: now.Add(time.Hour)}
			must(t, f.s.Request(ctx, f.nia, req))
			return []uuid.UUID{f.acme.ID}
		}},
		{name: "a member leaves", change: func(ctx context.Context, t *testing.T, f *settingsFixture) []uuid.UUID {
			must(t, f.s.Leave(ctx, f.max, f.acme.ID, f.max, now))
			return []uuid.UUID{f.acme.ID}
		}},
		{name: "a role granted", change: func(ctx context.Context, t *testing.T, f *settingsFixture) []uuid.UUID {
			must(t, f.s.GrantRole(ctx, f.olivia, f.acme.ID, f.max, auth.RoleOwner))
			return []uuid.UUID{f.acme.ID}
		}},
		{name: "a custom role created", change: func(ctx context.Context, t *testing.T, f *settingsFixture) []uuid.UUID {
			release := orgs.Role{ID: uuid.New(), Name: "Release manager", Permissions: []string{"org.read"}, OrgID: f.acme.ID}
			must(t, f.s.CreateRole(ctx, f.olivia, release))
			return []uuid.UUID{f.acme.ID}
		}},
		{name: "an API key made", change: func(ctx context.Context, t *testing.T, f *settingsFixture) []uuid.UUID {
			token := auth.NewSecret(auth.PrefixAPIKey)
			key := orgs.APIKey{ID: uuid.New(), OrgID: f.acme.ID, Name: "Release", CreatedBy: f.olivia, CreatedAt: now, Roles: []orgs.Role{{ID: auth.RoleMember}}}
			must(t, f.s.CreateAPIKey(ctx, f.olivia, key, orgs.Secret{ID: uuid.New(), Hint: auth.Hint(token), CreatedAt: now}, auth.Hash(token)))
			return []uuid.UUID{f.acme.ID}
		}},
		{name: "an API key used", change: func(ctx context.Context, t *testing.T, f *settingsFixture) []uuid.UUID {
			auths := NewAuthStore(f.pool)
			cred, _, err := auths.Credential(ctx, f.hash)
			must(t, err)
			must(t, auths.TouchCredential(ctx, cred.ID, cred.Kind, now.Add(time.Minute)))
			return []uuid.UUID{f.acme.ID}
		}},
		{name: "an API key deleted", change: func(ctx context.Context, t *testing.T, f *settingsFixture) []uuid.UUID {
			must(t, f.s.DeleteAPIKey(ctx, f.olivia, f.acme.ID, f.key.ID))
			return []uuid.UUID{f.acme.ID}
		}},
		{name: "access to a repository granted", change: func(ctx context.Context, t *testing.T, f *settingsFixture) []uuid.UUID {
			must(t, f.r.Grant(ctx, f.olivia, f.website, f.max, auth.RoleDeveloper))
			return []uuid.UUID{f.acme.ID, f.website.ID}
		}},
		{name: "access to a secret granted", change: func(ctx context.Context, t *testing.T, f *settingsFixture) []uuid.UUID {
			must(t, NewSecretStore(f.pool).Grant(ctx, f.olivia, f.npm, f.max))
			return []uuid.UUID{f.acme.ID}
		}},
	}
}

// pageChanges are changes to what an organization's live pages show, which wake its watchers.
func pageChanges() []settingsChange {
	return []settingsChange{
		{name: "a repository created", change: func(ctx context.Context, t *testing.T, f *settingsFixture) []uuid.UUID {
			docs := repos.Repository{ID: uuid.New(), OrgID: f.acme.ID, Slug: "docs", Name: "x", ObjectFormat: "sha1", CreatedBy: f.olivia, CreatedAt: now}
			must(t, f.r.Create(ctx, f.olivia, docs, 0))
			return []uuid.UUID{f.acme.ID}
		}},
		{name: "a repository's Git data grown", change: func(ctx context.Context, t *testing.T, f *settingsFixture) []uuid.UUID {
			_, err := f.pool.Exec(ctx, "INSERT INTO packs (repo_id, org_id, name, upload, size, objects) VALUES ($1, $2, 'pack-1', $3, 10, 1)",
				f.website.ID, f.acme.ID, uuid.New())
			must(t, err)
			return []uuid.UUID{f.acme.ID, f.website.ID}
		}},
		{name: "an issue created", change: func(ctx context.Context, t *testing.T, f *settingsFixture) []uuid.UUID {
			_, err := NewIssueStore(f.pool).Create(ctx, f.olivia, issues.Issue{ID: v7(), OrgID: f.acme.ID, Title: "Safari", State: issues.StateTodo,
				CreatedBy: &issues.Principal{ID: f.olivia}, CreatedAt: now, UpdatedAt: now, Version: 1})
			must(t, err)
			return []uuid.UUID{f.acme.ID}
		}},
	}
}

func TestOrganizationPagesChangesHub(t *testing.T) {
	t.Parallel()
	for _, tt := range pageChanges() {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			hearSettingsChange(t, tt.change)
		})
	}
}

// TestOrganizationPagesNotify checks that each table whose rows the organization's live pages show notifies its
// organization when a row changes, as the tables Settings · Models shows do, and each whose rows a repository's pages
// show its repository too.
func TestOrganizationPagesNotify(t *testing.T) {
	t.Parallel()
	f := newOrgFixture(t)
	rows, err := f.pool.Query(context.Background(), `SELECT c.relname, pg_get_triggerdef(t.oid) FROM pg_trigger t JOIN pg_class c ON c.oid = t.tgrelid
		WHERE t.tgname = 'notify_changes' AND NOT t.tgisinternal`)
	must(t, err)
	defs := map[string]string{}
	for rows.Next() {
		var table, def string
		must(t, rows.Scan(&table, &def))
		defs[table] = def
	}
	must(t, rows.Err())
	const org, repo = "notify_changes('org_id')", "notify_changes('org_id', 'repo_id')"
	for table, want := range map[string]string{"repositories": org, "branches": repo, "annotations": repo, "pushes": repo, "reviews": repo,
		"issues": org, "branch_issues": org, "agent_tasks": org, "task_spend": org, "job_runs": repo, "deliveries": repo, "artifacts": org,
		"packs": repo, "organization_limits": org} {
		def := defs[table]
		if !strings.Contains(def, "AFTER INSERT OR DELETE OR UPDATE") || !strings.Contains(def, want) {
			t.Errorf("table %s's trigger notify_changes = %q; want one after every change, %s", table, def, want)
		}
	}
}

func TestSettingsChangesHub(t *testing.T) {
	t.Parallel()
	for _, tt := range settingsChanges() {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			hearSettingsChange(t, tt.change)
		})
	}
}

// settingsFixture is repoFixture with Acme's API key Deploy, its secret's hash, and Acme's own secret npm.
type settingsFixture struct {
	*repoFixture
	key  orgs.APIKey
	hash []byte
	npm  secrets.Secret
}

// hearSettingsChange makes Acme's API key Deploy and secret npm, watches Acme, its repository website and another
// organization on a hub of the fixture's database, makes change, and checks that it wakes the watchers of what it
// names and no other.
func hearSettingsChange(t *testing.T, change func(ctx context.Context, t *testing.T, f *settingsFixture) []uuid.UUID) {
	t.Helper()
	f := &settingsFixture{repoFixture: newRepoFixture(t)}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	f.key, f.hash = deployKey(t, f.orgFixture)
	var err error
	if f.npm, err = NewSecretStore(f.pool).Create(ctx, f.olivia, secrets.Secret{ID: uuid.New(), OrgID: f.acme.ID, Name: "npm"}, []byte("sealed")); err != nil {
		t.Fatal(err)
	}
	hub := ListenChanges(ctx, f.pool)
	watched := map[uuid.UUID]<-chan struct{}{}
	for _, id := range []uuid.UUID{f.acme.ID, f.website.ID, uuid.New()} {
		wake, stop := hub.Watch(id)
		defer stop()
		watched[id] = wake
	}
	// Every watcher wakes once the hub starts to listen.
	for _, wake := range watched {
		select {
		case <-wake:
		case <-time.After(10 * time.Second):
			t.Fatal("no wake once the hub listened")
		}
	}
	woken := change(ctx, t, f)
	for _, id := range woken {
		select {
		case <-watched[id]:
		case <-time.After(10 * time.Second):
			t.Fatalf("the change didn't wake the watcher of %s", id)
		}
	}
	for id, wake := range watched {
		if slices.Contains(woken, id) {
			continue
		}
		select {
		case <-wake:
			t.Errorf("the change woke the watcher of %s, which it doesn't name", id)
		case <-time.After(200 * time.Millisecond):
		}
	}
}
