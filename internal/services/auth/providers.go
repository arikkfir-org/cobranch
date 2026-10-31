package auth

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"
)

// Presets are the identity providers a configuration may name by a word instead of an issuer.
var Presets = map[string]string{
	"google": "https://accounts.google.com",
}

// ProviderConfig is an identity provider's configuration, the values' or the Back-office's: what the adapter that
// speaks to it needs, its issuer resolved from its preset.
type ProviderConfig struct {
	// ID names the provider in URLs, flows and the database.
	ID string
	// Name is what the sign-in page shows.
	Name string
	// Issuer is the provider's issuer URL; Preset names the preset it came from, if any.
	Issuer string
	Preset string
	// ClientID and ClientSecret are the client the operator registered at the provider.
	ClientID     string
	ClientSecret string
}

// StoredProvider is an identity provider the Back-office added, as the database keeps it: its client secret sealed
// with the installation's secrets key, for its ID.
type StoredProvider struct {
	ID   string
	Name string
	// Issuer is the provider's issuer URL, or "" when Preset names it.
	Issuer       string
	Preset       string
	ClientID     string
	SealedSecret []byte
	// Version counts the changes; a change names the version it read, so one made meanwhile refuses it.
	Version   int64
	CreatedAt time.Time
	UpdatedAt time.Time
	// UpdatedBy is who changed it last: uuid.Nil when they're gone.
	UpdatedBy uuid.UUID
}

// Sealer keeps secrets encrypted at rest with the installation's secrets key. A secret is sealed for what it belongs
// to (about), and opens only for the same.
type Sealer interface {
	Seal(plain []byte, about string) ([]byte, error)
	Open(sealed []byte, about string) ([]byte, error)
}

// SecretAbout is what a provider's client secret is sealed for.
func SecretAbout(providerID string) string {
	return "identity_providers/" + providerID + "/client_secret"
}

// ProviderBuilder makes the Provider a configuration describes: the adapter that speaks to it.
type ProviderBuilder func(ProviderConfig) (Provider, error)

// storedProviders are the Back-office's providers as the Auth server signs in with them: each built once per version,
// so its discovery is kept while its version holds.
type storedProviders struct {
	sealer Sealer
	build  ProviderBuilder

	mu    sync.Mutex
	built map[string]builtProvider
}

// builtProvider is a stored provider's Provider, at the version it was built from.
type builtProvider struct {
	version  int64
	provider Provider
}

// UseStoredProviders makes the service offer the providers the Back-office added, beside the values', opening their
// secrets with sealer and building each with build. A stored provider whose ID the values use is theirs.
func (s *Service) UseStoredProviders(sealer Sealer, build ProviderBuilder) {
	s.stored = &storedProviders{sealer: sealer, build: build, built: map[string]builtProvider{}}
}

// Providers lists the installation's identity providers: the values', in their order, then the Back-office's by ID,
// as the database has them now. One the service can't build (its secret no longer opens) is left out and logged.
func (s *Service) Providers(ctx context.Context) ([]Provider, error) {
	if s.stored == nil {
		return s.order, nil
	}
	stored, err := s.store.IdentityProviders(ctx)
	if err != nil {
		return nil, fmt.Errorf("reading the identity providers: %w", err)
	}
	out := append(make([]Provider, 0, len(s.order)+len(stored)), s.order...)
	s.stored.mu.Lock()
	defer s.stored.mu.Unlock()
	kept := make(map[string]builtProvider, len(stored))
	for _, sp := range stored {
		if _, values := s.providers[sp.ID]; values {
			continue
		}
		b, err := s.stored.provider(sp)
		if err != nil {
			slog.ErrorContext(ctx, "An identity provider can't sign in", "provider", sp.ID, "error", err)
			continue
		}
		kept[sp.ID] = b
		out = append(out, b.provider)
	}
	s.stored.built = kept
	return out, nil
}

// provider returns sp's Provider: the one built for its version, or a new one.
func (p *storedProviders) provider(sp StoredProvider) (builtProvider, error) {
	if b, ok := p.built[sp.ID]; ok && b.version == sp.Version {
		return b, nil
	}
	secret, err := p.sealer.Open(sp.SealedSecret, SecretAbout(sp.ID))
	if err != nil {
		return builtProvider{}, fmt.Errorf("opening its client secret: %w", err)
	}
	issuer := sp.Issuer
	if sp.Preset != "" {
		if issuer = Presets[sp.Preset]; issuer == "" {
			return builtProvider{}, fmt.Errorf("no preset %q", sp.Preset)
		}
	}
	built, err := p.build(ProviderConfig{ID: sp.ID, Name: sp.Name, Issuer: issuer, Preset: sp.Preset, ClientID: sp.ClientID,
		ClientSecret: string(secret)})
	if err != nil {
		return builtProvider{}, fmt.Errorf("building it: %w", err)
	}
	return builtProvider{version: sp.Version, provider: built}, nil
}

// provider finds the provider id names, the values' or the Back-office's, or ErrUnknownProvider.
func (s *Service) provider(ctx context.Context, id string) (Provider, error) {
	if p, ok := s.providers[id]; ok {
		return p, nil
	}
	providers, err := s.Providers(ctx)
	if err != nil {
		return nil, err
	}
	for _, p := range providers {
		if p.ID() == id {
			return p, nil
		}
	}
	return nil, ErrUnknownProvider
}
