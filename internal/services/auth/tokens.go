package auth

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Lifetimes of a personal token, the principals design's: every token ends.
const (
	// TokenLifetime is how long a personal token lasts unless its user sets an end.
	TokenLifetime = 30 * 24 * time.Hour
	// TokenMaxLifetime is the latest end a personal token may have, from when it is made.
	TokenMaxLifetime = 365 * 24 * time.Hour
	// clockSkew is how far ahead of the server's a client's clock may run: a Console that asks for a year from its own
	// now still gets one.
	clockSkew = 5 * time.Minute
)

// DefaultTokenPermissions are what a personal token may use unless its user lists others: reading and pushing to every
// repository its user reaches, which is what a Git client needs.
var DefaultTokenPermissions = []string{"repo.push", "repo.read"}

// TokenInput is what makes a personal token: its name, the permissions it may use (DefaultTokenPermissions when
// empty), and when it ends (TokenLifetime from now when zero).
type TokenInput struct {
	Name        string
	Permissions []string
	ExpiresAt   time.Time
}

// ValidateToken normalizes in at now, or refuses it: a name of 1 to 100 characters, permissions of the catalog, and an
// end after now and no later than TokenMaxLifetime from it.
func ValidateToken(in TokenInput, now time.Time) (TokenInput, error) {
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" || len(in.Name) > 100 {
		return in, &ValidationError{Field: "name", Code: "token.name_invalid", Message: "a name is 1 to 100 characters"}
	}
	if len(in.Permissions) == 0 {
		in.Permissions = DefaultTokenPermissions
	}
	catalog := Permissions()
	for _, p := range in.Permissions {
		if !slices.Contains(catalog, p) {
			return in, &ValidationError{Field: "permissions", Code: "token.permission_unknown", Message: fmt.Sprintf("%q is no permission", p)}
		}
	}
	in.Permissions = slices.Compact(slices.Sorted(slices.Values(in.Permissions)))
	switch {
	case in.ExpiresAt.IsZero():
		in.ExpiresAt = now.Add(TokenLifetime)
	case !in.ExpiresAt.After(now):
		return in, &ValidationError{Field: "expiresAt", Code: "token.expiry_invalid", Message: "the end must be in the future"}
	case in.ExpiresAt.After(now.Add(TokenMaxLifetime + clockSkew)):
		return in, &ValidationError{Field: "expiresAt", Code: "token.expiry_invalid", Message: "a token ends within a year"}
	}
	return in, nil
}

// CreateToken makes a personal token for the user a signs in, from a session only: never from another token or an API
// key's secret, so a token never outlives or outgrows the sign-in behind it. The token keeps the session's identity;
// CreateToken returns it with its secret, which is shown this once.
func (s *Service) CreateToken(ctx context.Context, a Authenticated, in TokenInput) (Credential, string, error) {
	if a.Session.ID == uuid.Nil || a.User.Kind != KindUser {
		return Credential{}, "", fmt.Errorf("%w: only a signed-in user makes a personal token, never a token or a key", ErrForbidden)
	}
	now := s.now()
	in, err := ValidateToken(in, now)
	if err != nil {
		return Credential{}, "", err
	}
	id, err := uuid.NewV7()
	if err != nil {
		return Credential{}, "", fmt.Errorf("making the token's ID: %w", err)
	}
	secret := NewSecret(PrefixPersonalToken)
	token := Credential{
		ID: id, Kind: CredentialPersonalToken, PrincipalID: a.User.ID, Hint: Hint(secret), CreatedAt: now, CreatedBy: a.User.ID,
		ExpiresAt: in.ExpiresAt, Name: in.Name, Permissions: in.Permissions, Issuer: a.Session.Issuer, Subject: a.Session.Subject,
	}
	if err := s.store.CreateToken(ctx, token, Hash(secret)); err != nil {
		return Credential{}, "", fmt.Errorf("recording the token: %w", err)
	}
	return token, secret, nil
}

// Tokens lists user userID's personal tokens that are not revoked, expired ones included, newest first.
func (s *Service) Tokens(ctx context.Context, userID uuid.UUID) ([]Credential, error) {
	tokens, err := s.store.Tokens(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("listing the tokens: %w", err)
	}
	return tokens, nil
}

// RevokeToken revokes personal token id of user userID, or ErrNotFound; the next request with it fails.
func (s *Service) RevokeToken(ctx context.Context, userID, id uuid.UUID) error {
	if err := s.store.RevokeToken(ctx, userID, id, s.now()); err != nil {
		return fmt.Errorf("revoking the token: %w", err)
	}
	return nil
}

// authenticateCredential resolves a credential's secret of kind to whom it acts as: an API key's to the key, a
// personal token to its user.
func (s *Service) authenticateCredential(ctx context.Context, token string, kind CredentialKind) (Authenticated, error) {
	credential, user, err := s.store.Credential(ctx, Hash(token))
	if errors.Is(err, ErrNotFound) {
		return Authenticated{}, ErrUnauthenticated
	}
	if err != nil {
		return Authenticated{}, fmt.Errorf("finding the credential: %w", err)
	}
	now := s.now()
	if credential.Kind != kind || !credential.Live(now) || user.Suspended {
		return Authenticated{}, ErrUnauthenticated
	}
	if now.Sub(credential.LastUsedAt) >= touchEvery {
		if err := s.store.TouchCredential(ctx, credential.ID, now); err != nil {
			return Authenticated{}, fmt.Errorf("marking the credential used: %w", err)
		}
		credential.LastUsedAt = now
	}
	return Authenticated{User: user, Credential: credential}, nil
}
