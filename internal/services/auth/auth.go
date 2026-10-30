// Package auth is the domain of sign-in: identity providers, users and their identities, sign-in flows, sessions and
// the grants a sign-in makes. It knows nothing of HTTP, OpenID Connect's wire format or PostgreSQL: adapters bring
// those through its ports, Provider and Store (https://docs.dev.kfirs.com/cobranch/designs/sign-in-and-organizations.md).
package auth

import (
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// Kind is the kind of a principal. This slice makes users; Anonymous is built in; the other kinds come with their
// slices, as rows of the same table.
type Kind string

// The principal kinds.
const (
	KindUser      Kind = "user"
	KindAnonymous Kind = "anonymous"
)

// AnonymousID is the ID of Anonymous, the one built-in principal, which the first migration writes.
var AnonymousID = uuid.MustParse("00000000-0000-7000-8000-000000000001")

// The built-in roles a sign-in grants, by the IDs the first migration gives them.
var (
	RoleInstallationAdmin = uuid.MustParse("00000000-0000-7000-8000-000000000101")
	RoleInstallationUser  = uuid.MustParse("00000000-0000-7000-8000-000000000102")
)

// Source says what made a grant, so that a declarative source takes back only what it gave.
type Source string

// The grant sources.
const (
	// SourceGrant is a grant somebody made.
	SourceGrant Source = "grant"
	// SourceAdmins is Installation admin, granted at sign-in to an email the installation's values list.
	SourceAdmins Source = "admins"
	// SourceSelfServe is Installation user, granted at sign-up while self-serve organizations are on.
	SourceSelfServe Source = "self_serve"
)

// SeedIssuer is the issuer of the identities the demo data makes: no provider has it, and the Auth server signs in as
// one of them only while seeding is on.
const SeedIssuer = "cobranch:seed"

// Principal is an actor: whoever a credential resolves to, or Anonymous without one.
type Principal struct {
	ID          uuid.UUID
	Kind        Kind
	DisplayName string
	Suspended   bool
}

// Anonymous is the principal of a request with no credential.
var Anonymous = Principal{ID: AnonymousID, Kind: KindAnonymous, DisplayName: "Anonymous"}

// User is a person's principal, with the emails they hold.
type User struct {
	Principal
	Emails    []Email
	CreatedAt time.Time
}

// Email is an email a user holds, verified or not. A user holds one from sign-up, and more in a later slice.
type Email struct {
	Email    string
	Verified bool
}

// Seeded is a demo user the sign-in page offers while seeding is on: the subject of their SeedIssuer identity signs
// them in.
type Seeded struct {
	Subject     string
	DisplayName string
	Email       string
}

// Identity is a user at an identity provider: the provider's issuer and the subject it gave, never the email.
type Identity struct {
	Issuer        string
	Subject       string
	UserID        uuid.UUID
	Email         string
	EmailVerified bool
	Name          string
	LastSignInAt  time.Time
}

// Claims is what an identity provider says about the person who just signed in there.
type Claims struct {
	Issuer        string
	Subject       string
	Email         string
	EmailVerified bool
	// Name is the person's display name, or "" when the provider gave none.
	Name string
}

// Flow is a sign-in that began and waits for the provider's answer: a row, bound to its browser by a cookie.
type Flow struct {
	// StateHash is the hash of the state the provider echoes back, and the row's key.
	StateHash []byte
	// BrowserHash is the hash of the token the flow cookie carries.
	BrowserHash  []byte
	ProviderID   string
	Nonce        string
	PKCEVerifier string
	ReturnPath   string
	ExpiresAt    time.Time
}

// Session is a signed-in browser: a row whose token the cookie carries, hashed.
type Session struct {
	ID        uuid.UUID
	TokenHash []byte
	UserID    uuid.UUID
	// Issuer and Subject name the identity that signed in; empty for a seeded sign-in.
	Issuer, Subject string
	CreatedAt       time.Time
	LastSeenAt      time.Time
	IdleExpiresAt   time.Time
	ExpiresAt       time.Time
	RevokedAt       time.Time
	UserAgent       string
	IP              string
}

// Live reports whether the session signs its user in at now: not revoked, used lately and not aged out.
func (s Session) Live(now time.Time) bool {
	return s.RevokedAt.IsZero() && now.Before(s.IdleExpiresAt) && now.Before(s.ExpiresAt)
}

// Client is what a session records about the browser that made it.
type Client struct {
	UserAgent string
	IP        string
}

// Grant is a role given to a principal at a scope; this slice grants at the installation alone.
type Grant struct {
	RoleID uuid.UUID
	Source Source
}

// SignUp is a new user: the identity that signed up, its verified email if any, and the installation grants it gets.
type SignUp struct {
	Principal Principal
	Identity  Identity
	Grants    []Grant
}

// Policy is an installation's sign-up policy.
type Policy string

// The sign-up policies.
const (
	// PolicyAnyone lets any identity sign up.
	PolicyAnyone Policy = "anyone"
	// PolicyDomains lets a verified email in a listed domain sign up.
	PolicyDomains Policy = "domains"
	// PolicyInvited lets only a verified email with a pending invitation sign up.
	PolicyInvited Policy = "invited"
)

// ParsePolicy reads a policy from its name.
func ParsePolicy(s string) (Policy, error) {
	switch p := Policy(s); p {
	case PolicyAnyone, PolicyDomains, PolicyInvited:
		return p, nil
	default:
		return "", fmt.Errorf("%q is not a sign-up policy: anyone, domains or invited", s)
	}
}

// The errors the domain answers; an adapter maps each to its status.
var (
	// ErrNotFound is a row that isn't there.
	ErrNotFound = errors.New("not found")
	// ErrUnknownProvider is a provider the installation doesn't list.
	ErrUnknownProvider = errors.New("unknown identity provider")
	// ErrFlow is a sign-in that can't be finished: late, used, or begun in another browser.
	ErrFlow = errors.New("the sign-in is late, was used, or began in another browser")
	// ErrProvider is an identity provider that didn't sign the person in.
	ErrProvider = errors.New("the identity provider refused the sign-in")
	// ErrSignUpRefused is a new identity the sign-up policy doesn't admit.
	ErrSignUpRefused = errors.New("the sign-up policy refuses this identity")
	// ErrUnauthenticated is a credential that signs nobody in.
	ErrUnauthenticated = errors.New("the credential signs nobody in")
	// ErrSeedOff is a seeded sign-in on an installation whose seeding is off.
	ErrSeedOff = errors.New("seeded sign-in is off")
)

// EmailTakenError is a new identity whose verified email another user already holds; Issuer names the provider that
// user signs in with, for the page to say.
type EmailTakenError struct {
	Email  string
	Issuer string
}

func (e *EmailTakenError) Error() string {
	return fmt.Sprintf("another user holds %s, signed in through %s", e.Email, e.Issuer)
}

// Is makes an EmailTakenError an ErrSignUpRefused too.
func (e *EmailTakenError) Is(target error) bool {
	return target == ErrSignUpRefused
}
