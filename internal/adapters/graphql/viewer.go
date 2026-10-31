package graphql

import (
	"context"
	"errors"
	"time"

	"github.com/99designs/gqlgen/graphql"
	"github.com/vektah/gqlparser/v2/gqlerror"

	"cobranch.dev/internal/services/auth"
)

// unauthenticated is the error code of a field that needs a signed-in viewer and has Anonymous.
const unauthenticated = "UNAUTHENTICATED"

// viewer returns who the request signs in, or an UNAUTHENTICATED error for Anonymous.
func viewer(ctx context.Context) (auth.Authenticated, error) {
	v, ok := auth.Viewer(ctx)
	if !ok {
		return auth.Authenticated{}, &gqlerror.Error{
			Path: graphql.GetPath(ctx), Message: "sign in first", Extensions: map[string]any{"code": unauthenticated},
		}
	}
	return v, nil
}

// user is u as the API shows them; sessions, organizations and the rest resolve on their own.
func user(u auth.User) *User {
	emails := make([]*Email, 0, len(u.Emails))
	for _, e := range u.Emails {
		emails = append(emails, &Email{Email: e.Email, Verified: e.Verified})
	}
	return &User{ID: u.ID.String(), DisplayName: u.DisplayName, Emails: emails}
}

// sessions lists the viewer's live sessions, marking the request's own.
func (r *Resolver) sessions(ctx context.Context, v auth.Authenticated) ([]*Session, error) {
	all, err := r.Sessions.Sessions(ctx, v.User.ID)
	if err != nil {
		return nil, errors.New("the sessions could not be listed")
	}
	out := make([]*Session, 0, len(all))
	for _, s := range all {
		out = append(out, &Session{
			ID: s.ID.String(), CreatedAt: s.CreatedAt, LastSeenAt: s.LastSeenAt, ExpiresAt: minTime(s.IdleExpiresAt, s.ExpiresAt),
			UserAgent: s.UserAgent, IP: s.IP, Current: s.ID == v.Session.ID,
		})
	}
	return out, nil
}

// minTime is the earlier of a and b.
func minTime(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}
