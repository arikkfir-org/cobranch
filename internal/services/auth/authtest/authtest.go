// Package authtest holds fakes of the sign-in domain's ports, for the tests of its service and of the adapters that
// serve it: a Provider that signs anyone in as fixed claims and a Store in memory.
package authtest

import (
	"context"
	"errors"
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
	Fail       error
}

// NewStore returns an empty Store.
func NewStore() *Store {
	return &Store{Flows: map[string]auth.Flow{}, Identities: map[string]auth.Identity{}, Users: map[uuid.UUID]auth.User{},
		Grants: map[uuid.UUID][]auth.Grant{}, ByToken: map[string]auth.Session{}}
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

// SignUp records a new user.
func (s *Store) SignUp(_ context.Context, up auth.SignUp) error {
	if s.Fail != nil {
		return s.Fail
	}
	u := auth.User{Principal: up.Principal, CreatedAt: up.Identity.LastSignInAt}
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
