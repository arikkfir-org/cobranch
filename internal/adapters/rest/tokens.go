package rest

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"cobranch.dev/internal/services/auth"
)

// personalToken is t as its user sees it.
func personalToken(t auth.Credential) PersonalToken {
	return PersonalToken{Id: t.ID, Name: t.Name, Hint: t.Hint, Permissions: nonNil(t.Permissions), CreatedAt: t.CreatedAt,
		ExpiresAt: t.ExpiresAt, LastUsedAt: timeOrNil(t.LastUsedAt)}
}

// ListMyTokens lists the viewer's personal tokens that are not revoked, newest first.
func (a api) ListMyTokens(ctx context.Context, _ ListMyTokensRequestObject) (ListMyTokensResponseObject, error) {
	v, ok := auth.Viewer(ctx)
	if !ok {
		return nil, errUnauthenticated
	}
	tokens, err := a.sessions.Tokens(ctx, v.User.ID)
	if err != nil {
		return nil, fmt.Errorf("listing the tokens: %w", err)
	}
	return ListMyTokens200JSONResponse{Tokens: nonNil(mapAll(tokens, personalToken))}, nil
}

// CreateMyToken makes a personal token for the viewer, from their session, and shows it once.
func (a api) CreateMyToken(ctx context.Context, req CreateMyTokenRequestObject) (CreateMyTokenResponseObject, error) {
	v, ok := auth.Viewer(ctx)
	if !ok {
		return nil, errUnauthenticated
	}
	in := auth.TokenInput{Name: req.Body.Name}
	if req.Body.Permissions != nil {
		in.Permissions = *req.Body.Permissions
	}
	if req.Body.ExpiresAt != nil {
		in.ExpiresAt = *req.Body.ExpiresAt
	}
	token, secret, err := a.sessions.CreateToken(ctx, v, in)
	if errors.Is(err, auth.ErrForbidden) {
		return nil, refuse(http.StatusForbidden, "token.session_required", "Only a signed-in session makes a personal token, never a token or an API key.")
	}
	if err != nil {
		return nil, fail(err, "token")
	}
	return CreateMyToken201JSONResponse{Token: personalToken(token), Secret: secret}, nil
}

// RevokeMyToken revokes one of the viewer's personal tokens.
func (a api) RevokeMyToken(ctx context.Context, req RevokeMyTokenRequestObject) (RevokeMyTokenResponseObject, error) {
	v, ok := auth.Viewer(ctx)
	if !ok {
		return nil, errUnauthenticated
	}
	err := a.sessions.RevokeToken(ctx, v.User.ID, req.Id)
	if errors.Is(err, auth.ErrNotFound) {
		return nil, refuse(http.StatusNotFound, "token.not_found", "You have no such token.")
	}
	if err != nil {
		return nil, fmt.Errorf("revoking the token: %w", err)
	}
	return RevokeMyToken204Response{}, nil
}
