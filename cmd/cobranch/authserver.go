package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"

	"cobranch.dev/db/migrations"
	"cobranch.dev/internal/adapters/authserver"
	"cobranch.dev/internal/adapters/postgres"
	"cobranch.dev/internal/adapters/server"
	"cobranch.dev/internal/services/auth"
	"cobranch.dev/internal/system/buildinfo"
	"cobranch.dev/internal/system/config"
	"cobranch.dev/internal/system/telemetry"
)

// runAuthServer serves sign-in until ctx ends: the only workload that talks to identity providers and sets the
// session cookie. It turns ready once the database answers and the schema is this build's.
func runAuthServer(ctx context.Context, logs io.Writer) error {
	version := buildinfo.Version()
	flush, err := telemetry.Setup(ctx, logs, "auth-server", version)
	if err != nil {
		return fmt.Errorf("setting up telemetry: %w", err)
	}
	defer flushTelemetry(ctx, flush)
	cfg, err := config.LoadAuthServer()
	if err != nil {
		return fmt.Errorf("configuring: %w", err)
	}
	cookie, err := sessionCookie(cfg.URLs)
	if err != nil {
		return err
	}
	settings, err := authSettings(cfg)
	if err != nil {
		return fmt.Errorf("configuring: %w", err)
	}
	pool, err := postgres.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		return fmt.Errorf("connecting to the database: %w", err)
	}
	defer pool.Close()
	schema, err := postgres.NewSchema(pool, migrations.FS, migrations.Go...)
	if err != nil {
		return fmt.Errorf("reading the schema's version: %w", err)
	}
	defer func() { _ = schema.Close() }() // holds nothing the server wrote
	service, err := signInService(cfg, postgres.NewAuthStore(pool), settings)
	if err != nil {
		return fmt.Errorf("configuring: %w", err)
	}

	ln, err := server.Listen(ctx, cfg.HTTPAddr)
	if err != nil {
		return fmt.Errorf("listening: %w", err)
	}
	handler := server.Handler("/", authserver.Handler(service, cookie, cfg.Console.URL, cfg.BackOffice.URL),
		map[string]server.Check{"schema": schema.Check, "database": postgres.Ping(pool)})
	slog.InfoContext(ctx, "Starting the Auth server", "schemaVersion", schema.Version(), "sessionCookie", cookie.Name,
		"providers", len(cfg.Providers), "secretsKey", cfg.SecretsKey != "", "signUp", settings.SignUp, "seed", settings.Seed)
	if err := server.Serve(ctx, ln, handler, cfg.ShutdownTimeout); err != nil {
		return fmt.Errorf("serving: %w", err)
	}
	return nil
}

// authSettings reads sign-in's settings from the configuration. A sign-in may return to the Back-office, when the
// installation runs one.
func authSettings(cfg config.AuthServer) (auth.Settings, error) {
	settings, err := signUpSettings(cfg.SignUpSettings)
	if err != nil {
		return auth.Settings{}, err
	}
	settings.Admins, settings.Seed = cfg.Admins, cfg.Seed
	if cfg.BackOffice.URL != nil {
		settings.ReturnURLs = append(settings.ReturnURLs, cfg.BackOffice.URL)
	}
	return settings, nil
}

// signUpSettings reads the sign-up settings the chart's values give, which lock those they set: the Back-office keeps
// the rest.
func signUpSettings(cfg config.SignUpSettings) (auth.Settings, error) {
	settings := auth.Settings{SignUpDomains: cfg.SignUpDomains, SelfServe: cfg.SelfServe}
	if cfg.SignUp != "" {
		policy, err := auth.ParsePolicy(cfg.SignUp)
		if err != nil {
			return auth.Settings{}, fmt.Errorf("%s_AUTH_SIGN_UP: %w", config.Prefix, err)
		}
		settings.SignUp = policy
	}
	return settings, nil
}

// signInService is the Auth server's domain over store: the values' identity providers and, with a secrets key, the
// Back-office's beside them, whose callback is the Auth server's /callback.
func signInService(cfg config.AuthServer, store auth.Store, settings auth.Settings) (*auth.Service, error) {
	configs, err := providerConfigs(cfg.Providers)
	if err != nil {
		return nil, err
	}
	build := providerBuilder(cfg)
	providers := make([]auth.Provider, 0, len(configs))
	for _, p := range configs {
		provider, err := build(p)
		if err != nil {
			return nil, err
		}
		providers = append(providers, provider)
	}
	service := auth.NewService(store, providers, settings)
	box, err := sealer(cfg.SecretsKey)
	if err != nil {
		return nil, err
	}
	if box != nil {
		service.UseStoredProviders(box, build)
	}
	return service, nil
}
