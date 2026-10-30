package config

import (
	"os"
	"reflect"
	"testing"
	"time"
)

// every names every variable the loaders read, to unset before each test.
var every = []string{"HTTP_ADDR", "DATABASE_URL", "BLOB_URL", "SHUTDOWN_TIMEOUT", "CONSOLE_URL", "REST_URL", "GRAPHQL_URL",
	"AUTH_URL", "AUTH_PROVIDERS", "AUTH_SIGN_UP", "AUTH_SIGN_UP_DOMAINS", "AUTH_ADMINS", "ORGANIZATIONS_SELF_SERVE", "SEED",
	"AUTH_CLIENT_SECRET_GOOGLE", "AUTH_CLIENT_SECRET_MY_IDP"}

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
		{
			name: "defaults",
			env:  map[string]string{"COBRANCH_BLOB_URL": "file:///blob"},
			want: func(*testing.T) Server {
				return Server{HTTPAddr: ":8080", BlobURL: "file:///blob", ShutdownTimeout: 20 * time.Second}
			},
		},
		{
			name: "every variable",
			env: map[string]string{
				"COBRANCH_HTTP_ADDR":        ":9090",
				"COBRANCH_DATABASE_URL":     "postgres://db/cobranch",
				"COBRANCH_BLOB_URL":         "gs://bucket",
				"COBRANCH_SHUTDOWN_TIMEOUT": "5s",
				"COBRANCH_CONSOLE_URL":      "https://Console.Example.com/home/",
				"COBRANCH_REST_URL":         "https://api.example.com",
				"COBRANCH_GRAPHQL_URL":      "https://console.example.com/graphql",
				"COBRANCH_AUTH_URL":         "https://console.example.com/auth",
			},
			want: func(t *testing.T) Server {
				return Server{
					HTTPAddr: ":9090", DatabaseURL: "postgres://db/cobranch", BlobURL: "gs://bucket", ShutdownTimeout: 5 * time.Second,
					URLs: URLs{Console: mustURL(t, "https://console.example.com/home"), REST: mustURL(t, "https://api.example.com"),
						GraphQL: mustURL(t, "https://console.example.com/graphql"), Auth: mustURL(t, "https://console.example.com/auth")},
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
			if err != nil {
				return
			}
			if want := tt.want(t); !reflect.DeepEqual(got, want) {
				t.Errorf("LoadServer() = %+v, want %+v", got, want)
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
	u := URLs{Console: mustURL(t, "https://a.example.com"), Auth: mustURL(t, "https://b.example.com")}
	all := u.All()
	if len(all) != 2 || all[0].Host != "a.example.com" || all[1].Host != "b.example.com" {
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

// authServerTests are TestLoadAuthServer's cases.
var authServerTests = []struct {
	name          string
	env           map[string]string
	wantProviders Providers
	wantSignUp    string
	wantDomains   []string
	wantAdmins    []string
	wantSelfServe bool
	wantSeed      bool
	wantErr       bool
}{
	{name: "defaults, no provider", env: authBase, wantSignUp: "anyone", wantSelfServe: true},
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
		}),
		wantProviders: Providers{
			{ID: "google", Name: "Google", Preset: "google", ClientID: "c1", ClientSecret: "s1"},
			{ID: "my-idp", Issuer: "https://idp.example.com", ClientID: "c2", ClientSecret: "s2"},
		},
		wantSignUp: "domains", wantDomains: []string{"example.com", "example.org"}, wantAdmins: []string{"alice@example.com"}, wantSeed: true,
	},
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
				got.SelfServe != tt.wantSelfServe || got.Seed != tt.wantSeed {
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
	clean(t, map[string]string{"COBRANCH_DATABASE_URL": "postgres://db/cobranch"})
	got, err := LoadMaintenance()
	if err != nil || got.DatabaseURL != "postgres://db/cobranch" {
		t.Errorf("LoadMaintenance() = %+v, %v", got, err)
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
