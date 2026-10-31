package admin

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"cobranch.dev/internal/services/auth"
	"cobranch.dev/internal/services/orgs"
)

// Where an identity provider comes from.
const (
	// SourceValues is a provider of the chart's values: the Back-office only shows it.
	SourceValues = "values"
	// SourceBackOffice is a provider an administrator added in the Back-office.
	SourceBackOffice = "backOffice"
)

var (
	// ErrProviderTaken is a new provider whose ID another provider, the values' or the Back-office's, has.
	ErrProviderTaken = errors.New("another identity provider has this ID")
	// ErrNoSecretsKey is a client secret to seal on an installation that has no secrets key.
	ErrNoSecretsKey = errors.New("the installation has no secrets key")
)

// Limits on what a provider says.
const (
	maxProviderName = 100
	maxClientID     = 500
	maxClientSecret = 2000
	maxIssuer       = 500
)

// providerIDPattern is a provider's ID: it names the provider in URLs, the database and, for the values', a variable.
var providerIDPattern = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,30}[a-z0-9])?$`)

// Provider is an identity provider as the Back-office lists it, never with its client secret.
type Provider struct {
	ID   string
	Name string
	// Issuer is the provider's issuer URL, or "" when Preset names it.
	Issuer   string
	Preset   string
	ClientID string
	// Source is SourceValues or SourceBackOffice.
	Source string
	// Shadowed is a Back-office provider whose ID the values took since: theirs signs in.
	Shadowed bool
	// Version counts a Back-office provider's changes; the values' have none.
	Version   int64
	CreatedAt time.Time
	UpdatedAt time.Time
	// UpdatedBy is who changed a Back-office provider last: uuid.Nil when they're gone.
	UpdatedBy uuid.UUID
}

// Fingerprint changes whenever the provider does. The Admin API's ETag is made of it.
func (p Provider) Fingerprint() string {
	if p.Source == SourceValues {
		return SourceValues
	}
	return strconv.FormatInt(p.Version, 10)
}

// NewProvider is a provider to add: an issuer or a preset, and the client registered at it.
type NewProvider struct {
	ID, Name, Issuer, Preset, ClientID, ClientSecret string
}

// ProviderChange changes a provider: a nil field stays as it is. Naming an issuer drops the preset, and the other way
// round.
type ProviderChange struct {
	Name, Issuer, Preset, ClientID, ClientSecret *string
}

// ProviderSetup is what the providers' administration needs from outside the database.
type ProviderSetup struct {
	// Values are the chart's values' providers, which the Back-office shows and never changes.
	Values []auth.ProviderConfig
	// Sealer seals client secrets with the installation's secrets key; nil when it has none.
	Sealer auth.Sealer
	// Discover checks that an issuer answers OpenID Connect discovery.
	Discover func(ctx context.Context, issuer string) error
}

// SetProviders configures the identity providers' administration.
func (s *Service) SetProviders(setup ProviderSetup) {
	s.providers = setup
}

// Providers lists the installation's identity providers: the values', in their order, then the Back-office's by ID;
// it takes installation.manage.
func (s *Service) Providers(ctx context.Context, v auth.User) ([]Provider, error) {
	if err := s.require(ctx, v, PermManage); err != nil {
		return nil, err
	}
	stored, err := s.store.IdentityProviders(ctx)
	if err != nil {
		return nil, fmt.Errorf("listing the identity providers: %w", err)
	}
	out := make([]Provider, 0, len(s.providers.Values)+len(stored))
	for _, p := range s.providers.Values {
		out = append(out, fromValues(p))
	}
	for _, p := range stored {
		out = append(out, s.listed(p))
	}
	return out, nil
}

// Provider finds the provider id names; it takes installation.manage.
func (s *Service) Provider(ctx context.Context, v auth.User, id string) (Provider, error) {
	if err := s.require(ctx, v, PermManage); err != nil {
		return Provider{}, err
	}
	if p, ok := s.valuesProvider(id); ok {
		return p, nil
	}
	stored, err := s.store.IdentityProvider(ctx, id)
	if err != nil {
		return Provider{}, fmt.Errorf("reading identity provider %q: %w", id, err)
	}
	return s.listed(stored), nil
}

// AddProvider adds a provider beside the values', once its issuer answers discovery, its client secret sealed; it
// takes installation.manage. An ID another provider has is ErrProviderTaken.
func (s *Service) AddProvider(ctx context.Context, v auth.User, p NewProvider) (Provider, error) {
	if err := s.require(ctx, v, PermManage); err != nil {
		return Provider{}, err
	}
	next := auth.StoredProvider{ID: strings.TrimSpace(p.ID), Name: strings.TrimSpace(p.Name), Issuer: strings.TrimSpace(p.Issuer),
		Preset: strings.TrimSpace(p.Preset), ClientID: strings.TrimSpace(p.ClientID)}
	if !providerIDPattern.MatchString(next.ID) {
		return Provider{}, &orgs.ValidationError{Field: "id", Code: "provider_id.invalid", Message: "lowercase letters, digits and dashes, 32 at most"}
	}
	if _, ok := s.valuesProvider(next.ID); ok {
		return Provider{}, ErrProviderTaken
	}
	if err := errors.Join(validate(next), checkSecret(p.ClientSecret)); err != nil {
		return Provider{}, err
	}
	if err := s.discover(ctx, next); err != nil {
		return Provider{}, err
	}
	sealed, err := s.seal(next.ID, p.ClientSecret)
	if err != nil {
		return Provider{}, err
	}
	next.SealedSecret = sealed
	written, err := s.store.CreateIdentityProvider(ctx, v.ID, next, s.now())
	if err != nil {
		if errors.Is(err, ErrProviderTaken) {
			return Provider{}, ErrProviderTaken
		}
		return Provider{}, fmt.Errorf("adding identity provider %q: %w", next.ID, err)
	}
	return s.listed(written), nil
}

// ChangeProvider makes change to the Back-office's provider id, unless it changed since the caller read it, as its
// fingerprint match says (* for any): then ErrStale. The values' providers refuse any change (ErrLocked); it takes
// installation.manage.
func (s *Service) ChangeProvider(ctx context.Context, v auth.User, id, match string, change ProviderChange) (Provider, error) {
	stored, err := s.storedForChange(ctx, v, id, match)
	if err != nil {
		return Provider{}, err
	}
	next := apply(stored, change)
	invalid := validate(next)
	if change.ClientSecret != nil {
		invalid = errors.Join(invalid, checkSecret(*change.ClientSecret))
	}
	if invalid != nil {
		return Provider{}, invalid
	}
	if next.Issuer != stored.Issuer || next.Preset != stored.Preset {
		if err := s.discover(ctx, next); err != nil {
			return Provider{}, err
		}
	}
	if change.ClientSecret != nil {
		if next.SealedSecret, err = s.seal(id, *change.ClientSecret); err != nil {
			return Provider{}, err
		}
	}
	written, err := s.store.ChangeIdentityProvider(ctx, v.ID, next, s.now())
	if err != nil {
		return Provider{}, fmt.Errorf("changing identity provider %q: %w", id, err)
	}
	return s.listed(written), nil
}

// RemoveProvider removes the Back-office's provider id, unless it changed since the caller read it, as its
// fingerprint match says (* for any): then ErrStale. Its users keep their accounts. The values' providers refuse
// (ErrLocked); it takes installation.manage.
func (s *Service) RemoveProvider(ctx context.Context, v auth.User, id, match string) error {
	stored, err := s.storedForChange(ctx, v, id, match)
	if err != nil {
		return err
	}
	if err := s.store.DeleteIdentityProvider(ctx, v.ID, id, stored.Version); err != nil {
		return fmt.Errorf("removing identity provider %q: %w", id, err)
	}
	return nil
}

// storedForChange reads the Back-office's provider id for v to change or remove, from the version match names.
func (s *Service) storedForChange(ctx context.Context, v auth.User, id, match string) (auth.StoredProvider, error) {
	if err := s.require(ctx, v, PermManage); err != nil {
		return auth.StoredProvider{}, err
	}
	stored, err := s.store.IdentityProvider(ctx, id)
	if errors.Is(err, orgs.ErrNotFound) {
		if _, ok := s.valuesProvider(id); ok {
			return auth.StoredProvider{}, fmt.Errorf("%w: identity provider %s", ErrLocked, id)
		}
	}
	if err != nil {
		return auth.StoredProvider{}, fmt.Errorf("reading identity provider %q: %w", id, err)
	}
	if match != "*" && s.listed(stored).Fingerprint() != match {
		return auth.StoredProvider{}, ErrStale
	}
	return stored, nil
}

// apply makes change to a stored provider. Naming an issuer drops the preset, and the other way round; naming both
// keeps both, which validate refuses.
func apply(stored auth.StoredProvider, change ProviderChange) auth.StoredProvider {
	next := stored
	if change.Name != nil {
		next.Name = strings.TrimSpace(*change.Name)
	}
	if change.ClientID != nil {
		next.ClientID = strings.TrimSpace(*change.ClientID)
	}
	switch {
	case change.Issuer != nil && change.Preset != nil:
		next.Issuer, next.Preset = strings.TrimSpace(*change.Issuer), strings.TrimSpace(*change.Preset)
	case change.Issuer != nil:
		next.Issuer, next.Preset = strings.TrimSpace(*change.Issuer), ""
	case change.Preset != nil:
		next.Issuer, next.Preset = "", strings.TrimSpace(*change.Preset)
	}
	return next
}

// validate refuses a provider that isn't one: a name, an issuer or a preset (one of the two), and a client ID.
func validate(p auth.StoredProvider) error {
	var errs []error
	if p.Name == "" || utf8.RuneCountInString(p.Name) > maxProviderName {
		errs = append(errs, &orgs.ValidationError{Field: "name", Code: "name.invalid", Message: fmt.Sprintf("a name of 1 to %d characters", maxProviderName)})
	}
	if err := checkIssuer(p.Issuer, p.Preset); err != nil {
		errs = append(errs, err)
	}
	if p.ClientID == "" || len(p.ClientID) > maxClientID {
		errs = append(errs, &orgs.ValidationError{Field: "clientId", Code: "client_id.invalid", Message: fmt.Sprintf("a client ID of 1 to %d characters", maxClientID)})
	}
	return errors.Join(errs...)
}

// checkSecret refuses a client secret that is empty or too long.
func checkSecret(secret string) error {
	if secret == "" || len(secret) > maxClientSecret {
		return &orgs.ValidationError{Field: "clientSecret", Code: "client_secret.invalid", Message: fmt.Sprintf("a client secret of 1 to %d characters", maxClientSecret)}
	}
	return nil
}

// discover refuses a provider whose issuer doesn't answer OpenID Connect discovery.
func (s *Service) discover(ctx context.Context, p auth.StoredProvider) error {
	if s.providers.Discover == nil {
		return nil
	}
	issuer := p.Issuer
	if p.Preset != "" {
		issuer = auth.Presets[p.Preset]
	}
	if err := s.providers.Discover(ctx, issuer); err != nil {
		return &orgs.ValidationError{Field: "issuer", Code: "issuer.unreachable", Message: "the issuer doesn't answer OpenID Connect discovery: " + err.Error()}
	}
	return nil
}

// checkIssuer refuses anything but an https issuer URL or a preset, one of the two.
func checkIssuer(issuer, preset string) error {
	switch {
	case (issuer == "") == (preset == ""):
		return &orgs.ValidationError{Field: "issuer", Code: "issuer.required", Message: "an issuer or a preset, one of the two"}
	case preset != "":
		if _, ok := auth.Presets[preset]; !ok {
			return &orgs.ValidationError{Field: "preset", Code: "preset.invalid", Message: "the presets are google"}
		}
		return nil
	}
	u, err := url.Parse(issuer)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || len(issuer) > maxIssuer {
		return &orgs.ValidationError{Field: "issuer", Code: "issuer.invalid", Message: "an https URL with no query, such as https://accounts.example.com"}
	}
	return nil
}

// seal seals provider id's client secret, or refuses when the installation has no secrets key.
func (s *Service) seal(id, secret string) ([]byte, error) {
	if s.providers.Sealer == nil {
		return nil, ErrNoSecretsKey
	}
	sealed, err := s.providers.Sealer.Seal([]byte(secret), auth.SecretAbout(id))
	if err != nil {
		return nil, fmt.Errorf("sealing identity provider %q's client secret: %w", id, err)
	}
	return sealed, nil
}

// valuesProvider finds the values' provider id names.
func (s *Service) valuesProvider(id string) (Provider, bool) {
	for _, p := range s.providers.Values {
		if p.ID == id {
			return fromValues(p), true
		}
	}
	return Provider{}, false
}

// fromValues is how the Back-office lists a values' provider: by its preset alone, when it has one.
func fromValues(p auth.ProviderConfig) Provider {
	out := Provider{ID: p.ID, Name: p.Name, Issuer: p.Issuer, Preset: p.Preset, ClientID: p.ClientID, Source: SourceValues}
	if p.Preset != "" {
		out.Issuer = ""
	}
	return out
}

// listed is how the Back-office lists a stored provider: shadowed when the values took its ID.
func (s *Service) listed(p auth.StoredProvider) Provider {
	_, shadowed := s.valuesProvider(p.ID)
	return Provider{ID: p.ID, Name: p.Name, Issuer: p.Issuer, Preset: p.Preset, ClientID: p.ClientID, Source: SourceBackOffice,
		Shadowed: shadowed, Version: p.Version, CreatedAt: p.CreatedAt, UpdatedAt: p.UpdatedAt, UpdatedBy: p.UpdatedBy}
}
