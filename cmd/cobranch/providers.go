package main

import (
	"context"
	"fmt"

	"cobranch.dev/internal/adapters/oidc"
	"cobranch.dev/internal/adapters/postgres"
	"cobranch.dev/internal/adapters/secrets"
	"cobranch.dev/internal/services/admin"
	"cobranch.dev/internal/services/auth"
	"cobranch.dev/internal/system/config"
)

// providerConfigs are the values' identity providers as the domain names them, each preset resolved to its issuer.
func providerConfigs(providers config.Providers) ([]auth.ProviderConfig, error) {
	out := make([]auth.ProviderConfig, 0, len(providers))
	for _, p := range providers {
		issuer := p.Issuer
		if p.Preset != "" {
			preset, ok := auth.Presets[p.Preset]
			if !ok {
				return nil, fmt.Errorf("provider %q: no preset %q; the presets are google", p.ID, p.Preset)
			}
			issuer = preset
		}
		out = append(out, auth.ProviderConfig{ID: p.ID, Name: p.Name, Issuer: issuer, Preset: p.Preset, ClientID: p.ClientID,
			ClientSecret: p.ClientSecret})
	}
	return out, nil
}

// providerBuilder builds the providers whose callback is the Auth server's /callback at auth.
func providerBuilder(cfg config.AuthServer) auth.ProviderBuilder {
	redirect := cfg.Auth.JoinPath("/callback").String()
	return func(p auth.ProviderConfig) (auth.Provider, error) {
		provider, err := oidc.New(oidc.Config{ID: p.ID, Name: p.Name, Issuer: p.Issuer, ClientID: p.ClientID, ClientSecret: p.ClientSecret}, redirect, nil)
		if err != nil {
			return nil, fmt.Errorf("configuring the identity providers: %w", err)
		}
		return provider, nil
	}
}

// sealer is the Box of the installation's secrets key, or nil when it has none.
func sealer(key string) (auth.Sealer, error) {
	if key == "" {
		return nil, nil
	}
	box, err := secrets.NewBox(key)
	if err != nil {
		return nil, fmt.Errorf("%s_SECRETS_KEY: %w", config.Prefix, err)
	}
	return box, nil
}

// adminService is the Admin server's domain: the installation's administration under the values' sign-up settings
// and identity providers, sealing the Back-office's providers' secrets with the secrets key.
func adminService(cfg config.Server, store *postgres.OrgStore, authz *auth.Authorizer) (*admin.Service, error) {
	values, err := signUpSettings(cfg.SignUpSettings)
	if err != nil {
		return nil, err
	}
	providers, err := providerConfigs(cfg.Providers)
	if err != nil {
		return nil, fmt.Errorf("configuring: %w", err)
	}
	box, err := sealer(cfg.SecretsKey)
	if err != nil {
		return nil, fmt.Errorf("configuring: %w", err)
	}
	service := admin.NewService(store, authz, values)
	service.SetProviders(admin.ProviderSetup{Values: providers, Sealer: box, Discover: func(ctx context.Context, issuer string) error {
		return oidc.Discover(ctx, issuer, nil)
	}})
	return service, nil
}
