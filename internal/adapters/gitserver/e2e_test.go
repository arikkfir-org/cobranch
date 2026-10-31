package gitserver_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	gocloudblob "gocloud.dev/blob"

	"cobranch.dev/db/migrations"
	"cobranch.dev/db/seeds"
	"cobranch.dev/internal/adapters/blob"
	"cobranch.dev/internal/adapters/gitserver"
	"cobranch.dev/internal/adapters/postgres"
	"cobranch.dev/internal/adapters/postgres/postgrestest"
	"cobranch.dev/internal/adapters/pushworker"
	"cobranch.dev/internal/services/auth"
	"cobranch.dev/internal/services/git"
	"cobranch.dev/internal/services/repos"
)

// hookEnv marks the test binary's run as a push's hook.
const hookEnv = "COBRANCH_TEST_GIT_HOOK"

// TestMain runs the test binary as a push's pre-receive hook when the Git server's hook script runs it, as the
// cobranch binary runs as git-hook.
func TestMain(m *testing.M) {
	if os.Getenv(hookEnv) == "1" {
		os.Exit(gitserver.RunHook(context.Background(), gitserver.HookEnv{
			URL: os.Getenv(gitserver.HookURLEnv), Token: os.Getenv(gitserver.HookTokenEnv), Quarantine: os.Getenv("GIT_QUARANTINE_PATH"),
			ObjectDirectory: os.Getenv("GIT_OBJECT_DIRECTORY"), Alternates: os.Getenv("GIT_ALTERNATE_OBJECT_DIRECTORIES"),
		}, os.Stdin, os.Stderr))
	}
	os.Exit(m.Run())
}

// The seeds' principals and repositories the tests use: Olivia maintains Acme's website, Gwen reads it, and Max alone
// reaches Globex's ledger.
var (
	olivia  = uuid.MustParse("00000000-0000-7000-8000-00000000d001")
	gwen    = uuid.MustParse("00000000-0000-7000-8000-00000000d003")
	website = uuid.MustParse("00000000-0000-7000-8000-00000000d201")
)

// stack is a Git server and a push worker in process over a database with the seeds and a file:// bucket.
type stack struct {
	t      *testing.T
	pool   *pgxpool.Pool
	bucket *gocloudblob.Bucket
	base   string
	// record, when set, stands between the Git server and the push worker.
	record func(ctx context.Context, push git.Push, next gitserver.Recorder) (git.Answer, error)
	worker gitserver.Recorder
	// workerURL is the push worker's, which a test may put a proxy in front of.
	workerURL string
}

func newStack(t *testing.T) *stack {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("these tests drive the git CLI, which isn't installed")
	}
	ctx := context.Background()
	pool, err := postgres.Connect(ctx, postgrestest.Database(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	migrate(t, pool)
	bucket, err := blob.Open(ctx, "file://"+t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = bucket.Close() })
	s := &stack{t: t, pool: pool, bucket: bucket}
	worker := httptest.NewServer(pushworker.Handler("internal", git.NewService(postgres.NewGitStore(pool))))
	t.Cleanup(worker.Close)
	s.worker, s.workerURL = pushworker.NewClient(worker.URL, "internal"), worker.URL
	s.base = s.serve(t.TempDir())
	return s
}

// migrate brings pool's database to the schema and adds the seeds.
func migrate(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	schema, err := postgres.NewSchema(pool, migrations.FS, migrations.Go...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = schema.Close() })
	demo, err := postgres.NewSeeds(pool, seeds.Go...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = demo.Close() })
	if err := schema.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := demo.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
}

// serve starts a Git server with its own cache in dataDir, and returns its URL.
func (s *stack) serve(dataDir string) string {
	s.t.Helper()
	self, err := os.Executable()
	if err != nil {
		s.t.Fatal(err)
	}
	orgStore := postgres.NewOrgStore(s.pool)
	srv, err := gitserver.New(gitserver.Config{DataDir: dataDir, CacheSize: 1 << 30, MaxPushSize: 1 << 30, Hook: []string{"env", hookEnv + "=1", self}},
		auth.NewService(postgres.NewAuthStore(s.pool), nil, auth.Settings{}), repos.NewService(postgres.NewRepoStore(s.pool), auth.NewAuthorizer(orgStore)),
		git.NewService(postgres.NewGitStore(s.pool)), recorderFunc(s.recordPush), s.bucket)
	if err != nil {
		s.t.Fatal(err)
	}
	s.t.Cleanup(func() { _ = srv.Close() })
	server := httptest.NewServer(srv.Handler())
	s.t.Cleanup(server.Close)
	return server.URL
}

func (s *stack) recordPush(ctx context.Context, push git.Push) (git.Answer, error) {
	if s.record != nil {
		return s.record(ctx, push, s.worker)
	}
	return s.worker.Record(ctx, push)
}

type recorderFunc func(ctx context.Context, push git.Push) (git.Answer, error)

func (f recorderFunc) Record(ctx context.Context, push git.Push) (git.Answer, error) {
	return f(ctx, push)
}

// token makes a personal token of user's holding permissions, and returns its secret.
func (s *stack) token(user uuid.UUID, permissions ...string) string {
	s.t.Helper()
	if len(permissions) == 0 {
		permissions = auth.DefaultTokenPermissions
	}
	secret := auth.NewSecret(auth.PrefixPersonalToken)
	now := time.Now()
	if err := postgres.NewAuthStore(s.pool).CreateToken(context.Background(), auth.Credential{
		ID: uuid.Must(uuid.NewV7()), Kind: auth.CredentialPersonalToken, PrincipalID: user, Hint: auth.Hint(secret), CreatedAt: now, CreatedBy: user,
		ExpiresAt: now.Add(time.Hour), Name: "test", Permissions: permissions,
	}, auth.Hash(secret)); err != nil {
		s.t.Fatal(err)
	}
	return secret
}

// url is the Git URL of path on the stack's server, with secret as the password.
func (s *stack) url(base, secret, path string) string {
	u, err := url.Parse(base + "/" + path)
	if err != nil {
		s.t.Fatal(err)
	}
	if secret != "" {
		u.User = url.UserPassword("git", secret)
	}
	return u.String()
}

// gitCmd runs the git CLI in dir as a client would, isolated from the machine's configuration, and returns its
// combined output and error.
func gitCmd(t *testing.T, dir string, args ...string) (string, error) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "HOME="+dir, "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_TERMINAL_PROMPT=0",
		"GIT_AUTHOR_NAME=Ada Author", "GIT_AUTHOR_EMAIL=Ada@Example.com", "GIT_COMMITTER_NAME=Olivia Owner", "GIT_COMMITTER_EMAIL=olivia@cobranch.example",
		"GIT_AUTHOR_DATE=2026-10-31T12:00:00+02:00", "GIT_COMMITTER_DATE=2026-10-31T12:30:00+02:00")
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// mustGit runs gitCmd and fails the test when git fails.
func mustGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := gitCmd(t, dir, args...)
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return out
}

// clone clones repoURL into a new directory and returns it.
func clone(t *testing.T, repoURL string) string {
	t.Helper()
	parent := t.TempDir()
	mustGit(t, parent, "clone", "-q", repoURL, "repo")
	dir := filepath.Join(parent, "repo")
	mustGit(t, dir, "config", "user.useConfigOnly", "false")
	return dir
}

// commit writes file with content in dir and commits it.
func commit(t *testing.T, dir, file, content string) string {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, file), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	mustGit(t, dir, "add", file)
	mustGit(t, dir, "commit", "-q", "-m", "Add "+file)
	return strings.TrimSpace(mustGit(t, dir, "rev-parse", "HEAD"))
}

// query returns one value a query of the stack's database reads.
func query[T any](t *testing.T, pool *pgxpool.Pool, sql string, args ...any) T {
	t.Helper()
	var v T
	if err := pool.QueryRow(context.Background(), sql, args...).Scan(&v); err != nil {
		t.Fatalf("%s: %v", sql, err)
	}
	return v
}

func TestStatusCodes(t *testing.T) {
	s := newStack(t)
	reader, narrow := s.token(gwen), s.token(olivia, "repo.read")
	tests := []struct {
		name, secret, path string
		bearer             bool
		want               int
	}{
		{name: "no credential", path: "acme/website.git/info/refs?service=git-upload-pack", want: http.StatusUnauthorized},
		{name: "a credential that acts as nobody", secret: auth.NewSecret(auth.PrefixPersonalToken), path: "acme/website.git/info/refs?service=git-upload-pack", want: http.StatusUnauthorized},
		{name: "a session's token", secret: auth.NewSecret(auth.PrefixSession), path: "acme/website.git/info/refs?service=git-upload-pack", want: http.StatusUnauthorized},
		{name: "a reader fetches", secret: reader, path: "acme/website.git/info/refs?service=git-upload-pack", want: http.StatusOK},
		{name: "as a bearer token", secret: reader, bearer: true, path: "acme/website/info/refs?service=git-upload-pack", want: http.StatusOK},
		{name: "a reader pushes", secret: reader, path: "acme/website.git/info/refs?service=git-receive-pack", want: http.StatusForbidden},
		{name: "a token that only reads pushes", secret: narrow, path: "acme/website.git/info/refs?service=git-receive-pack", want: http.StatusForbidden},
		{name: "a repository the token can't read", secret: reader, path: "globex/ledger.git/info/refs?service=git-upload-pack", want: http.StatusNotFound},
		{name: "a repository that isn't there", secret: reader, path: "acme/nothing.git/info/refs?service=git-upload-pack", want: http.StatusNotFound},
		{name: "the dumb protocol", secret: reader, path: "acme/website.git/info/refs", want: http.StatusForbidden},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, err := http.NewRequest(http.MethodGet, s.base+"/"+tt.path, nil)
			if err != nil {
				t.Fatal(err)
			}
			switch {
			case tt.bearer:
				req.Header.Set("Authorization", "Bearer "+tt.secret)
			case tt.secret != "":
				req.SetBasicAuth("anyone", tt.secret)
			}
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			_ = resp.Body.Close()
			if resp.StatusCode != tt.want {
				t.Errorf("status = %d, want %d", resp.StatusCode, tt.want)
			}
			if tt.want == http.StatusUnauthorized && !strings.HasPrefix(resp.Header.Get("WWW-Authenticate"), "Basic ") {
				t.Errorf("WWW-Authenticate = %q, want a Basic challenge", resp.Header.Get("WWW-Authenticate"))
			}
		})
	}
}
