package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"cobranch.dev/internal/adapters/postgres/queries"
	"cobranch.dev/internal/services/auth"
)

// uniqueViolation is PostgreSQL's error code for a row that a unique constraint refuses.
const uniqueViolation = "23505"

// AuthStore keeps sign-in's rows (db/migrations/00002_sign_in.sql): auth.Store on PostgreSQL, through the queries sqlc
// generates from db/queries.
type AuthStore struct {
	pool *pgxpool.Pool
	q    *queries.Queries
}

// NewAuthStore returns sign-in's store on pool's database.
func NewAuthStore(pool *pgxpool.Pool) *AuthStore {
	return &AuthStore{pool: pool, q: queries.New(pool)}
}

var _ auth.Store = (*AuthStore)(nil)

// CreateFlow records a sign-in that began.
func (s *AuthStore) CreateFlow(ctx context.Context, f auth.Flow) error {
	err := s.q.CreateFlow(ctx, queries.CreateFlowParams{
		StateHash:    f.StateHash,
		BrowserHash:  f.BrowserHash,
		ProviderID:   f.ProviderID,
		Nonce:        f.Nonce,
		PkceVerifier: f.PKCEVerifier,
		ReturnPath:   f.ReturnPath,
		ExpiresAt:    f.ExpiresAt,
	})
	if err != nil {
		return fmt.Errorf("inserting the sign-in flow: %w", err)
	}
	return nil
}

// TakeFlow deletes the flow stateHash names and returns it, so each is finished once.
func (s *AuthStore) TakeFlow(ctx context.Context, stateHash []byte) (auth.Flow, error) {
	f, err := s.q.TakeFlow(ctx, stateHash)
	if err != nil {
		return auth.Flow{}, wrap("taking the sign-in flow", err)
	}
	return auth.Flow{
		StateHash:    f.StateHash,
		BrowserHash:  f.BrowserHash,
		ProviderID:   f.ProviderID,
		Nonce:        f.Nonce,
		PKCEVerifier: f.PkceVerifier,
		ReturnPath:   f.ReturnPath,
		ExpiresAt:    f.ExpiresAt,
	}, nil
}

// identity is the domain's view of an identities row.
func identity(i queries.Identity) auth.Identity {
	return auth.Identity{
		Issuer:        i.Issuer,
		Subject:       i.Subject,
		UserID:        i.PrincipalID,
		Email:         i.Email,
		EmailVerified: i.EmailVerified,
		Name:          i.Name,
		LastSignInAt:  i.LastSignInAt,
	}
}

// Identity finds who issuer and subject signed in as.
func (s *AuthStore) Identity(ctx context.Context, issuer, subject string) (auth.Identity, error) {
	i, err := s.q.Identity(ctx, issuer, subject)
	if err != nil {
		return auth.Identity{}, wrap("reading the identity", err)
	}
	return identity(i), nil
}

// EmailHolder finds the user who holds email, verified, and the issuer of one of their identities.
func (s *AuthStore) EmailHolder(ctx context.Context, email string) (uuid.UUID, string, error) {
	h, err := s.q.EmailHolder(ctx, email)
	if err != nil {
		return uuid.Nil, "", wrap("finding the email's holder", err)
	}
	return h.PrincipalID, h.Issuer, nil
}

// SignUp creates a user, their identity, their verified email if any, their installation grants and the event.
func (s *AuthStore) SignUp(ctx context.Context, up auth.SignUp) error {
	p, i := up.Principal, up.Identity
	return s.transact(ctx, "signing up", p.ID, func(q *queries.Queries) error {
		if err := q.InsertPrincipal(ctx, queries.InsertPrincipalParams{
			ID: p.ID, Kind: string(p.Kind), DisplayName: p.DisplayName, CreatedAt: i.LastSignInAt,
		}); err != nil {
			return fmt.Errorf("inserting the user: %w", err)
		}
		if err := q.InsertIdentity(ctx, queries.InsertIdentityParams{
			Issuer: i.Issuer, Subject: i.Subject, PrincipalID: p.ID, Email: i.Email, EmailVerified: i.EmailVerified,
			Name: i.Name, SignedInAt: i.LastSignInAt,
		}); err != nil {
			return fmt.Errorf("inserting the identity: %w", err)
		}
		if i.EmailVerified && i.Email != "" {
			if err := q.InsertEmail(ctx, i.Email, p.ID, true); err != nil {
				return fmt.Errorf("inserting the email: %w", err)
			}
		}
		if err := event(ctx, q, p.ID, "user.signed_up", p.ID, map[string]any{"issuer": i.Issuer}); err != nil {
			return err
		}
		for _, g := range up.Grants {
			if err := grant(ctx, q, p.ID, g); err != nil {
				return err
			}
		}
		return nil
	})
}

// SignIn records the identity's latest claims and sign-in, sets Installation admin from the installation's list,
// and creates the session and its event.
func (s *AuthStore) SignIn(ctx context.Context, i auth.Identity, admin bool, session auth.Session) error {
	return s.transact(ctx, "signing in", i.UserID, func(q *queries.Queries) error {
		updated, err := q.UpdateIdentity(ctx, queries.UpdateIdentityParams{
			Issuer: i.Issuer, Subject: i.Subject, Email: i.Email, EmailVerified: i.EmailVerified, Name: i.Name,
			SignedInAt: i.LastSignInAt,
		})
		if err != nil {
			return fmt.Errorf("updating the identity: %w", err)
		}
		if updated == 0 {
			return auth.ErrNotFound
		}
		if err := setAdmin(ctx, q, i.UserID, admin); err != nil {
			return err
		}
		if err := q.InsertSession(ctx, queries.InsertSessionParams{
			ID:            session.ID,
			TokenHash:     session.TokenHash,
			PrincipalID:   session.UserID,
			Issuer:        session.Issuer,
			Subject:       session.Subject,
			CreatedAt:     session.CreatedAt,
			LastSeenAt:    session.LastSeenAt,
			IdleExpiresAt: session.IdleExpiresAt,
			ExpiresAt:     session.ExpiresAt,
			UserAgent:     session.UserAgent,
			IP:            session.IP,
		}); err != nil {
			return fmt.Errorf("inserting the session: %w", err)
		}
		return event(ctx, q, i.UserID, "user.signed_in", session.ID, map[string]any{"issuer": i.Issuer})
	})
}

// setAdmin gives userID Installation admin from the installation's list, or takes back the grant that list gave,
// never one somebody made.
func setAdmin(ctx context.Context, q *queries.Queries, userID uuid.UUID, admin bool) error {
	if admin {
		return grant(ctx, q, userID, auth.Grant{RoleID: auth.RoleInstallationAdmin, Source: auth.SourceAdmins})
	}
	deleted, err := q.DeleteGrant(ctx, userID, auth.RoleInstallationAdmin, string(auth.SourceAdmins))
	if err != nil {
		return fmt.Errorf("taking the admin grant back: %w", err)
	}
	if deleted > 0 {
		return event(ctx, q, userID, "grant.removed", userID,
			map[string]any{"role": auth.RoleInstallationAdmin, "source": auth.SourceAdmins})
	}
	return nil
}

// grant gives principalID g at the installation, once: a grant it already has is kept as it is.
func grant(ctx context.Context, q *queries.Queries, principalID uuid.UUID, g auth.Grant) error {
	inserted, err := q.InsertInstallationGrant(ctx, principalID, g.RoleID, string(g.Source))
	if err != nil {
		return fmt.Errorf("inserting the grant: %w", err)
	}
	if inserted == 0 {
		return nil
	}
	return event(ctx, q, principalID, "grant.made", principalID, map[string]any{"role": g.RoleID, "source": g.Source})
}

// event appends to the audit log.
func event(ctx context.Context, q *queries.Queries, actorID uuid.UUID, kind string, subjectID uuid.UUID, details map[string]any) error {
	if details == nil {
		details = map[string]any{}
	}
	encoded, err := json.Marshal(details)
	if err != nil {
		return fmt.Errorf("encoding the event's details: %w", err)
	}
	if err := q.InsertEvent(ctx, queries.InsertEventParams{
		ActorID: actorID, Kind: kind, SubjectID: &subjectID, Details: encoded,
	}); err != nil {
		return fmt.Errorf("recording the event: %w", err)
	}
	return nil
}

// sessionOf is the domain's view of a sessions row.
func sessionOf(m queries.Session) auth.Session {
	s := auth.Session{
		ID:            m.ID,
		TokenHash:     m.TokenHash,
		UserID:        m.PrincipalID,
		Issuer:        m.Issuer,
		Subject:       m.Subject,
		CreatedAt:     m.CreatedAt,
		LastSeenAt:    m.LastSeenAt,
		IdleExpiresAt: m.IdleExpiresAt,
		ExpiresAt:     m.ExpiresAt,
		UserAgent:     m.UserAgent,
		IP:            m.IP,
	}
	if m.RevokedAt != nil {
		s.RevokedAt = *m.RevokedAt
	}
	return s
}

// Session finds a session by its token's hash, with its user.
func (s *AuthStore) Session(ctx context.Context, tokenHash []byte) (auth.Session, auth.User, error) {
	row, err := s.q.SessionByToken(ctx, tokenHash)
	if err != nil {
		return auth.Session{}, auth.User{}, wrap("reading the session", err)
	}
	p := row.Principal
	u := auth.User{
		Principal: auth.Principal{ID: p.ID, Kind: auth.Kind(p.Kind), DisplayName: p.DisplayName, Suspended: p.Suspended},
		CreatedAt: p.CreatedAt,
	}
	emails, err := s.q.Emails(ctx, u.ID)
	if err != nil {
		return auth.Session{}, auth.User{}, fmt.Errorf("listing the user's emails: %w", err)
	}
	for _, e := range emails {
		u.Emails = append(u.Emails, auth.Email{Email: e.Email, Verified: e.Verified})
	}
	return sessionOf(row.Session), u, nil
}

// Touch records that session id was used at seen.
func (s *AuthStore) Touch(ctx context.Context, id uuid.UUID, seen time.Time) error {
	if err := s.q.TouchSession(ctx, seen, seen.Add(auth.SessionIdle), id); err != nil {
		return fmt.Errorf("marking the session used: %w", err)
	}
	return nil
}

// Sessions lists a user's sessions that are not revoked, newest first.
func (s *AuthStore) Sessions(ctx context.Context, userID uuid.UUID) ([]auth.Session, error) {
	rows, err := s.q.Sessions(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("listing the sessions: %w", err)
	}
	var sessions []auth.Session
	for _, m := range rows {
		sessions = append(sessions, sessionOf(m))
	}
	return sessions, nil
}

// Revoke revokes session id of user userID at now.
func (s *AuthStore) Revoke(ctx context.Context, userID, id uuid.UUID, now time.Time) error {
	return s.transact(ctx, "revoking the session", userID, func(q *queries.Queries) error {
		revoked, err := q.RevokeSession(ctx, now, id, userID)
		if err != nil {
			return fmt.Errorf("updating the session: %w", err)
		}
		if revoked == 0 {
			return auth.ErrNotFound
		}
		return event(ctx, q, userID, "session.revoked", id, nil)
	})
}

// SeededUsers lists the demo data's users: those with an identity of auth.SeedIssuer, by display name.
func (s *AuthStore) SeededUsers(ctx context.Context) ([]auth.Seeded, error) {
	rows, err := s.q.SeededUsers(ctx, auth.SeedIssuer)
	if err != nil {
		return nil, fmt.Errorf("listing the seeded users: %w", err)
	}
	var users []auth.Seeded
	for _, r := range rows {
		users = append(users, auth.Seeded{Subject: r.Subject, DisplayName: r.DisplayName, Email: r.Email})
	}
	return users, nil
}

// SeededUser finds a demo user's identity by its subject.
func (s *AuthStore) SeededUser(ctx context.Context, subject string) (auth.Identity, error) {
	return s.Identity(ctx, auth.SeedIssuer, subject)
}

// DeleteExpired deletes the flows and sessions that expired before now.
func (s *AuthStore) DeleteExpired(ctx context.Context, now time.Time) (flows, sessions int64, err error) {
	if flows, err = s.q.DeleteExpiredFlows(ctx, now); err != nil {
		return 0, 0, fmt.Errorf("deleting expired sign-in flows: %w", err)
	}
	if sessions, err = s.q.DeleteExpiredSessions(ctx, now); err != nil {
		return 0, 0, fmt.Errorf("deleting expired sessions: %w", err)
	}
	return flows, sessions, nil
}

// wrap returns auth.ErrNotFound for a query that found no row, and err in what's context otherwise.
func wrap(what string, err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return auth.ErrNotFound
	}
	return fmt.Errorf("%s: %w", what, err)
}

// transact runs fn with the queries in a transaction that acts for actor, committed when it returns nil and rolled
// back otherwise; a unique constraint it hits is a row that is already there. The actor goes in session variable
// cobranch.principal_id, local to the transaction, so trigger audit() records who changed each row
// (db/migrations/00003_audit.sql); uuid.Nil leaves it unset, for a change of Cobranch's own.
func (s *AuthStore) transact(ctx context.Context, what string, actor uuid.UUID, fn func(q *queries.Queries) error) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("%s: beginning the transaction: %w", what, err)
	}
	defer func() { _ = tx.Rollback(ctx) }() // a no-op after Commit
	q := s.q.WithTx(tx)
	if actor != uuid.Nil {
		if err := q.SetPrincipal(ctx, actor.String()); err != nil {
			return fmt.Errorf("%s: naming the principal: %w", what, err)
		}
	}
	if err := fn(q); err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == uniqueViolation {
			return fmt.Errorf("%s: a row is already there (%s): %w", what, pgErr.ConstraintName, err)
		}
		return fmt.Errorf("%s: %w", what, err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("%s: committing: %w", what, err)
	}
	return nil
}
