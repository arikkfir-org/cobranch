package rest

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/google/uuid"

	"cobranch.dev/internal/adapters/server"
	"cobranch.dev/internal/services/auth"
)

// Sessions is what the API needs of sign-in's service: the viewer's sessions, personal tokens and SSH keys, and
// revoking one.
type Sessions interface {
	Sessions(ctx context.Context, userID uuid.UUID) ([]auth.Session, error)
	Revoke(ctx context.Context, userID, id uuid.UUID) error
	CreateToken(ctx context.Context, a auth.Authenticated, in auth.TokenInput) (auth.Credential, string, error)
	Tokens(ctx context.Context, userID uuid.UUID) ([]auth.Credential, error)
	RevokeToken(ctx context.Context, userID, id uuid.UUID) error
	AddSSHKey(ctx context.Context, a auth.Authenticated, in auth.SSHKeyInput) (auth.Credential, error)
	SSHKeys(ctx context.Context, userID uuid.UUID) ([]auth.Credential, error)
	DeleteSSHKey(ctx context.Context, userID, id uuid.UUID) error
}

// problem is a Problem for the request ctx belongs to.
func problem(ctx context.Context, status int, code, detail string) Problem {
	p := Problem{Type: "about:blank", Title: http.StatusText(status), Status: status, Code: code, RequestId: server.RequestID(ctx)}
	if detail != "" {
		p.Detail = &detail
	}
	return p
}

// unauthenticated is the problem of an operation that needs a signed-in viewer and has Anonymous.
func unauthenticated(ctx context.Context) ProblemApplicationProblemPlusJSONResponse {
	return ProblemApplicationProblemPlusJSONResponse(problem(ctx, http.StatusUnauthorized, "auth.unauthenticated", "Sign in first."))
}

// user is u as the API shows them, holding permissions at the installation.
func user(u auth.User, permissions []string) User {
	emails := make([]Email, 0, len(u.Emails))
	for _, e := range u.Emails {
		emails = append(emails, Email{Email: e.Email, Verified: e.Verified})
	}
	return User{Id: u.ID, DisplayName: u.DisplayName, Emails: emails, Permissions: nonNil(permissions)}
}

// GetMe answers who the request's credential signs in, with what they hold at the installation.
func (a api) GetMe(ctx context.Context, _ GetMeRequestObject) (GetMeResponseObject, error) {
	v, ok := auth.Viewer(ctx)
	if !ok {
		return GetMe401ApplicationProblemPlusJSONResponse{unauthenticated(ctx)}, nil
	}
	permissions, err := a.orgs.Permissions(ctx, v.User)
	if err != nil {
		return nil, fmt.Errorf("reading the permissions: %w", err)
	}
	return GetMe200JSONResponse(user(v.User, permissions)), nil
}

// ListMySessions lists the viewer's live sessions, newest first, marking the request's own.
func (a api) ListMySessions(ctx context.Context, _ ListMySessionsRequestObject) (ListMySessionsResponseObject, error) {
	v, ok := auth.Viewer(ctx)
	if !ok {
		return ListMySessions401ApplicationProblemPlusJSONResponse{unauthenticated(ctx)}, nil
	}
	all, err := a.sessions.Sessions(ctx, v.User.ID)
	if err != nil {
		return nil, fmt.Errorf("listing the sessions: %w", err)
	}
	sessions := make([]Session, 0, len(all))
	for _, s := range all {
		expires := s.ExpiresAt
		if s.IdleExpiresAt.Before(expires) {
			expires = s.IdleExpiresAt
		}
		sessions = append(sessions, Session{Id: s.ID, CreatedAt: s.CreatedAt, LastSeenAt: s.LastSeenAt, ExpiresAt: expires,
			UserAgent: s.UserAgent, Ip: s.IP, Current: s.ID == v.Session.ID})
	}
	return ListMySessions200JSONResponse{Sessions: sessions}, nil
}

// RevokeMySession revokes one of the viewer's sessions.
func (a api) RevokeMySession(ctx context.Context, req RevokeMySessionRequestObject) (RevokeMySessionResponseObject, error) {
	v, ok := auth.Viewer(ctx)
	if !ok {
		return RevokeMySession401ApplicationProblemPlusJSONResponse{unauthenticated(ctx)}, nil
	}
	err := a.sessions.Revoke(ctx, v.User.ID, req.Id)
	if errors.Is(err, auth.ErrNotFound) {
		return RevokeMySession404ApplicationProblemPlusJSONResponse(problem(ctx, http.StatusNotFound, "session.not_found", "You have no such session.")), nil
	}
	if err != nil {
		return nil, fmt.Errorf("revoking the session: %w", err)
	}
	return RevokeMySession204Response{}, nil
}
