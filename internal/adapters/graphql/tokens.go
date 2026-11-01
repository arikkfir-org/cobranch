package graphql

import (
	"context"
	"errors"
	"log/slog"

	"github.com/google/uuid"

	"cobranch.dev/internal/services/auth"
)

// personalToken is t as its user sees it.
func personalToken(t auth.Credential) *PersonalToken {
	return &PersonalToken{ID: t.ID.String(), Name: t.Name, Hint: t.Hint, Permissions: nonNil(t.Permissions), CreatedAt: t.CreatedAt,
		ExpiresAt: t.ExpiresAt, LastUsedAt: timeOrNil(t.LastUsedAt)}
}

// personalTokens lists the viewer's personal tokens that are not revoked.
func (r *Resolver) personalTokens(ctx context.Context) ([]*PersonalToken, error) {
	v, err := viewer(ctx)
	if err != nil {
		return nil, err
	}
	tokens, err := r.Sessions.Tokens(ctx, v.User.ID)
	if err != nil {
		return nil, fail(ctx, err, "token", "Listing the viewer's tokens")
	}
	return nonNil(mapAll(tokens, personalToken)), nil
}

// createPersonalToken makes a personal token for the viewer, from their session, and shows it once.
func (r *Resolver) createPersonalToken(ctx context.Context, input PersonalTokenInput) (*PersonalTokenCreated, error) {
	v, err := viewer(ctx)
	if err != nil {
		return nil, err
	}
	in := auth.TokenInput{Name: input.Name, Permissions: input.Permissions}
	if input.ExpiresAt != nil {
		in.ExpiresAt = *input.ExpiresAt
	}
	token, secret, err := r.Sessions.CreateToken(ctx, v, in)
	if errors.Is(err, auth.ErrForbidden) {
		return nil, refuse(ctx, forbidden, "token.session_required", "only a signed-in session makes a personal token, never a token or an API key")
	}
	if err != nil {
		return nil, fail(ctx, err, "token", "Making the token")
	}
	return &PersonalTokenCreated{Token: personalToken(token), Secret: secret}, nil
}

// revokePersonalToken revokes one of the viewer's personal tokens: false when the viewer has no such token.
func (r *Resolver) revokePersonalToken(ctx context.Context, id string) (bool, error) {
	v, err := viewer(ctx)
	if err != nil {
		return false, err
	}
	tokenID, err := uuid.Parse(id)
	if err != nil {
		return false, nil
	}
	err = r.Sessions.RevokeToken(ctx, v.User.ID, tokenID)
	switch {
	case errors.Is(err, auth.ErrNotFound):
		return false, nil
	case err != nil:
		slog.ErrorContext(ctx, "Revoking a token failed", "error", err)
		return false, errors.New("the token could not be revoked")
	}
	return true, nil
}
