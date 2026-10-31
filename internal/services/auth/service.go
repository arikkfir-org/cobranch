package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"net/url"
	"path"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Lifetimes, the principals design's defaults.
const (
	// FlowLifetime is how long a sign-in may take at the provider.
	FlowLifetime = 10 * time.Minute
	// SessionIdle ends a session unused for this long.
	SessionIdle = 7 * 24 * time.Hour
	// SessionMax ends a session this long after sign-in, used or not.
	SessionMax = 30 * 24 * time.Hour
	// touchEvery is how often a session's last use is written: at most once a minute, not on every request.
	touchEvery = time.Minute
)

// Provider is an identity provider the installation trusts: OpenID Connect's code flow, as the adapter speaks it.
type Provider interface {
	ID() string
	Name() string
	// AuthCodeURL is where the browser signs in, carrying state, nonce and the PKCE challenge of a verifier.
	AuthCodeURL(ctx context.Context, state, nonce, codeVerifier string) (string, error)
	// Exchange trades the code for an ID token, verifies it and its nonce, and returns its claims.
	Exchange(ctx context.Context, code, codeVerifier, nonce string) (Claims, error)
}

// Store is what the database keeps for sign-in. Each method is one transaction.
type Store interface {
	CreateFlow(ctx context.Context, flow Flow) error
	// TakeFlow deletes the flow stateHash names and returns it, or ErrNotFound: a flow is finished once.
	TakeFlow(ctx context.Context, stateHash []byte) (Flow, error)
	// Identity finds who an issuer and subject signed in as, or ErrNotFound.
	Identity(ctx context.Context, issuer, subject string) (Identity, error)
	// EmailHolder finds the user who holds email, verified, and the issuer of an identity of theirs, or ErrNotFound.
	EmailHolder(ctx context.Context, email string) (uuid.UUID, string, error)
	// LoginlessHolder finds the login-less user who holds email unverified, a user with no identity whom a push made
	// for a commit's author or committer, or ErrNotFound.
	LoginlessHolder(ctx context.Context, email string) (uuid.UUID, error)
	// SignUp creates the user, its identity, its email if verified, its grants, and their events; with Adopt, it gives
	// the login-less user the identity and verifies the email instead of creating them.
	SignUp(ctx context.Context, s SignUp) error
	// SignIn records the identity's latest claims and sign-in, sets Installation admin from the installation's list
	// (adding or taking back only the grant that list gave), creates the session and its event.
	SignIn(ctx context.Context, identity Identity, admin bool, session Session) error
	// Session finds a session by its token's hash, with its user, or ErrNotFound.
	Session(ctx context.Context, tokenHash []byte) (Session, User, error)
	// Touch records that session id was used at seen.
	Touch(ctx context.Context, id uuid.UUID, seen time.Time) error
	// Credential finds a credential by its token's hash, with the principal it acts as (a user with their emails), or
	// ErrNotFound.
	Credential(ctx context.Context, tokenHash []byte) (Credential, User, error)
	// TouchCredential records that credential id was used at usedAt.
	TouchCredential(ctx context.Context, id uuid.UUID, usedAt time.Time) error
	// CreateToken records personal token t, its secret hashed to hash, and its event, as its user.
	CreateToken(ctx context.Context, t Credential, hash []byte) error
	// Tokens lists a user's personal tokens that are not revoked, newest first.
	Tokens(ctx context.Context, userID uuid.UUID) ([]Credential, error)
	// RevokeToken revokes personal token id of user userID at now, or ErrNotFound.
	RevokeToken(ctx context.Context, userID, id uuid.UUID, now time.Time) error
	// Sessions lists a user's sessions that are not revoked, newest first.
	Sessions(ctx context.Context, userID uuid.UUID) ([]Session, error)
	// Revoke revokes session id of user userID at now, or ErrNotFound.
	Revoke(ctx context.Context, userID, id uuid.UUID, now time.Time) error
	// SeededUsers lists the demo data's users: those with an identity of SeedIssuer, by display name.
	SeededUsers(ctx context.Context) ([]Seeded, error)
	// SeededUser finds a demo user by the subject of their SeedIssuer identity, or ErrNotFound.
	SeededUser(ctx context.Context, subject string) (Identity, error)
	// DeleteExpired deletes the flows and sessions that expired before now, and returns how many of each.
	DeleteExpired(ctx context.Context, now time.Time) (flows, sessions int64, err error)
	// InstallationSettings reads the settings the Back-office changes.
	InstallationSettings(ctx context.Context) (InstallationSettings, error)
	// IdentityProviders lists the identity providers the Back-office added, by ID.
	IdentityProviders(ctx context.Context) ([]StoredProvider, error)
}

// Settings is the installation's sign-in configuration, from the chart's values. A sign-up setting they leave unset
// (an empty SignUp, a nil SelfServe) is the Back-office's (InstallationSettings).
type Settings struct {
	SignUp Policy
	// SignUpDomains admits verified emails under PolicyDomains, lowercase.
	SignUpDomains []string
	// Admins are the first administrators' emails, lowercase: a verified sign-in with one gets Installation admin.
	Admins []string
	// SelfServe grants every new user Installation user, which creates organizations.
	SelfServe *bool
	// Seed lets the demo data's users sign in without a provider: demo installations only.
	Seed bool
	// ReturnURLs are the absolute URLs, besides the Console's paths, below which a sign-in may return: the
	// Back-office's, whose pages sign in through the Console's Auth server too.
	ReturnURLs []*url.URL
}

// Service signs people in and up, and resolves their sessions.
type Service struct {
	store     Store
	providers map[string]Provider
	order     []Provider
	// stored are the Back-office's providers, when the service offers them (UseStoredProviders).
	stored   *storedProviders
	settings Settings
	now      func() time.Time
}

// NewService returns a Service over store, trusting providers, under settings.
func NewService(store Store, providers []Provider, settings Settings) *Service {
	byID := make(map[string]Provider, len(providers))
	for _, p := range providers {
		byID[p.ID()] = p
	}
	return &Service{store: store, providers: byID, order: providers, settings: settings, now: time.Now}
}

// SetClock makes the service read the time from now: for tests.
func (s *Service) SetClock(now func() time.Time) {
	s.now = now
}

// Settings returns the installation's sign-in settings.
func (s *Service) Settings() Settings {
	return s.settings
}

// Begun is a sign-in that began: where to send the browser, and the token its flow cookie carries.
type Begun struct {
	RedirectURL  string
	BrowserToken string
}

// Begin starts a sign-in at provider providerID that returns to returnPath (SafeReturn), and records the flow for
// whichever replica the callback reaches.
func (s *Service) Begin(ctx context.Context, providerID, returnPath string) (Begun, error) {
	p, err := s.provider(ctx, providerID)
	if err != nil {
		return Begun{}, err
	}
	state, nonce, verifier, browser := randomText(), randomText(), randomText(), randomText()
	flow := Flow{
		StateHash:    Hash(state),
		BrowserHash:  Hash(browser),
		ProviderID:   providerID,
		Nonce:        nonce,
		PKCEVerifier: verifier,
		ReturnPath:   s.SafeReturn(returnPath),
		ExpiresAt:    s.now().Add(FlowLifetime),
	}
	if err := s.store.CreateFlow(ctx, flow); err != nil {
		return Begun{}, fmt.Errorf("recording the sign-in: %w", err)
	}
	u, err := p.AuthCodeURL(ctx, state, nonce, verifier)
	if err != nil {
		return Begun{}, fmt.Errorf("%w: %w", ErrProvider, err)
	}
	return Begun{RedirectURL: u, BrowserToken: browser}, nil
}

// SafeReturnPath keeps returnPath when it is a path of the Console's own URL, and otherwise returns its home.
func SafeReturnPath(returnPath string) string {
	if returnPath == "" || returnPath[0] != '/' || strings.HasPrefix(returnPath, "//") || strings.HasPrefix(returnPath, "/\\") ||
		strings.ContainsAny(returnPath, " \r\n") {
		return "/"
	}
	return returnPath
}

// SafeReturn keeps where a sign-in returns to when it is a path of the Console's own URL, or an absolute URL at or
// below one of the settings' ReturnURLs, and otherwise returns the Console's home: never another site.
func (s *Service) SafeReturn(returnTo string) string {
	u, err := url.Parse(returnTo)
	if err != nil || !u.IsAbs() || u.User != nil || strings.ContainsAny(returnTo, " \r\n\\") {
		return SafeReturnPath(returnTo)
	}
	for _, base := range s.settings.ReturnURLs {
		if strings.EqualFold(u.Scheme, base.Scheme) && strings.EqualFold(u.Host, base.Host) && below(u.Path, base.Path) {
			return returnTo
		}
	}
	return "/"
}

// below reports whether p is base, or a path under it, with no dot segments that would climb out of it.
func below(p, base string) bool {
	if clean := path.Clean(p); p != "" && clean != p && clean != strings.TrimSuffix(p, "/") {
		return false
	}
	base = strings.TrimSuffix(base, "/")
	return p == base || strings.HasPrefix(p, base+"/")
}

// Completed is a finished sign-in: the session's token, for the cookie, and where the browser is to return to: a
// Console path, or an absolute URL that SafeReturn kept.
type Completed struct {
	SessionToken string
	ReturnPath   string
}

// Complete finishes the sign-in whose provider sent code and state back to a browser carrying browserToken: it takes
// the flow once, exchanges the code, finds the identity or signs it up under the policy, and makes a session.
func (s *Service) Complete(ctx context.Context, code, state, browserToken string, client Client) (Completed, error) {
	flow, err := s.store.TakeFlow(ctx, Hash(state))
	if errors.Is(err, ErrNotFound) {
		return Completed{}, ErrFlow
	}
	if err != nil {
		return Completed{}, fmt.Errorf("taking the sign-in flow: %w", err)
	}
	now := s.now()
	if !now.Before(flow.ExpiresAt) || string(flow.BrowserHash) != string(Hash(browserToken)) {
		return Completed{}, ErrFlow
	}
	p, err := s.provider(ctx, flow.ProviderID)
	if err != nil {
		return Completed{}, err
	}
	claims, err := p.Exchange(ctx, code, flow.PKCEVerifier, flow.Nonce)
	if err != nil {
		return Completed{}, fmt.Errorf("%w: %w", ErrProvider, err)
	}
	claims.Email = strings.ToLower(claims.Email)
	identity, err := s.store.Identity(ctx, claims.Issuer, claims.Subject)
	switch {
	case errors.Is(err, ErrNotFound):
		if identity, err = s.signUp(ctx, claims, now); err != nil {
			return Completed{}, err
		}
	case err != nil:
		return Completed{}, fmt.Errorf("finding the identity: %w", err)
	}
	identity.Email, identity.EmailVerified, identity.Name, identity.LastSignInAt = claims.Email, claims.EmailVerified, claims.Name, now
	return s.signIn(ctx, identity, client, now, flow.ReturnPath)
}

// signUp admits a new identity under the policy and creates its user, or refuses it.
func (s *Service) signUp(ctx context.Context, c Claims, now time.Time) (Identity, error) {
	verified := c.EmailVerified && c.Email != ""
	if verified {
		_, issuer, err := s.store.EmailHolder(ctx, c.Email)
		if err == nil {
			return Identity{}, &EmailTakenError{Email: c.Email, Issuer: issuer}
		}
		if !errors.Is(err, ErrNotFound) {
			return Identity{}, fmt.Errorf("looking the email up: %w", err)
		}
	}
	installation, err := s.store.InstallationSettings(ctx)
	if err != nil {
		return Identity{}, fmt.Errorf("reading the installation's settings: %w", err)
	}
	settings := s.settings.SignUpSettings(installation)
	admin := verified && s.isAdmin(c.Email)
	if !admin && !settings.admits(c, verified) {
		return Identity{}, ErrSignUpRefused
	}
	id, err := uuid.NewV7()
	if err != nil {
		return Identity{}, fmt.Errorf("making an ID: %w", err)
	}
	adopt := false
	if verified {
		loginless, err := s.store.LoginlessHolder(ctx, c.Email)
		switch {
		case err == nil:
			id, adopt = loginless, true
		case !errors.Is(err, ErrNotFound):
			return Identity{}, fmt.Errorf("looking for a login-less user with the email: %w", err)
		}
	}
	identity := Identity{Issuer: c.Issuer, Subject: c.Subject, UserID: id, Email: c.Email, EmailVerified: c.EmailVerified, Name: c.Name, LastSignInAt: now}
	up := SignUp{Principal: Principal{ID: id, Kind: KindUser, DisplayName: displayName(c)}, Identity: identity, Adopt: adopt}
	if settings.SelfServe {
		up.Grants = append(up.Grants, Grant{RoleID: RoleInstallationUser, Source: SourceSelfServe})
	}
	if admin {
		up.Grants = append(up.Grants, Grant{RoleID: RoleInstallationAdmin, Source: SourceAdmins})
	}
	if err := s.store.SignUp(ctx, up); err != nil {
		return Identity{}, fmt.Errorf("signing up: %w", err)
	}
	return identity, nil
}

func (s *Service) isAdmin(email string) bool {
	for _, a := range s.settings.Admins {
		if a == email {
			return true
		}
	}
	return false
}

// signIn records the sign-in and makes the session.
func (s *Service) signIn(ctx context.Context, identity Identity, client Client, now time.Time, returnPath string) (Completed, error) {
	token := NewSecret(PrefixSession)
	id, err := uuid.NewV7()
	if err != nil {
		return Completed{}, fmt.Errorf("making an ID: %w", err)
	}
	session := Session{
		ID: id, TokenHash: Hash(token), UserID: identity.UserID, Issuer: identity.Issuer, Subject: identity.Subject,
		CreatedAt: now, LastSeenAt: now, IdleExpiresAt: now.Add(SessionIdle), ExpiresAt: now.Add(SessionMax),
		UserAgent: client.UserAgent, IP: client.IP,
	}
	admin := identity.EmailVerified && s.isAdmin(identity.Email)
	if err := s.store.SignIn(ctx, identity, admin, session); err != nil {
		return Completed{}, fmt.Errorf("signing in: %w", err)
	}
	return Completed{SessionToken: token, ReturnPath: returnPath}, nil
}

// SignInSeeded signs in as the demo user whose seed subject is subject, with no provider: for demo installations, while
// seeding is on, and otherwise ErrSeedOff.
func (s *Service) SignInSeeded(ctx context.Context, subject, returnPath string, client Client) (Completed, error) {
	if !s.settings.Seed {
		return Completed{}, ErrSeedOff
	}
	identity, err := s.store.SeededUser(ctx, subject)
	if err != nil {
		return Completed{}, fmt.Errorf("finding the seeded user: %w", err)
	}
	now := s.now()
	identity.LastSignInAt = now
	return s.signIn(ctx, identity, client, now, s.SafeReturn(returnPath))
}

// SeededUsers lists the demo users the sign-in page offers while seeding is on, and nothing otherwise.
func (s *Service) SeededUsers(ctx context.Context) ([]Seeded, error) {
	if !s.settings.Seed {
		return nil, nil
	}
	users, err := s.store.SeededUsers(ctx)
	if err != nil {
		return nil, fmt.Errorf("listing the seeded users: %w", err)
	}
	return users, nil
}

// Authenticated is who a credential resolved to: a user and the session it came from if a cookie's, a user and their
// personal token, or an API key's principal and its secret.
type Authenticated struct {
	User       User
	Session    Session
	Credential Credential
}

// Authenticate resolves a token to who it acts as: a session token (the cookie's) to its user, a personal token to
// its user, an API key's secret to the key. ErrUnauthenticated when it matches nothing, expired, was revoked or its
// principal is suspended. A live credential is marked used, at most once a minute.
func (s *Service) Authenticate(ctx context.Context, token string) (Authenticated, error) {
	switch {
	case ValidSecret(PrefixSession, token):
		return s.authenticateSession(ctx, token)
	case ValidSecret(PrefixPersonalToken, token):
		return s.authenticateCredential(ctx, token, CredentialPersonalToken)
	case ValidSecret(PrefixAPIKey, token):
		return s.authenticateCredential(ctx, token, CredentialAPIKey)
	}
	return Authenticated{}, ErrUnauthenticated
}

// authenticateSession resolves a session token to its user.
func (s *Service) authenticateSession(ctx context.Context, token string) (Authenticated, error) {
	session, user, err := s.store.Session(ctx, Hash(token))
	if errors.Is(err, ErrNotFound) {
		return Authenticated{}, ErrUnauthenticated
	}
	if err != nil {
		return Authenticated{}, fmt.Errorf("finding the session: %w", err)
	}
	now := s.now()
	if !session.Live(now) || user.Suspended {
		return Authenticated{}, ErrUnauthenticated
	}
	if now.Sub(session.LastSeenAt) >= touchEvery {
		if err := s.store.Touch(ctx, session.ID, now); err != nil {
			return Authenticated{}, fmt.Errorf("marking the session used: %w", err)
		}
		session.LastSeenAt = now
	}
	return Authenticated{User: user, Session: session}, nil
}

// SignOut revokes the session token names; a token that signs nobody in is no error, the browser is signed out
// either way.
func (s *Service) SignOut(ctx context.Context, token string) error {
	a, err := s.Authenticate(ctx, token)
	if errors.Is(err, ErrUnauthenticated) {
		return nil
	}
	if err != nil {
		return err
	}
	return s.Revoke(ctx, a.User.ID, a.Session.ID)
}

// Sessions lists user userID's live sessions, newest first.
func (s *Service) Sessions(ctx context.Context, userID uuid.UUID) ([]Session, error) {
	all, err := s.store.Sessions(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("listing the sessions: %w", err)
	}
	now := s.now()
	live := all[:0]
	for _, session := range all {
		if session.Live(now) {
			live = append(live, session)
		}
	}
	return live, nil
}

// Revoke revokes session id of user userID; the next request with its cookie fails.
func (s *Service) Revoke(ctx context.Context, userID, id uuid.UUID) error {
	if err := s.store.Revoke(ctx, userID, id, s.now()); err != nil {
		return fmt.Errorf("revoking the session: %w", err)
	}
	return nil
}

// DeleteExpired drops the sign-in flows and sessions that expired: the maintenance job's hourly task.
func (s *Service) DeleteExpired(ctx context.Context) (flows, sessions int64, err error) {
	flows, sessions, err = s.store.DeleteExpired(ctx, s.now())
	if err != nil {
		return 0, 0, fmt.Errorf("deleting expired rows: %w", err)
	}
	return flows, sessions, nil
}

// displayName is a new user's name: the provider's, or the part of the email before @, or "Someone".
func displayName(c Claims) string {
	if c.Name != "" {
		return c.Name
	}
	if local, _, ok := strings.Cut(c.Email, "@"); ok && local != "" {
		return local
	}
	return "Someone"
}

// randomText is 32 random bytes as URL-safe text: a state, a nonce, a PKCE verifier or a browser token.
func randomText() string {
	var b [32]byte
	_, _ = rand.Read(b[:]) // never fails, since Go 1.24
	return base64.RawURLEncoding.EncodeToString(b[:])
}

// PKCEChallenge is the S256 challenge of verifier, which the provider's adapter sends with the request.
func PKCEChallenge(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}
