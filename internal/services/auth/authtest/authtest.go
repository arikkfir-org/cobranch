// Package authtest holds fakes of the sign-in domain's ports, for the tests of its service and of the adapters that
// serve it: a Provider that signs anyone in as fixed claims and a Store in memory.
package authtest

import (
	"context"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"

	"cobranch.dev/internal/services/auth"
)

// Provider is an auth.Provider that signs anyone in as Claims when the code is "good", and refuses otherwise.
type Provider struct {
	ProviderID string
	Claims     auth.Claims
	Refuse     bool
	// Got records the last Exchange's arguments.
	Got struct{ Code, Verifier, Nonce string }
}

// ID names the provider.
func (p *Provider) ID() string { return p.ProviderID }

// Name is the ID, capitalized.
func (p *Provider) Name() string { return strings.ToUpper(p.ProviderID[:1]) + p.ProviderID[1:] }

// AuthCodeURL is a URL at idp.example.com that carries state, nonce and the PKCE challenge.
func (p *Provider) AuthCodeURL(_ context.Context, state, nonce, codeVerifier string) (string, error) {
	return "https://idp.example.com/auth?state=" + state + "&nonce=" + nonce + "&challenge=" + auth.PKCEChallenge(codeVerifier), nil
}

// Exchange answers Claims for the code "good".
func (p *Provider) Exchange(_ context.Context, code, codeVerifier, nonce string) (auth.Claims, error) {
	p.Got.Code, p.Got.Verifier, p.Got.Nonce = code, codeVerifier, nonce
	if p.Refuse || code != "good" {
		return auth.Claims{}, errors.New("bad code")
	}
	return p.Claims, nil
}

// Store is an auth.Store in memory. Fail, when set, fails every call that writes or looks a credential up.
type Store struct {
	Flows      map[string]auth.Flow
	Identities map[string]auth.Identity // by issuer + "|" + subject
	Users      map[uuid.UUID]auth.User
	Grants     map[uuid.UUID][]auth.Grant
	ByToken    map[string]auth.Session // by token hash
	// Credentials are the API keys' secrets and the personal tokens by token hash, and Principals the API keys they
	// act as (a token acts as one of Users).
	Credentials map[string]auth.Credential
	Principals  map[uuid.UUID]auth.Principal
	// Installation is the settings the Back-office changes, and Stored the providers it added, by ID.
	Installation auth.InstallationSettings
	Stored       []auth.StoredProvider
	Fail         error
}

// NewStore returns an empty Store.
func NewStore() *Store {
	return &Store{Flows: map[string]auth.Flow{}, Identities: map[string]auth.Identity{}, Users: map[uuid.UUID]auth.User{},
		Grants: map[uuid.UUID][]auth.Grant{}, ByToken: map[string]auth.Session{},
		Credentials: map[string]auth.Credential{}, Principals: map[uuid.UUID]auth.Principal{}}
}

// AddCredential registers secret as an API key's secret acting as principal, with expiresAt (zero never).
func (s *Store) AddCredential(secret string, principal auth.Principal, expiresAt time.Time) auth.Credential {
	c := auth.Credential{ID: uuid.New(), Kind: auth.CredentialAPIKey, PrincipalID: principal.ID, Hint: auth.Hint(secret), ExpiresAt: expiresAt}
	s.Credentials[string(auth.Hash(secret))] = c
	s.Principals[principal.ID] = principal
	return c
}

// Credential finds a credential by its token's hash, with the user or the API key it acts as.
func (s *Store) Credential(_ context.Context, tokenHash []byte) (auth.Credential, auth.User, error) {
	if s.Fail != nil {
		return auth.Credential{}, auth.User{}, s.Fail
	}
	c, ok := s.Credentials[string(tokenHash)]
	if !ok {
		return auth.Credential{}, auth.User{}, auth.ErrNotFound
	}
	if u, ok := s.Users[c.PrincipalID]; ok {
		return c, u, nil
	}
	return c, auth.User{Principal: s.Principals[c.PrincipalID]}, nil
}

// CreateToken records personal token t by hash.
func (s *Store) CreateToken(_ context.Context, t auth.Credential, hash []byte) error {
	if s.Fail != nil {
		return s.Fail
	}
	s.Credentials[string(hash)] = t
	return nil
}

// Tokens lists a user's personal tokens that are not revoked, newest first.
func (s *Store) Tokens(_ context.Context, userID uuid.UUID) ([]auth.Credential, error) {
	if s.Fail != nil {
		return nil, s.Fail
	}
	var out []auth.Credential
	for _, c := range s.Credentials {
		if c.Kind == auth.CredentialPersonalToken && c.PrincipalID == userID && c.RevokedAt.IsZero() {
			out = append(out, c)
		}
	}
	slices.SortFunc(out, func(a, b auth.Credential) int { return b.CreatedAt.Compare(a.CreatedAt) })
	return out, nil
}

// RevokeToken revokes a user's personal token.
func (s *Store) RevokeToken(_ context.Context, userID, id uuid.UUID, at time.Time) error {
	if s.Fail != nil {
		return s.Fail
	}
	for k, c := range s.Credentials {
		if c.ID == id && c.Kind == auth.CredentialPersonalToken && c.PrincipalID == userID && c.RevokedAt.IsZero() {
			c.RevokedAt = at
			s.Credentials[k] = c
			return nil
		}
	}
	return auth.ErrNotFound
}

// TouchCredential marks credential id used at usedAt.
func (s *Store) TouchCredential(_ context.Context, id uuid.UUID, usedAt time.Time) error {
	for k, c := range s.Credentials {
		if c.ID == id {
			c.LastUsedAt = usedAt
			s.Credentials[k] = c
		}
	}
	return nil
}

// CreateFlow records f.
func (s *Store) CreateFlow(_ context.Context, f auth.Flow) error {
	if s.Fail != nil {
		return s.Fail
	}
	s.Flows[string(f.StateHash)] = f
	return nil
}

// TakeFlow removes and returns the flow stateHash names.
func (s *Store) TakeFlow(_ context.Context, stateHash []byte) (auth.Flow, error) {
	if s.Fail != nil {
		return auth.Flow{}, s.Fail
	}
	f, ok := s.Flows[string(stateHash)]
	if !ok {
		return auth.Flow{}, auth.ErrNotFound
	}
	delete(s.Flows, string(stateHash))
	return f, nil
}

// Identity finds an identity.
func (s *Store) Identity(_ context.Context, issuer, subject string) (auth.Identity, error) {
	if s.Fail != nil {
		return auth.Identity{}, s.Fail
	}
	i, ok := s.Identities[issuer+"|"+subject]
	if !ok {
		return auth.Identity{}, auth.ErrNotFound
	}
	return i, nil
}

// EmailHolder finds who holds email, verified.
func (s *Store) EmailHolder(_ context.Context, email string) (uuid.UUID, string, error) {
	for id, u := range s.Users {
		for _, e := range u.Emails {
			if e.Email == email && e.Verified {
				for _, i := range s.Identities {
					if i.UserID == id {
						return id, i.Issuer, nil
					}
				}
				return id, "", nil
			}
		}
	}
	return uuid.Nil, "", auth.ErrNotFound
}

// LoginlessHolder finds the user with no identity who holds email unverified.
func (s *Store) LoginlessHolder(_ context.Context, email string) (uuid.UUID, error) {
	if s.Fail != nil {
		return uuid.Nil, s.Fail
	}
	for id, u := range s.Users {
		for _, e := range u.Emails {
			if e.Email == email && !e.Verified && !s.signsIn(id) {
				return id, nil
			}
		}
	}
	return uuid.Nil, auth.ErrNotFound
}

// signsIn reports whether the user has an identity.
func (s *Store) signsIn(id uuid.UUID) bool {
	for _, i := range s.Identities {
		if i.UserID == id {
			return true
		}
	}
	return false
}

// SignUp records a new user, or gives a login-less one the identity and verifies their email.
func (s *Store) SignUp(_ context.Context, up auth.SignUp) error {
	if s.Fail != nil {
		return s.Fail
	}
	u := auth.User{Principal: up.Principal, CreatedAt: up.Identity.LastSignInAt}
	if up.Adopt {
		u.CreatedAt = s.Users[up.Principal.ID].CreatedAt
	}
	if up.Identity.EmailVerified {
		u.Emails = []auth.Email{{Email: up.Identity.Email, Verified: true}}
	}
	s.Users[up.Principal.ID] = u
	s.Identities[up.Identity.Issuer+"|"+up.Identity.Subject] = up.Identity
	s.Grants[up.Principal.ID] = up.Grants
	return nil
}

// SignIn records a sign-in, as the real store does: the admin grant from the list comes or goes.
func (s *Store) SignIn(_ context.Context, i auth.Identity, admin bool, session auth.Session) error {
	if s.Fail != nil {
		return s.Fail
	}
	s.Identities[i.Issuer+"|"+i.Subject] = i
	s.ByToken[string(session.TokenHash)] = session
	grants := s.Grants[i.UserID][:0:0]
	for _, g := range s.Grants[i.UserID] {
		if g.RoleID != auth.RoleInstallationAdmin || g.Source != auth.SourceAdmins {
			grants = append(grants, g)
		}
	}
	if admin {
		grants = append(grants, auth.Grant{RoleID: auth.RoleInstallationAdmin, Source: auth.SourceAdmins})
	}
	s.Grants[i.UserID] = grants
	return nil
}

// Session finds a session by its token's hash.
func (s *Store) Session(_ context.Context, tokenHash []byte) (auth.Session, auth.User, error) {
	if s.Fail != nil {
		return auth.Session{}, auth.User{}, s.Fail
	}
	session, ok := s.ByToken[string(tokenHash)]
	if !ok {
		return auth.Session{}, auth.User{}, auth.ErrNotFound
	}
	return session, s.Users[session.UserID], nil
}

// Touch marks session id used at seen.
func (s *Store) Touch(_ context.Context, id uuid.UUID, seen time.Time) error {
	for k, session := range s.ByToken {
		if session.ID == id {
			session.LastSeenAt, session.IdleExpiresAt = seen, seen.Add(auth.SessionIdle)
			s.ByToken[k] = session
		}
	}
	return nil
}

// Sessions lists a user's sessions that are not revoked.
func (s *Store) Sessions(_ context.Context, userID uuid.UUID) ([]auth.Session, error) {
	var out []auth.Session
	for _, session := range s.ByToken {
		if session.UserID == userID && session.RevokedAt.IsZero() {
			out = append(out, session)
		}
	}
	return out, nil
}

// Revoke revokes a user's session.
func (s *Store) Revoke(_ context.Context, userID, id uuid.UUID, at time.Time) error {
	for k, session := range s.ByToken {
		if session.ID == id && session.UserID == userID && session.RevokedAt.IsZero() {
			session.RevokedAt = at
			s.ByToken[k] = session
			return nil
		}
	}
	return auth.ErrNotFound
}

// SeededUsers lists the users with a seed identity.
func (s *Store) SeededUsers(context.Context) ([]auth.Seeded, error) {
	var out []auth.Seeded
	for _, i := range s.Identities {
		if i.Issuer == auth.SeedIssuer {
			out = append(out, auth.Seeded{Subject: i.Subject, DisplayName: s.Users[i.UserID].DisplayName, Email: i.Email})
		}
	}
	return out, nil
}

// SeededUser finds a seed identity by its subject.
func (s *Store) SeededUser(ctx context.Context, subject string) (auth.Identity, error) {
	return s.Identity(ctx, auth.SeedIssuer, subject)
}

// InstallationSettings reads the settings the Back-office changes.
func (s *Store) InstallationSettings(context.Context) (auth.InstallationSettings, error) {
	if s.Fail != nil {
		return auth.InstallationSettings{}, s.Fail
	}
	return s.Installation, nil
}

// IdentityProviders lists the providers the Back-office added.
func (s *Store) IdentityProviders(context.Context) ([]auth.StoredProvider, error) {
	if s.Fail != nil {
		return nil, s.Fail
	}
	return s.Stored, nil
}

// DeleteExpired drops the flows and sessions that expired at `at`.
func (s *Store) DeleteExpired(_ context.Context, at time.Time) (int64, int64, error) {
	if s.Fail != nil {
		return 0, 0, s.Fail
	}
	var flows, sessions int64
	for k, f := range s.Flows {
		if f.ExpiresAt.Before(at) {
			delete(s.Flows, k)
			flows++
		}
	}
	for k, session := range s.ByToken {
		if !session.Live(at) {
			delete(s.ByToken, k)
			sessions++
		}
	}
	return flows, sessions, nil
}

// Sealer is an auth.Sealer that seals in plain sight, as "sealed:<about>:<plain>", so a test can read what it sealed.
// Fail, when set, fails every call.
type Sealer struct {
	Fail error
}

// Seal prefixes plain with what it is sealed for.
func (s Sealer) Seal(plain []byte, about string) ([]byte, error) {
	if s.Fail != nil {
		return nil, s.Fail
	}
	return append([]byte("sealed:"+about+":"), plain...), nil
}

// Open strips what Seal prefixed, or fails for another about.
func (s Sealer) Open(sealed []byte, about string) ([]byte, error) {
	prefix := "sealed:" + about + ":"
	if s.Fail != nil || !strings.HasPrefix(string(sealed), prefix) {
		return nil, errors.New("doesn't open")
	}
	return sealed[len(prefix):], nil
}
