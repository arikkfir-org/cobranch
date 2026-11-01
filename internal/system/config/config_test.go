package config

import (
	"os"
	"reflect"
	"testing"
	"time"
)

// every names every variable the loaders read, to unset before each test.
var every = []string{"HTTP_ADDR", "DATABASE_URL", "BLOB_URL", "SHUTDOWN_TIMEOUT", "CONSOLE_URL", "REST_URL", "GRAPHQL_URL",
	"AUTH_URL", "ADMIN_URL", "BACK_OFFICE_URL", "AUTH_PROVIDERS", "AUTH_SIGN_UP", "AUTH_SIGN_UP_DOMAINS", "AUTH_ADMINS", "ORGANIZATIONS_SELF_SERVE", "SEED", "SECRETS_KEY",
	"AUTH_CLIENT_SECRET_GOOGLE", "AUTH_CLIENT_SECRET_MY_IDP", "PUSH_POLL_INTERVAL", "GIT_DATA_DIR", "GIT_CACHE_SIZE",
	"GIT_MAX_PUSH_SIZE", "GIT_COMPACT_AT", "GIT_SSH_ADDR", "GIT_SSH_HOST_KEY", "HOOK_URL", "HOOK_TOKEN"}

// clean unsets every variable, then sets env.
func clean(t *testing.T, env map[string]string) {
	t.Helper()
	for _, name := range every {
		unsetenv(t, "COBRANCH_"+name)
	}
	for k, v := range env {
		t.Setenv(k, v)
	}
}

// mustURL parses s as Decode would.
func mustURL(t *testing.T, s string) URL {
	t.Helper()
	var u URL
	if err := u.Decode(s); err != nil {
		t.Fatal(err)
	}
	return u
}

func TestLoadServer(t *testing.T) {
	tests := []struct {
		name    string
		env     map[string]string
		want    func(t *testing.T) Server
		wantErr bool
	}{
		{name: "defaults", env: map[string]string{"COBRANCH_BLOB_URL": "file:///blob"}, want: func(*testing.T) Server {
			return Server{HTTPAddr: ":8080", BlobURL: "file:///blob", ShutdownTimeout: 20 * time.Second}
		}},
		{
			name: "every variable",
			env: map[string]string{
				"COBRANCH_HTTP_ADDR":                ":9090",
				"COBRANCH_DATABASE_URL":             "postgres://db/cobranch",
				"COBRANCH_BLOB_URL":                 "gs://bucket",
				"COBRANCH_SHUTDOWN_TIMEOUT":         "5s",
				"COBRANCH_CONSOLE_URL":              "https://Console.Example.com/home/",
				"COBRANCH_REST_URL":                 "https://api.example.com",
				"COBRANCH_GRAPHQL_URL":              "https://console.example.com/graphql",
				"COBRANCH_AUTH_URL":                 "https://console.example.com/auth",
				"COBRANCH_ADMIN_URL":                "https://admin.example.com/v/",
				"COBRANCH_BACK_OFFICE_URL":          "https://back-office.example.com",
				"COBRANCH_AUTH_SIGN_UP":             "domains",
				"COBRANCH_AUTH_SIGN_UP_DOMAINS":     "Example.com",
				"COBRANCH_ORGANIZATIONS_SELF_SERVE": "false",
				"COBRANCH_AUTH_PROVIDERS":           `[{"id":"google","name":"Google","preset":"google","clientId":"c1"}]`,
				"COBRANCH_SECRETS_KEY":              "a secrets key of at least 32 characters",
			},
			want: func(t *testing.T) Server {
				return Server{
					HTTPAddr: ":9090", DatabaseURL: "postgres://db/cobranch", BlobURL: "gs://bucket", ShutdownTimeout: 5 * time.Second,
					URLs: URLs{Console: mustURL(t, "https://console.example.com/home"), REST: mustURL(t, "https://api.example.com"),
						GraphQL: mustURL(t, "https://console.example.com/graphql"), Auth: mustURL(t, "https://console.example.com/auth"),
						Admin: mustURL(t, "https://admin.example.com/v"), BackOffice: mustURL(t, "https://back-office.example.com")},
					SignUpSettings: SignUpSettings{SignUp: "domains", SignUpDomains: []string{"example.com"}, SelfServe: &no},
					Providers:      Providers{{ID: "google", Name: "Google", Preset: "google", ClientID: "c1"}}, SecretsKey: "a secrets key of at least 32 characters",
				}
			},
		},
		{name: "the Console set empty", env: map[string]string{"COBRANCH_BLOB_URL": "file:///blob", "COBRANCH_CONSOLE_URL": ""}, want: func(*testing.T) Server {
			return Server{HTTPAddr: ":8080", BlobURL: "file:///blob", ShutdownTimeout: 20 * time.Second}
		}},
		{name: "the Console without a scheme", env: map[string]string{"COBRANCH_BLOB_URL": "file:///blob", "COBRANCH_CONSOLE_URL": "console"}, wantErr: true},
		{name: "no bucket", env: map[string]string{}, wantErr: true},
		{name: "bad duration", env: map[string]string{"COBRANCH_BLOB_URL": "file:///blob", "COBRANCH_SHUTDOWN_TIMEOUT": "soon"}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clean(t, tt.env)
			got, err := LoadServer()
			if (err != nil) != tt.wantErr {
				t.Fatalf("LoadServer() error = %v, want error %t", err, tt.wantErr)
			}
			if want := tt.want; err == nil && !reflect.DeepEqual(got, want(t)) {
				t.Errorf("LoadServer() = %+v, want %+v", got, want(t))
			}
		})
	}
}

func TestURLDecode(t *testing.T) {
	tests := []struct {
		name       string
		value      string
		want       string
		wantOrigin string
		wantErr    bool
	}{
		{name: "a URL with a path", value: "https://console.example.com/home", want: "https://console.example.com/home", wantOrigin: "https://console.example.com"},
		{name: "a trailing slash", value: "https://console.example.com/", want: "https://console.example.com", wantOrigin: "https://console.example.com"},
		{name: "a host in capitals", value: "https://Console.Example.com", want: "https://console.example.com", wantOrigin: "https://console.example.com"},
		{name: "a port", value: "http://localhost:8080/", want: "http://localhost:8080", wantOrigin: "http://localhost:8080"},
		{name: "empty", value: ""},
		{name: "no scheme", value: "console.example.com", wantErr: true},
		{name: "another scheme", value: "ftp://console.example.com", wantErr: true},
		{name: "no host", value: "https:///home", wantErr: true},
		{name: "a query", value: "https://console.example.com/?x=1", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got URL
			err := got.Decode(tt.value)
			if (err != nil) != tt.wantErr {
				t.Fatalf("Decode(%q) error = %v, want error %t", tt.value, err, tt.wantErr)
			}
			if err != nil {
				return
			}
			var s string
			if got.URL != nil {
				s = got.String()
			}
			if s != tt.want || got.Origin() != tt.wantOrigin {
				t.Errorf("Decode(%q) = %q with origin %q, want %q and %q", tt.value, s, got.Origin(), tt.want, tt.wantOrigin)
			}
		})
	}
}

func TestURLsAll(t *testing.T) {
	u := URLs{Console: mustURL(t, "https://a.example.com"), Auth: mustURL(t, "https://b.example.com"), BackOffice: mustURL(t, "https://c.example.com")}
	all := u.All()
	if len(all) != 3 || all[0].Host != "a.example.com" || all[1].Host != "b.example.com" || all[2].Host != "c.example.com" {
		t.Errorf("All() = %v", all)
	}
	if (URLs{}).All() != nil {
		t.Error("All() of no URLs isn't nil")
	}
}

// authBase is the least auth-server needs: the Console's and its own URL.
var authBase = map[string]string{"COBRANCH_CONSOLE_URL": "https://cobranch.example.com", "COBRANCH_AUTH_URL": "https://cobranch.example.com/auth"}

// with is authBase plus more.
func with(more map[string]string) map[string]string {
	env := map[string]string{}
	for k, v := range authBase {
		env[k] = v
	}
	for k, v := range more {
		env[k] = v
	}
	return env
}

// yes and no are a setting's values, which a nil leaves unset.
var yes, no = true, false

// authServerTests are TestLoadAuthServer's cases.
var authServerTests = []struct {
	name          string
	env           map[string]string
	wantProviders Providers
	wantSignUp    string
	wantDomains   []string
	wantAdmins    []string
	wantSelfServe *bool
	wantSeed      bool
	wantKey       string
	wantErr       bool
}{
	{name: "defaults, no provider: sign-up is the Back-office's", env: authBase},
	{
		name: "every variable",
		env: with(map[string]string{
			"COBRANCH_AUTH_PROVIDERS":            `[{"id":"google","name":"Google","preset":"google","clientId":"c1"},{"id":"my-idp","issuer":"https://idp.example.com","clientId":"c2"}]`,
			"COBRANCH_AUTH_CLIENT_SECRET_GOOGLE": "s1",
			"COBRANCH_AUTH_CLIENT_SECRET_MY_IDP": "s2",
			"COBRANCH_AUTH_SIGN_UP":              "domains",
			"COBRANCH_AUTH_SIGN_UP_DOMAINS":      "Example.com, example.org",
			"COBRANCH_AUTH_ADMINS":               "Alice@Example.com",
			"COBRANCH_ORGANIZATIONS_SELF_SERVE":  "false",
			"COBRANCH_SEED":                      "true",
			"COBRANCH_SECRETS_KEY":               "a secrets key of at least 32 characters",
		}),
		wantProviders: Providers{
			{ID: "google", Name: "Google", Preset: "google", ClientID: "c1", ClientSecret: "s1"},
			{ID: "my-idp", Issuer: "https://idp.example.com", ClientID: "c2", ClientSecret: "s2"},
		},
		wantSignUp: "domains", wantDomains: []string{"example.com", "example.org"}, wantAdmins: []string{"alice@example.com"}, wantSelfServe: &no, wantSeed: true,
		wantKey: "a secrets key of at least 32 characters",
	},
	{name: "self-serve set on", env: with(map[string]string{"COBRANCH_ORGANIZATIONS_SELF_SERVE": "true"}), wantSelfServe: &yes},
	{name: "self-serve that isn't a boolean", env: with(map[string]string{"COBRANCH_ORGANIZATIONS_SELF_SERVE": "maybe"}), wantErr: true},
	{name: "a provider without its secret", env: with(map[string]string{"COBRANCH_AUTH_PROVIDERS": `[{"id":"google","preset":"google","clientId":"c1"}]`}), wantErr: true},
	{name: "a provider with an empty secret", env: with(map[string]string{"COBRANCH_AUTH_PROVIDERS": `[{"id":"google","preset":"google","clientId":"c1"}]`, "COBRANCH_AUTH_CLIENT_SECRET_GOOGLE": ""}), wantErr: true},
	{name: "a bad provider ID", env: with(map[string]string{"COBRANCH_AUTH_PROVIDERS": `[{"id":"My IdP","issuer":"https://idp.example.com","clientId":"c"}]`}), wantErr: true},
	{name: "a provider twice", env: with(map[string]string{"COBRANCH_AUTH_PROVIDERS": `[{"id":"a","issuer":"x","clientId":"c"},{"id":"a","issuer":"y","clientId":"c"}]`}), wantErr: true},
	{name: "providers that aren't JSON", env: with(map[string]string{"COBRANCH_AUTH_PROVIDERS": `google`}), wantErr: true},
	{name: "no Console URL", env: map[string]string{"COBRANCH_AUTH_URL": "https://cobranch.example.com/auth"}, wantErr: true},
	{name: "no Auth URL", env: map[string]string{"COBRANCH_CONSOLE_URL": "https://cobranch.example.com"}, wantErr: true},
}

func TestLoadAuthServer(t *testing.T) {
	for _, tt := range authServerTests {
		t.Run(tt.name, func(t *testing.T) {
			clean(t, tt.env)
			got, err := LoadAuthServer()
			if (err != nil) != tt.wantErr {
				t.Fatalf("LoadAuthServer() error = %v, want error %t", err, tt.wantErr)
			}
			if err != nil {
				return
			}
			if !reflect.DeepEqual(got.Providers, tt.wantProviders) || got.SignUp != tt.wantSignUp ||
				!reflect.DeepEqual(got.SignUpDomains, tt.wantDomains) || !reflect.DeepEqual(got.Admins, tt.wantAdmins) ||
				!reflect.DeepEqual(got.SelfServe, tt.wantSelfServe) || got.Seed != tt.wantSeed || got.SecretsKey != tt.wantKey {
				t.Errorf("LoadAuthServer() = %+v", got)
			}
			if got.Console.Host != "cobranch.example.com" || got.Auth.Path != "/auth" || got.HTTPAddr != ":8080" {
				t.Errorf("LoadAuthServer() URLs = %+v, addr %q", got.URLs, got.HTTPAddr)
			}
		})
	}
}

func TestSecretVariable(t *testing.T) {
	tests := []struct{ id, want string }{
		{id: "google", want: "COBRANCH_AUTH_CLIENT_SECRET_GOOGLE"},
		{id: "my-idp", want: "COBRANCH_AUTH_CLIENT_SECRET_MY_IDP"},
	}
	for _, tt := range tests {
		if got := SecretVariable(tt.id); got != tt.want {
			t.Errorf("SecretVariable(%q) = %q, want %q", tt.id, got, tt.want)
		}
	}
}

func TestLoadMigrate(t *testing.T) {
	tests := []struct {
		name string
		env  map[string]string
		want Migrate
	}{
		{name: "libpq's variables", env: map[string]string{}, want: Migrate{}},
		{name: "a URL", env: map[string]string{"COBRANCH_DATABASE_URL": "postgres://db/cobranch"}, want: Migrate{DatabaseURL: "postgres://db/cobranch"}},
		{name: "seeds", env: map[string]string{"COBRANCH_SEED": "true"}, want: Migrate{Seed: true}},
		{name: "no seeds", env: map[string]string{"COBRANCH_SEED": "false"}, want: Migrate{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clean(t, tt.env)
			got, err := LoadMigrate()
			if err != nil {
				t.Fatalf("LoadMigrate() error = %v", err)
			}
			if got != tt.want {
				t.Errorf("LoadMigrate() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestLoadMaintenance(t *testing.T) {
	tests := []struct {
		name    string
		env     map[string]string
		want    Maintenance
		wantErr bool
	}{
		{name: "the database alone", env: map[string]string{"COBRANCH_DATABASE_URL": "postgres://db/cobranch"},
			want: Maintenance{DatabaseURL: "postgres://db/cobranch", GitDataDir: "/var/lib/cobranch/git", CompactAt: 8}},
		{name: "the packs too", env: map[string]string{"COBRANCH_BLOB_URL": "gs://objects", "COBRANCH_GIT_COMPACT_AT": "4", "COBRANCH_GIT_DATA_DIR": "/data"},
			want: Maintenance{BlobURL: "gs://objects", GitDataDir: "/data", CompactAt: 4}},
		{name: "a threshold that compacts nothing", env: map[string]string{"COBRANCH_BLOB_URL": "gs://objects", "COBRANCH_GIT_COMPACT_AT": "1"}, wantErr: true},
		{name: "a threshold that isn't a number", env: map[string]string{"COBRANCH_GIT_COMPACT_AT": "eight"}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clean(t, tt.env)
			got, err := LoadMaintenance()
			if (err != nil) != tt.wantErr || got != tt.want {
				t.Errorf("LoadMaintenance() = %+v, %v, want %+v", got, err, tt.want)
			}
		})
	}
}

// unsetenv unsets name for the test, and sets it back when the test ends.
func unsetenv(t *testing.T, name string) {
	t.Helper()
	t.Setenv(name, "") // records the value to restore
	if err := os.Unsetenv(name); err != nil {
		t.Fatal(err)
	}
}

func TestLoadGitServer(t *testing.T) {
	required := map[string]string{"COBRANCH_BLOB_URL": "file:///blob"}
	with := func(extra map[string]string) map[string]string {
		env := map[string]string{}
		for _, m := range []map[string]string{required, extra} {
			for k, v := range m {
				env[k] = v
			}
		}
		return env
	}
	tests := []struct {
		name    string
		env     map[string]string
		want    GitServer
		wantErr bool
	}{
		{name: "defaults", env: required, want: GitServer{HTTPAddr: ":8080", BlobURL: "file:///blob",
			DataDir: "/var/lib/cobranch/git", CacheSize: 10 << 30, MaxPushSize: 2 << 30, ShutdownTimeout: 20 * time.Second}},
		{name: "sizes", env: with(map[string]string{"COBRANCH_GIT_CACHE_SIZE": "512Mi", "COBRANCH_GIT_MAX_PUSH_SIZE": "1048576", "COBRANCH_GIT_DATA_DIR": "/data"}),
			want: GitServer{HTTPAddr: ":8080", BlobURL: "file:///blob",
				DataDir: "/data", CacheSize: 512 << 20, MaxPushSize: 1 << 20, ShutdownTimeout: 20 * time.Second}},
		{name: "a size that isn't one", env: with(map[string]string{"COBRANCH_GIT_CACHE_SIZE": "10GB"}), wantErr: true},
		{name: "SSH", env: with(map[string]string{"COBRANCH_GIT_SSH_ADDR": ":2222", "COBRANCH_GIT_SSH_HOST_KEY": "-----BEGIN OPENSSH PRIVATE KEY-----"}),
			want: GitServer{HTTPAddr: ":8080", BlobURL: "file:///blob", DataDir: "/var/lib/cobranch/git", CacheSize: 10 << 30, MaxPushSize: 2 << 30,
				SSHAddr: ":2222", SSHHostKey: "-----BEGIN OPENSSH PRIVATE KEY-----", ShutdownTimeout: 20 * time.Second}},
		{name: "SSH without a host key", env: with(map[string]string{"COBRANCH_GIT_SSH_ADDR": ":2222"}), wantErr: true},
		{name: "no bucket", env: map[string]string{}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clean(t, tt.env)
			got, err := LoadGitServer()
			if (err != nil) != tt.wantErr || got != tt.want {
				t.Errorf("LoadGitServer() = %+v, %v, want %+v", got, err, tt.want)
			}
		})
	}
}

func TestLoadPushWorker(t *testing.T) {
	tests := []struct {
		name    string
		env     map[string]string
		want    PushWorker
		wantErr bool
	}{
		{name: "defaults", env: map[string]string{"COBRANCH_DATABASE_URL": "postgres://db/cobranch"},
			want: PushWorker{HTTPAddr: ":8080", DatabaseURL: "postgres://db/cobranch", PollInterval: 30 * time.Second, ShutdownTimeout: 20 * time.Second}},
		{name: "a poll interval", env: map[string]string{"COBRANCH_PUSH_POLL_INTERVAL": "5s"},
			want: PushWorker{HTTPAddr: ":8080", PollInterval: 5 * time.Second, ShutdownTimeout: 20 * time.Second}},
		{name: "a poll interval that isn't one", env: map[string]string{"COBRANCH_PUSH_POLL_INTERVAL": "often"}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clean(t, tt.env)
			got, err := LoadPushWorker()
			if (err != nil) != tt.wantErr || (!tt.wantErr && got != tt.want) {
				t.Errorf("LoadPushWorker() = %+v, %v, want %+v", got, err, tt.want)
			}
		})
	}
}

func TestLoadHook(t *testing.T) {
	clean(t, map[string]string{"COBRANCH_HOOK_URL": "http://127.0.0.1:1/pre-receive", "COBRANCH_HOOK_TOKEN": "t"})
	if got, err := LoadHook(); err != nil || got != (Hook{URL: "http://127.0.0.1:1/pre-receive", Token: "t"}) {
		t.Errorf("LoadHook() = %+v, %v", got, err)
	}
	clean(t, map[string]string{"COBRANCH_HOOK_URL": "http://127.0.0.1:1/pre-receive"})
	if _, err := LoadHook(); err == nil {
		t.Error("LoadHook() without a token succeeded")
	}
}

func TestSizeDecode(t *testing.T) {
	tests := []struct {
		value   string
		want    Size
		wantErr bool
	}{
		{value: "1048576", want: 1 << 20},
		{value: "512Ki", want: 512 << 10},
		{value: "10Gi", want: 10 << 30},
		{value: " 2Ti ", want: 2 << 40},
		{value: "8388607Ti", want: 8388607 << 40},
		{value: "8388608Ti", wantErr: true},
		{value: "0", wantErr: true},
		{value: "-1Gi", wantErr: true},
		{value: "1.5Gi", wantErr: true},
		{value: "10G", wantErr: true},
		{value: "Gi", wantErr: true},
		{value: "", wantErr: true},
	}
	for _, tt := range tests {
		var got Size
		if err := got.Decode(tt.value); (err != nil) != tt.wantErr || got != tt.want {
			t.Errorf("Decode(%q) = %d, %v, want %d", tt.value, got, err, tt.want)
		}
	}
}
