// Package config reads the workloads' configuration from environment variables, the only configuration Cobranch reads.
// The Helm chart renders them.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/kelseyhightower/envconfig"
)

// Prefix starts every variable's name: COBRANCH_HTTP_ADDR, COBRANCH_BLOB_URL and so on.
const Prefix = "COBRANCH"

// URLs are the workloads' public URLs, as the browser sees them: where the Console's pages find each API, and
// together, the hosts the session cookie is set for. A server lets the Console's pages call it from its origin (CORS);
// the Admin server, the Back-office's. An empty URL is a workload that has none.
type URLs struct {
	Console    URL `envconfig:"CONSOLE_URL"`
	REST       URL `envconfig:"REST_URL"`
	GraphQL    URL `envconfig:"GRAPHQL_URL"`
	Auth       URL `envconfig:"AUTH_URL"`
	Admin      URL `envconfig:"ADMIN_URL"`
	BackOffice URL `envconfig:"BACK_OFFICE_URL"`
}

// All lists the URLs that are set.
func (u URLs) All() []*url.URL {
	var all []*url.URL
	for _, each := range []URL{u.Console, u.REST, u.GraphQL, u.Auth, u.Admin, u.BackOffice} {
		if each.URL != nil {
			all = append(all, each.URL)
		}
	}
	return all
}

// URL is an absolute http or https URL read from a variable, or nil when the variable is empty.
type URL struct {
	*url.URL
}

// Decode reads value, an absolute http or https URL, whose host is lowercased and whose trailing slash is dropped. An
// empty value is no URL.
func (u *URL) Decode(value string) error {
	if value == "" {
		u.URL = nil
		return nil
	}
	parsed, err := url.Parse(value)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return fmt.Errorf("%q is not an absolute http or https URL of a host and a path", value)
	}
	parsed.Host = strings.ToLower(parsed.Host)
	parsed.Path = strings.TrimSuffix(parsed.Path, "/")
	parsed.RawPath = ""
	u.URL = parsed
	return nil
}

// Origin is the URL's origin, its scheme and host, or "" without a URL.
func (u URL) Origin() string {
	if u.URL == nil {
		return ""
	}
	return u.Scheme + "://" + u.Host
}

// Server configures the servers that serve an API: rest-server, graphql-server and admin-server.
type Server struct {
	// HTTPAddr is the address the server listens on.
	HTTPAddr string `envconfig:"HTTP_ADDR" default:":8080"`
	// DatabaseURL is PostgreSQL's connection URL. When empty, pgx reads libpq's variables (PGHOST, PGPASSWORD and so
	// on), as the chart sets them for its own PostgreSQL.
	DatabaseURL string `envconfig:"DATABASE_URL"`
	// ShutdownTimeout bounds how long the server waits for requests in flight once asked to stop. It must end before
	// the pod's grace period does.
	ShutdownTimeout time.Duration `envconfig:"SHUTDOWN_TIMEOUT" default:"20s"`
	URLs
	// SignUpSettings are the Admin server's, which shows those the values set as locked.
	SignUpSettings
	// Providers are the values' identity providers, without their secrets: the Admin server's, which shows them.
	Providers Providers `envconfig:"AUTH_PROVIDERS"`
	// SecretsKey seals the secrets the Admin server keeps: the Back-office's providers' client secrets.
	SecretsKey string `envconfig:"SECRETS_KEY"`
	// GitInternalURL is the Git server's internal port, where the REST and GraphQL servers read repositories' trees
	// and files; empty, as in an installation without a Git server, those reads answer 503.
	GitInternalURL URL `envconfig:"GIT_INTERNAL_URL"`
}

// SignUpSettings are the sign-up settings the chart's values give. Each they leave unset is the Back-office's, kept in
// the database.
type SignUpSettings struct {
	// SignUp is the sign-up policy: anyone, domains or invited; empty, the Back-office's.
	SignUp string `envconfig:"AUTH_SIGN_UP"`
	// SignUpDomains are the email domains the domains policy admits.
	SignUpDomains []string `envconfig:"AUTH_SIGN_UP_DOMAINS"`
	// SelfServe grants every new user Installation user, which creates organizations; unset, the Back-office's.
	SelfServe *bool `envconfig:"ORGANIZATIONS_SELF_SERVE"`
}

// LoadServer reads a server's configuration from the environment.
func LoadServer() (Server, error) {
	var c Server
	if err := envconfig.Process(Prefix, &c); err != nil {
		return Server{}, fmt.Errorf("reading the server's configuration from the COBRANCH_ variables: %w", err)
	}
	lower(c.SignUpDomains)
	return c, nil
}

// Provider is an identity provider, as the chart's values describe it in COBRANCH_AUTH_PROVIDERS. Its client secret
// comes from COBRANCH_AUTH_CLIENT_SECRET_<ID>, the ID in capitals with dashes as underscores.
type Provider struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// Issuer is the provider's issuer URL; Preset names a known provider's instead (google).
	Issuer       string `json:"issuer"`
	Preset       string `json:"preset"`
	ClientID     string `json:"clientId"`
	ClientSecret string `json:"-"`
}

// providerID is what a provider's ID may look like: it names the provider in URLs, the database and a variable.
var providerID = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,30}[a-z0-9])?$`)

// Providers is the list of identity providers, read from JSON.
type Providers []Provider

// Decode reads the providers from a JSON list; an empty value is none.
func (p *Providers) Decode(value string) error {
	*p = nil
	if value == "" {
		return nil
	}
	if err := json.Unmarshal([]byte(value), p); err != nil {
		return fmt.Errorf("the providers are not a JSON list: %w", err)
	}
	seen := map[string]bool{}
	for _, each := range *p {
		if !providerID.MatchString(each.ID) {
			return fmt.Errorf("provider ID %q: lowercase letters, digits and dashes, 32 at most", each.ID)
		}
		if seen[each.ID] {
			return fmt.Errorf("provider %q is listed twice", each.ID)
		}
		seen[each.ID] = true
	}
	return nil
}

// SecretVariable names the variable that holds provider id's client secret.
func SecretVariable(id string) string {
	return Prefix + "_AUTH_CLIENT_SECRET_" + strings.ToUpper(strings.ReplaceAll(id, "-", "_"))
}

// AuthServer configures auth-server, which signs people in.
type AuthServer struct {
	HTTPAddr        string        `envconfig:"HTTP_ADDR" default:":8080"`
	DatabaseURL     string        `envconfig:"DATABASE_URL"`
	ShutdownTimeout time.Duration `envconfig:"SHUTDOWN_TIMEOUT" default:"20s"`
	URLs
	// Providers are the identity providers, each with its client secret from its own variable.
	Providers Providers `envconfig:"AUTH_PROVIDERS"`
	SignUpSettings
	// SecretsKey opens the Back-office's providers' client secrets; without one, only the values' providers sign in.
	SecretsKey string `envconfig:"SECRETS_KEY"`
	// Admins are the first administrators' emails.
	Admins []string `envconfig:"AUTH_ADMINS"`
	// Seed lets the demo data's users sign in without a provider: demo installations only.
	Seed bool `envconfig:"SEED"`
}

// LoadAuthServer reads auth-server's configuration from the environment. The Console's and the Auth server's own URL
// are required: it sends browsers to the one and providers send them back to the other.
func LoadAuthServer() (AuthServer, error) {
	var c AuthServer
	if err := envconfig.Process(Prefix, &c); err != nil {
		return AuthServer{}, fmt.Errorf("reading auth-server's configuration from the COBRANCH_ variables: %w", err)
	}
	if c.Console.URL == nil || c.Auth.URL == nil {
		return AuthServer{}, fmt.Errorf("auth-server needs %s_CONSOLE_URL and %s_AUTH_URL", Prefix, Prefix)
	}
	for i, p := range c.Providers {
		secret, ok := os.LookupEnv(SecretVariable(p.ID))
		if !ok || secret == "" {
			return AuthServer{}, fmt.Errorf("provider %q has no client secret: set %s", p.ID, SecretVariable(p.ID))
		}
		c.Providers[i].ClientSecret = secret
	}
	lower(c.SignUpDomains)
	lower(c.Admins)
	return c, nil
}

func lower(values []string) {
	for i, v := range values {
		values[i] = strings.ToLower(strings.TrimSpace(v))
	}
}

// Migrate configures the migrate Job.
type Migrate struct {
	// DatabaseURL is PostgreSQL's connection URL, as the servers'.
	DatabaseURL string `envconfig:"DATABASE_URL"`
	// Seed adds the demo data (db/seeds) after migrating the schema: for demo installations only, never a real one.
	Seed bool `envconfig:"SEED"`
}

// LoadMigrate reads the migrate Job's configuration from the environment.
func LoadMigrate() (Migrate, error) {
	var c Migrate
	if err := envconfig.Process(Prefix, &c); err != nil {
		return Migrate{}, fmt.Errorf("reading the migrate Job's configuration from the COBRANCH_ variables: %w", err)
	}
	return c, nil
}

// Maintenance configures the maintenance CronJob, which deletes what expired and, given CompactAt, compacts the
// repositories' packs and deletes those nobody lists any more.
type Maintenance struct {
	// DatabaseURL is PostgreSQL's connection URL, as the servers'.
	DatabaseURL string `envconfig:"DATABASE_URL"`
	// BlobURL names the object store, as the Git server's.
	BlobURL string `envconfig:"BLOB_URL"`
	// GitDataDir holds the throwaway repositories it compacts in.
	GitDataDir string `envconfig:"GIT_DATA_DIR" default:"/var/lib/cobranch/git"`
	// CompactAt is how many active packs make a repository due for compaction, 2 or more; 0, unset, leaves every pack
	// alone, as in an installation whose Git servers each keep their packs on a disk of their own.
	CompactAt int `envconfig:"GIT_COMPACT_AT"`
}

// LoadMaintenance reads the maintenance CronJob's configuration from the environment.
func LoadMaintenance() (Maintenance, error) {
	var c Maintenance
	if err := envconfig.Process(Prefix, &c); err != nil {
		return Maintenance{}, fmt.Errorf("reading the maintenance job's configuration from the COBRANCH_ variables: %w", err)
	}
	if c.CompactAt != 0 && c.CompactAt < 2 {
		return Maintenance{}, fmt.Errorf("COBRANCH_GIT_COMPACT_AT is %d: compacting takes 2 packs or more", c.CompactAt)
	}
	return c, nil
}

// GitServer configures git-server, which serves Git's smart HTTP protocol.
type GitServer struct {
	HTTPAddr string `envconfig:"HTTP_ADDR" default:":8080"`
	// DatabaseURL is PostgreSQL's connection URL, as the servers'.
	DatabaseURL string `envconfig:"DATABASE_URL"`
	// BlobURL names the object store, which holds the packs: empty or postgres:// for the database's large objects,
	// s3://, gs:// or azblob:// for a cloud's bucket, file:// for a directory.
	BlobURL string `envconfig:"BLOB_URL"`
	// DataDir holds the pack cache and the throwaway repositories, on one filesystem.
	DataDir string `envconfig:"GIT_DATA_DIR" default:"/var/lib/cobranch/git"`
	// CacheSize is how much of the bucket's packs the pod keeps, such as 10Gi.
	CacheSize Size `envconfig:"GIT_CACHE_SIZE" default:"10Gi"`
	// MaxPushSize is the largest pack a push may send, such as 2Gi.
	MaxPushSize Size `envconfig:"GIT_MAX_PUSH_SIZE" default:"2Gi"`
	// SSHAddr is where it also serves Git over SSH, such as :2222; empty, it serves HTTPS alone.
	SSHAddr string `envconfig:"GIT_SSH_ADDR"`
	// SSHHostKey is the private key, in PEM, that SSH clients know the server by; SSHAddr needs it.
	SSHHostKey string `envconfig:"GIT_SSH_HOST_KEY"`
	// InternalAddr is where it serves the internal call, the REST and GraphQL servers' reads of trees and files, on a
	// port no route names.
	InternalAddr    string        `envconfig:"GIT_INTERNAL_ADDR" default:":8081"`
	ShutdownTimeout time.Duration `envconfig:"SHUTDOWN_TIMEOUT" default:"20s"`
}

// LoadGitServer reads git-server's configuration from the environment.
func LoadGitServer() (GitServer, error) {
	var c GitServer
	if err := envconfig.Process(Prefix, &c); err != nil {
		return GitServer{}, fmt.Errorf("reading git-server's configuration from the COBRANCH_ variables: %w", err)
	}
	if c.SSHAddr != "" && c.SSHHostKey == "" {
		return GitServer{}, errors.New("COBRANCH_GIT_SSH_ADDR needs COBRANCH_GIT_SSH_HOST_KEY, the private key SSH clients know the server by")
	}
	return c, nil
}

// PushWorker configures push-worker, which processes the pushes the Git server recorded.
type PushWorker struct {
	HTTPAddr string `envconfig:"HTTP_ADDR" default:":8080"`
	// DatabaseURL is PostgreSQL's connection URL, as the servers'.
	DatabaseURL string `envconfig:"DATABASE_URL"`
	// PollInterval is how often it looks for waiting pushes without being woken, in case it missed a notification.
	PollInterval    time.Duration `envconfig:"PUSH_POLL_INTERVAL" default:"30s"`
	ShutdownTimeout time.Duration `envconfig:"SHUTDOWN_TIMEOUT" default:"20s"`
}

// LoadPushWorker reads push-worker's configuration from the environment.
func LoadPushWorker() (PushWorker, error) {
	var c PushWorker
	if err := envconfig.Process(Prefix, &c); err != nil {
		return PushWorker{}, fmt.Errorf("reading push-worker's configuration from the COBRANCH_ variables: %w", err)
	}
	return c, nil
}

// Hook configures git-hook, a push's pre-receive hook, from what its Git server gives receive-pack.
type Hook struct {
	URL   string `envconfig:"HOOK_URL" required:"true"`
	Token string `envconfig:"HOOK_TOKEN" required:"true"`
}

// LoadHook reads git-hook's configuration from the environment.
func LoadHook() (Hook, error) {
	var c Hook
	if err := envconfig.Process(Prefix, &c); err != nil {
		return Hook{}, fmt.Errorf("reading git-hook's configuration from the COBRANCH_ variables: %w", err)
	}
	return c, nil
}

// Size is a number of bytes, written as Kubernetes writes quantities: a number, with Ki, Mi, Gi or Ti after it.
type Size int64

// sizeUnits are the suffixes a Size may carry, and what each multiplies by.
var sizeUnits = []struct {
	suffix string
	factor int64
}{{"Ti", 1 << 40}, {"Gi", 1 << 30}, {"Mi", 1 << 20}, {"Ki", 1 << 10}}

// Decode reads value, such as 10Gi or 1048576.
func (s *Size) Decode(value string) error {
	number, factor := strings.TrimSpace(value), int64(1)
	for _, u := range sizeUnits {
		if trimmed, ok := strings.CutSuffix(number, u.suffix); ok {
			number, factor = trimmed, u.factor
			break
		}
	}
	n, err := strconv.ParseInt(number, 10, 64)
	if err != nil || n <= 0 || n > math.MaxInt64/factor {
		return fmt.Errorf("%q is not a size, such as 10Gi", value)
	}
	*s = Size(n * factor)
	return nil
}
