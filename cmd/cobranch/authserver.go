package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"

	"cobranch.dev/db/migrations"
	"cobranch.dev/internal/adapters/authserver"
	"cobranch.dev/internal/adapters/oidc"
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
	providers, err := identityProviders(cfg)
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
	service := auth.NewService(postgres.NewAuthStore(pool), providers, settings)

	ln, err := server.Listen(ctx, cfg.HTTPAddr)
	if err != nil {
		return fmt.Errorf("listening: %w", err)
	}
	handler := server.Handler("/", authserver.Handler(service, cookie, cfg.Console.URL),
		map[string]server.Check{"schema": schema.Check, "database": postgres.Ping(pool)})
	slog.InfoContext(ctx, "Starting the Auth server", "schemaVersion", schema.Version(), "sessionCookie", cookie.Name,
		"providers", len(providers), "signUp", settings.SignUp, "seed", settings.Seed)
	if err := server.Serve(ctx, ln, handler, cfg.ShutdownTimeout); err != nil {
		return fmt.Errorf("serving: %w", err)
	}
	return nil
}

// authSettings reads sign-in's settings from the configuration.
func authSettings(cfg config.AuthServer) (auth.Settings, error) {
	policy, err := auth.ParsePolicy(cfg.SignUp)
	if err != nil {
		return auth.Settings{}, fmt.Errorf("%s_AUTH_SIGN_UP: %w", config.Prefix, err)
	}
	return auth.Settings{SignUp: policy, SignUpDomains: cfg.SignUpDomains, Admins: cfg.Admins, SelfServe: cfg.SelfServe, Seed: cfg.Seed}, nil
}

// identityProviders builds the providers the configuration lists, whose callback is the Auth server's /callback.
func identityProviders(cfg config.AuthServer) ([]auth.Provider, error) {
	redirect := cfg.Auth.JoinPath("/callback").String()
	providers := make([]auth.Provider, 0, len(cfg.Providers))
	for _, p := range cfg.Providers {
		issuer := p.Issuer
		if p.Preset != "" {
			preset, ok := oidc.Presets[p.Preset]
			if !ok {
				return nil, fmt.Errorf("provider %q: no preset %q; the presets are google", p.ID, p.Preset)
			}
			issuer = preset
		}
		provider, err := oidc.New(oidc.Config{ID: p.ID, Name: p.Name, Issuer: issuer, ClientID: p.ClientID, ClientSecret: p.ClientSecret}, redirect, nil)
		if err != nil {
			return nil, fmt.Errorf("configuring the identity providers: %w", err)
		}
		providers = append(providers, provider)
	}
	return providers, nil
}
