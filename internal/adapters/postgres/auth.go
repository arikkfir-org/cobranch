package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"cobranch.dev/internal/services/auth"
)

// uniqueViolation is PostgreSQL's error code for a row that a unique constraint refuses.
const uniqueViolation = "23505"

// AuthStore keeps sign-in's rows (db/migrations/00002_sign_in.sql): auth.Store on PostgreSQL.
type AuthStore struct {
	pool *pgxpool.Pool
}

// NewAuthStore returns sign-in's store on pool's database.
func NewAuthStore(pool *pgxpool.Pool) *AuthStore {
	return &AuthStore{pool: pool}
}

var _ auth.Store = (*AuthStore)(nil)

// CreateFlow records a sign-in that began.
func (s *AuthStore) CreateFlow(ctx context.Context, f auth.Flow) error {
	_, err := s.pool.Exec(ctx, `INSERT INTO sign_in_flows
		(state_hash, browser_hash, provider_id, nonce, pkce_verifier, return_path, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		f.StateHash, f.BrowserHash, f.ProviderID, f.Nonce, f.PKCEVerifier, f.ReturnPath, f.ExpiresAt)
	if err != nil {
		return fmt.Errorf("inserting the sign-in flow: %w", err)
	}
	return nil
}

// TakeFlow deletes the flow stateHash names and returns it, so each is finished once.
func (s *AuthStore) TakeFlow(ctx context.Context, stateHash []byte) (auth.Flow, error) {
	var f auth.Flow
	err := s.pool.QueryRow(ctx, `DELETE FROM sign_in_flows WHERE state_hash = $1
		RETURNING state_hash, browser_hash, provider_id, nonce, pkce_verifier, return_path, expires_at`, stateHash).
		Scan(&f.StateHash, &f.BrowserHash, &f.ProviderID, &f.Nonce, &f.PKCEVerifier, &f.ReturnPath, &f.ExpiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return auth.Flow{}, auth.ErrNotFound
	}
	if err != nil {
		return auth.Flow{}, fmt.Errorf("taking the sign-in flow: %w", err)
	}
	return f, nil
}

const identityColumns = "issuer, subject, principal_id, email, email_verified, name, last_sign_in_at"

func scanIdentity(row pgx.Row) (auth.Identity, error) {
	var i auth.Identity
	err := row.Scan(&i.Issuer, &i.Subject, &i.UserID, &i.Email, &i.EmailVerified, &i.Name, &i.LastSignInAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return auth.Identity{}, auth.ErrNotFound
	}
	if err != nil {
		return auth.Identity{}, fmt.Errorf("reading the identity: %w", err)
	}
	return i, nil
}

// Identity finds who issuer and subject signed in as.
func (s *AuthStore) Identity(ctx context.Context, issuer, subject string) (auth.Identity, error) {
	return scanIdentity(s.pool.QueryRow(ctx,
		"SELECT "+identityColumns+" FROM identities WHERE issuer = $1 AND subject = $2", issuer, subject))
}

// EmailHolder finds the user who holds email, verified, and the issuer of one of their identities.
func (s *AuthStore) EmailHolder(ctx context.Context, email string) (uuid.UUID, string, error) {
	var id uuid.UUID
	var issuer string
	err := s.pool.QueryRow(ctx, `SELECT e.principal_id, coalesce((SELECT i.issuer FROM identities i
		WHERE i.principal_id = e.principal_id ORDER BY i.last_sign_in_at DESC LIMIT 1), '')
		FROM principal_emails e WHERE e.email = $1 AND e.verified`, email).Scan(&id, &issuer)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, "", auth.ErrNotFound
	}
	if err != nil {
		return uuid.Nil, "", fmt.Errorf("finding the email's holder: %w", err)
	}
	return id, issuer, nil
}

// SignUp creates a user, their identity, their verified email if any, their installation grants and the event.
func (s *AuthStore) SignUp(ctx context.Context, up auth.SignUp) error {
	return s.transact(ctx, "signing up", func(tx pgx.Tx) error {
		p, i := up.Principal, up.Identity
		if _, err := tx.Exec(ctx, "INSERT INTO principals (id, kind, display_name, created_at) VALUES ($1, $2, $3, $4)",
			p.ID, p.Kind, p.DisplayName, i.LastSignInAt); err != nil {
			return fmt.Errorf("inserting the user: %w", err)
		}
		if _, err := tx.Exec(ctx, `INSERT INTO identities (`+identityColumns+`, created_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $7)`,
			i.Issuer, i.Subject, p.ID, i.Email, i.EmailVerified, i.Name, i.LastSignInAt); err != nil {
			return fmt.Errorf("inserting the identity: %w", err)
		}
		if i.EmailVerified && i.Email != "" {
			if _, err := tx.Exec(ctx, "INSERT INTO principal_emails (email, principal_id, verified) VALUES ($1, $2, true)",
				i.Email, p.ID); err != nil {
				return fmt.Errorf("inserting the email: %w", err)
			}
		}
		if err := event(ctx, tx, p.ID, "user.signed_up", p.ID, map[string]any{"issuer": i.Issuer}); err != nil {
			return err
		}
		for _, g := range up.Grants {
			if err := grant(ctx, tx, p.ID, g); err != nil {
				return err
			}
		}
		return nil
	})
}

// SignIn records the identity's latest claims and sign-in, sets Installation admin from the installation's list,
// and creates the session and its event.
func (s *AuthStore) SignIn(ctx context.Context, i auth.Identity, admin bool, session auth.Session) error {
	return s.transact(ctx, "signing in", func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE identities SET email = $3, email_verified = $4, name = $5, last_sign_in_at = $6
			WHERE issuer = $1 AND subject = $2`, i.Issuer, i.Subject, i.Email, i.EmailVerified, i.Name, i.LastSignInAt)
		if err != nil {
			return fmt.Errorf("updating the identity: %w", err)
		}
		if tag.RowsAffected() == 0 {
			return auth.ErrNotFound
		}
		if err := setAdmin(ctx, tx, i.UserID, admin); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO sessions (id, token_hash, principal_id, issuer, subject, created_at,
			last_seen_at, idle_expires_at, expires_at, user_agent, ip) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)`,
			session.ID, session.TokenHash, session.UserID, session.Issuer, session.Subject, session.CreatedAt,
			session.LastSeenAt, session.IdleExpiresAt, session.ExpiresAt, session.UserAgent, session.IP); err != nil {
			return fmt.Errorf("inserting the session: %w", err)
		}
		return event(ctx, tx, i.UserID, "user.signed_in", session.ID, map[string]any{"issuer": i.Issuer})
	})
}

// setAdmin gives userID Installation admin from the installation's list, or takes back the grant that list gave,
// never one somebody made.
func setAdmin(ctx context.Context, tx pgx.Tx, userID uuid.UUID, admin bool) error {
	if admin {
		return grant(ctx, tx, userID, auth.Grant{RoleID: auth.RoleInstallationAdmin, Source: auth.SourceAdmins})
	}
	tag, err := tx.Exec(ctx, "DELETE FROM grants WHERE principal_id = $1 AND role_id = $2 AND source = $3",
		userID, auth.RoleInstallationAdmin, auth.SourceAdmins)
	if err != nil {
		return fmt.Errorf("taking the admin grant back: %w", err)
	}
	if tag.RowsAffected() > 0 {
		return event(ctx, tx, userID, "grant.removed", userID,
			map[string]any{"role": auth.RoleInstallationAdmin, "source": auth.SourceAdmins})
	}
	return nil
}

// grant gives principalID g at the installation, once: a grant it already has is kept as it is.
func grant(ctx context.Context, tx pgx.Tx, principalID uuid.UUID, g auth.Grant) error {
	tag, err := tx.Exec(ctx, `INSERT INTO grants (principal_id, role_id, scope, source) VALUES ($1, $2, 'installation', $3)
		ON CONFLICT DO NOTHING`, principalID, g.RoleID, g.Source)
	if err != nil {
		return fmt.Errorf("inserting the grant: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return nil
	}
	return event(ctx, tx, principalID, "grant.made", principalID, map[string]any{"role": g.RoleID, "source": g.Source})
}

// event appends to the audit log.
func event(ctx context.Context, tx pgx.Tx, actorID uuid.UUID, kind string, subjectID uuid.UUID, details map[string]any) error {
	if details == nil {
		details = map[string]any{}
	}
	if _, err := tx.Exec(ctx, "INSERT INTO events (actor_id, kind, subject_id, details) VALUES ($1, $2, $3, $4)",
		actorID, kind, subjectID, details); err != nil {
		return fmt.Errorf("recording the event: %w", err)
	}
	return nil
}

const sessionColumns = `s.id, s.token_hash, s.principal_id, s.issuer, s.subject, s.created_at, s.last_seen_at,
	s.idle_expires_at, s.expires_at, coalesce(s.revoked_at, '0001-01-01'), s.user_agent, s.ip`

func scanSession(row pgx.Row, dest ...any) (auth.Session, error) {
	var s auth.Session
	err := row.Scan(append([]any{&s.ID, &s.TokenHash, &s.UserID, &s.Issuer, &s.Subject, &s.CreatedAt, &s.LastSeenAt,
		&s.IdleExpiresAt, &s.ExpiresAt, &s.RevokedAt, &s.UserAgent, &s.IP}, dest...)...)
	if errors.Is(err, pgx.ErrNoRows) {
		return auth.Session{}, auth.ErrNotFound
	}
	if err != nil {
		return auth.Session{}, fmt.Errorf("reading the session: %w", err)
	}
	if s.RevokedAt.Year() == 1 {
		s.RevokedAt = time.Time{}
	}
	return s, nil
}

// Session finds a session by its token's hash, with its user.
func (s *AuthStore) Session(ctx context.Context, tokenHash []byte) (auth.Session, auth.User, error) {
	var u auth.User
	session, err := scanSession(s.pool.QueryRow(ctx, `SELECT `+sessionColumns+`,
		p.id, p.kind, p.display_name, p.suspended, p.created_at
		FROM sessions s JOIN principals p ON p.id = s.principal_id WHERE s.token_hash = $1`, tokenHash),
		&u.ID, &u.Kind, &u.DisplayName, &u.Suspended, &u.CreatedAt)
	if err != nil {
		return auth.Session{}, auth.User{}, err
	}
	if u.Emails, err = s.emails(ctx, u.ID); err != nil {
		return auth.Session{}, auth.User{}, err
	}
	return session, u, nil
}

// emails lists the emails user id holds.
func (s *AuthStore) emails(ctx context.Context, id uuid.UUID) ([]auth.Email, error) {
	rows, _ := s.pool.Query(ctx, "SELECT email, verified FROM principal_emails WHERE principal_id = $1 ORDER BY created_at", id)
	emails, err := pgx.CollectRows(rows, pgx.RowToStructByPos[auth.Email])
	if err != nil {
		return nil, fmt.Errorf("listing the user's emails: %w", err)
	}
	return emails, nil
}

// Touch records that session id was used at seen.
func (s *AuthStore) Touch(ctx context.Context, id uuid.UUID, seen time.Time) error {
	if _, err := s.pool.Exec(ctx, "UPDATE sessions SET last_seen_at = $2, idle_expires_at = $3 WHERE id = $1",
		id, seen, seen.Add(auth.SessionIdle)); err != nil {
		return fmt.Errorf("marking the session used: %w", err)
	}
	return nil
}

// Sessions lists a user's sessions that are not revoked, newest first.
func (s *AuthStore) Sessions(ctx context.Context, userID uuid.UUID) ([]auth.Session, error) {
	rows, err := s.pool.Query(ctx, "SELECT "+sessionColumns+" FROM sessions s WHERE s.principal_id = $1 AND s.revoked_at IS NULL ORDER BY s.created_at DESC", userID)
	if err != nil {
		return nil, fmt.Errorf("listing the sessions: %w", err)
	}
	defer rows.Close()
	var sessions []auth.Session
	for rows.Next() {
		session, err := scanSession(rows)
		if err != nil {
			return nil, err
		}
		sessions = append(sessions, session)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("listing the sessions: %w", err)
	}
	return sessions, nil
}

// Revoke revokes session id of user userID at now.
func (s *AuthStore) Revoke(ctx context.Context, userID, id uuid.UUID, now time.Time) error {
	return s.transact(ctx, "revoking the session", func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, "UPDATE sessions SET revoked_at = $3 WHERE id = $1 AND principal_id = $2 AND revoked_at IS NULL",
			id, userID, now)
		if err != nil {
			return fmt.Errorf("updating the session: %w", err)
		}
		if tag.RowsAffected() == 0 {
			return auth.ErrNotFound
		}
		return event(ctx, tx, userID, "session.revoked", id, nil)
	})
}

// SeededUsers lists the demo data's users: those with an identity of auth.SeedIssuer, by display name.
func (s *AuthStore) SeededUsers(ctx context.Context) ([]auth.Seeded, error) {
	rows, _ := s.pool.Query(ctx, `SELECT i.subject, p.display_name, i.email
		FROM principals p JOIN identities i ON i.principal_id = p.id WHERE i.issuer = $1 ORDER BY p.display_name`, auth.SeedIssuer)
	users, err := pgx.CollectRows(rows, pgx.RowToStructByPos[auth.Seeded])
	if err != nil {
		return nil, fmt.Errorf("listing the seeded users: %w", err)
	}
	return users, nil
}

// SeededUser finds a demo user's identity by its subject.
func (s *AuthStore) SeededUser(ctx context.Context, subject string) (auth.Identity, error) {
	return s.Identity(ctx, auth.SeedIssuer, subject)
}

// DeleteExpired deletes the flows and sessions that expired before now.
func (s *AuthStore) DeleteExpired(ctx context.Context, now time.Time) (flows, sessions int64, err error) {
	tag, err := s.pool.Exec(ctx, "DELETE FROM sign_in_flows WHERE expires_at < $1", now)
	if err != nil {
		return 0, 0, fmt.Errorf("deleting expired sign-in flows: %w", err)
	}
	flows = tag.RowsAffected()
	tag, err = s.pool.Exec(ctx, "DELETE FROM sessions WHERE LEAST(idle_expires_at, expires_at) < $1 OR revoked_at < $1", now)
	if err != nil {
		return 0, 0, fmt.Errorf("deleting expired sessions: %w", err)
	}
	return flows, tag.RowsAffected(), nil
}

// transact runs fn in a transaction, committed when it returns nil and rolled back otherwise; a unique constraint it
// hits is a row that is already there.
func (s *AuthStore) transact(ctx context.Context, what string, fn func(tx pgx.Tx) error) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("%s: beginning the transaction: %w", what, err)
	}
	defer func() { _ = tx.Rollback(ctx) }() // a no-op after Commit
	if err := fn(tx); err != nil {
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
