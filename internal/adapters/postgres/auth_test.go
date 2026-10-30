package postgres

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"cobranch.dev/db/migrations"
	"cobranch.dev/internal/services/auth"
)

// authStore returns sign-in's store on a migrated database of its own.
func authStore(t *testing.T) (*AuthStore, *pgxpool.Pool) {
	t.Helper()
	pool := testDatabase(t)
	if err := newSchema(t, pool, migrations.FS, migrations.Go...).Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	return NewAuthStore(pool), pool
}

var now = time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)

// signUp is a user who signed up through issuer with email, verified, holding grants.
func signUp(issuer, subject, email string, verified bool, grants ...auth.Grant) auth.SignUp {
	id := uuid.New()
	return auth.SignUp{
		Principal: auth.Principal{ID: id, Kind: auth.KindUser, DisplayName: "Alice"},
		Identity:  auth.Identity{Issuer: issuer, Subject: subject, UserID: id, Email: email, EmailVerified: verified, Name: "Alice", LastSignInAt: now},
		Grants:    grants,
	}
}

// alice signs Alice up on s, through idp.example.com with alice@example.com verified and grants, and returns her.
func alice(t *testing.T, s *AuthStore, grants ...auth.Grant) auth.SignUp {
	t.Helper()
	up := signUp("https://idp.example.com", "1", "alice@example.com", true, grants...)
	if err := s.SignUp(context.Background(), up); err != nil {
		t.Fatal(err)
	}
	return up
}

// count runs a count query on pool's database.
func count(t *testing.T, pool *pgxpool.Pool, query string, args ...any) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(), query, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// eventKinds lists the audit log's kinds, in order.
func eventKinds(t *testing.T, pool *pgxpool.Pool) []string {
	t.Helper()
	rows, err := pool.Query(context.Background(), "SELECT kind FROM events ORDER BY seq")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var kinds []string
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			t.Fatal(err)
		}
		kinds = append(kinds, k)
	}
	return kinds
}

// session is a session for user userID whose token is token, made at now.
func session(userID uuid.UUID, token string) auth.Session {
	return auth.Session{
		ID: uuid.New(), TokenHash: auth.Hash(token), UserID: userID, Issuer: "https://idp.example.com", Subject: "1",
		CreatedAt: now, LastSeenAt: now, IdleExpiresAt: now.Add(auth.SessionIdle), ExpiresAt: now.Add(auth.SessionMax),
		UserAgent: "test", IP: "127.0.0.1",
	}
}

// signIn signs up's user in on s with a session whose token is token, and returns the session.
func signIn(t *testing.T, s *AuthStore, up auth.SignUp, token string) auth.Session {
	t.Helper()
	session := session(up.Principal.ID, token)
	if err := s.SignIn(context.Background(), up.Identity, false, session); err != nil {
		t.Fatal(err)
	}
	return session
}

func TestAuthStoreFlows(t *testing.T) {
	flow := auth.Flow{StateHash: []byte("s1"), BrowserHash: []byte("b1"), ProviderID: "google", ExpiresAt: now}
	tests := []struct {
		name    string
		take    []byte
		wantErr error
	}{
		{name: "a flow taken once", take: []byte("s1")},
		{name: "an unknown state", take: []byte("s2"), wantErr: auth.ErrNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, _ := authStore(t)
			ctx := context.Background()
			if err := s.CreateFlow(ctx, flow); err != nil {
				t.Fatal(err)
			}
			got, err := s.TakeFlow(ctx, tt.take)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("TakeFlow() error = %v, want %v", err, tt.wantErr)
			}
			if err != nil {
				return
			}
			if string(got.BrowserHash) != "b1" || got.ProviderID != "google" || !got.ExpiresAt.Equal(now) {
				t.Errorf("TakeFlow() = %+v", got)
			}
			if _, err := s.TakeFlow(ctx, tt.take); !errors.Is(err, auth.ErrNotFound) {
				t.Errorf("TakeFlow() twice = %v, want ErrNotFound: a flow is finished once", err)
			}
		})
	}
}

func TestAuthStoreCreateFlowTwice(t *testing.T) {
	s, _ := authStore(t)
	ctx := context.Background()
	if err := s.CreateFlow(ctx, auth.Flow{StateHash: []byte("s1"), BrowserHash: []byte("b1"), ExpiresAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateFlow(ctx, auth.Flow{StateHash: []byte("s1"), BrowserHash: []byte("b2"), ExpiresAt: now}); err == nil {
		t.Error("CreateFlow() accepted the same state twice")
	}
}

func TestAuthStoreSignUp(t *testing.T) {
	admin := auth.Grant{RoleID: auth.RoleInstallationAdmin, Source: auth.SourceAdmins}
	user := auth.Grant{RoleID: auth.RoleInstallationUser, Source: auth.SourceSelfServe}
	tests := []struct {
		name       string
		first      auth.SignUp
		second     auth.SignUp
		wantEmails int
		wantGrants int
		wantHolder bool
	}{
		{name: "a verified email and two grants", first: signUp("https://accounts.google.com", "1", "alice@example.com", true, admin, user), wantEmails: 1, wantGrants: 2, wantHolder: true},
		{name: "an unverified email", first: signUp("https://idp.example.com", "1", "alice@example.com", false)},
		{name: "the same identity twice", first: signUp("https://idp.example.com", "1", "alice@example.com", true), second: signUp("https://idp.example.com", "1", "other@example.com", true), wantEmails: 1, wantHolder: true},
		{name: "the same verified email twice", first: signUp("https://idp.example.com", "1", "alice@example.com", true), second: signUp("https://other.example.com", "2", "alice@example.com", true), wantEmails: 1, wantHolder: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, pool := authStore(t)
			ctx := context.Background()
			if err := s.SignUp(ctx, tt.first); err != nil {
				t.Fatalf("SignUp() = %v", err)
			}
			if tt.second.Principal.ID != uuid.Nil {
				if err := s.SignUp(ctx, tt.second); err == nil {
					t.Fatal("second SignUp() = nil, want a refused row")
				}
			}
			checkSignedUp(t, s, pool, tt.first, tt.wantEmails, tt.wantGrants)
			checkHolder(t, s, tt.first, tt.wantHolder)
		})
	}
}

// checkSignedUp checks that up's identity, wantEmails emails and wantGrants grants are there, and one sign-up event.
func checkSignedUp(t *testing.T, s *AuthStore, pool *pgxpool.Pool, up auth.SignUp, wantEmails, wantGrants int) {
	t.Helper()
	identity, err := s.Identity(context.Background(), up.Identity.Issuer, up.Identity.Subject)
	if err != nil || identity.UserID != up.Principal.ID || identity.Email != up.Identity.Email {
		t.Errorf("Identity() = %+v, %v", identity, err)
	}
	if got := count(t, pool, "SELECT count(*) FROM principal_emails"); got != wantEmails {
		t.Errorf("%d emails, want %d", got, wantEmails)
	}
	if got := count(t, pool, "SELECT count(*) FROM grants"); got != wantGrants {
		t.Errorf("%d grants, want %d", got, wantGrants)
	}
	if kinds := eventKinds(t, pool); kinds[0] != "user.signed_up" || slices.Index(kinds[1:], "user.signed_up") >= 0 {
		t.Errorf("events = %v, want one sign-up first", kinds)
	}
}

// checkHolder checks that alice@example.com is held by up's user if wantHolder, and by nobody otherwise.
func checkHolder(t *testing.T, s *AuthStore, up auth.SignUp, wantHolder bool) {
	t.Helper()
	holder, issuer, err := s.EmailHolder(context.Background(), "alice@example.com")
	if wantHolder && (err != nil || holder != up.Principal.ID || issuer != up.Identity.Issuer) {
		t.Errorf("EmailHolder() = %s, %q, %v, want %s of %s", holder, issuer, err, up.Principal.ID, up.Identity.Issuer)
	}
	if !wantHolder && !errors.Is(err, auth.ErrNotFound) {
		t.Errorf("EmailHolder() = %s, %q, %v, want ErrNotFound", holder, issuer, err)
	}
}

func TestAuthStoreSignIn(t *testing.T) {
	listed := auth.Grant{RoleID: auth.RoleInstallationAdmin, Source: auth.SourceAdmins}
	made := auth.Grant{RoleID: auth.RoleInstallationAdmin, Source: auth.SourceGrant}
	tests := []struct {
		name       string
		grants     []auth.Grant // at sign-up
		admin      bool         // at sign-in
		wantAdmins int          // Installation admin grants after
		wantEvents []string
	}{
		{name: "made an admin", admin: true, wantAdmins: 1, wantEvents: []string{"user.signed_up", "grant.made", "user.signed_in"}},
		{name: "no longer listed", grants: []auth.Grant{listed}, wantEvents: []string{"user.signed_up", "grant.made", "grant.removed", "user.signed_in"}},
		{name: "an admin somebody made, not listed", grants: []auth.Grant{made}, wantAdmins: 1, wantEvents: []string{"user.signed_up", "grant.made", "user.signed_in"}},
		{name: "still an admin", grants: []auth.Grant{listed}, admin: true, wantAdmins: 1, wantEvents: []string{"user.signed_up", "grant.made", "user.signed_in"}},
		{name: "a plain user", wantEvents: []string{"user.signed_up", "user.signed_in"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, pool := authStore(t)
			ctx := context.Background()
			up := alice(t, s, tt.grants...)
			identity := up.Identity
			identity.Name, identity.LastSignInAt = "Alice B.", now.Add(time.Hour)
			if err := s.SignIn(ctx, identity, tt.admin, session(up.Principal.ID, "cbs_token")); err != nil {
				t.Fatalf("SignIn() = %v", err)
			}
			if got := count(t, pool, "SELECT count(*) FROM grants WHERE role_id = $1", auth.RoleInstallationAdmin); got != tt.wantAdmins {
				t.Errorf("%d admin grants, want %d", got, tt.wantAdmins)
			}
			if kinds := eventKinds(t, pool); !slices.Equal(kinds, tt.wantEvents) {
				t.Errorf("events = %v, want %v", kinds, tt.wantEvents)
			}
			got, err := s.Identity(ctx, identity.Issuer, identity.Subject)
			if err != nil || got.Name != "Alice B." || !got.LastSignInAt.Equal(now.Add(time.Hour)) {
				t.Errorf("Identity() after sign-in = %+v, %v", got, err)
			}
		})
	}
}

func TestAuthStoreSignInUnknownIdentity(t *testing.T) {
	s, _ := authStore(t)
	err := s.SignIn(context.Background(), auth.Identity{Issuer: "x", Subject: "y", UserID: uuid.New()}, false, session(uuid.New(), "t"))
	if !errors.Is(err, auth.ErrNotFound) {
		t.Errorf("SignIn() = %v, want ErrNotFound", err)
	}
}

func TestAuthStoreSession(t *testing.T) {
	tests := []struct {
		name    string
		lookup  string
		wantErr error
	}{
		{name: "a session", lookup: "a"},
		{name: "an unknown token", lookup: "b", wantErr: auth.ErrNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, _ := authStore(t)
			up := alice(t, s)
			want := signIn(t, s, up, "a")
			got, user, err := s.Session(context.Background(), auth.Hash(tt.lookup))
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("Session() error = %v, want %v", err, tt.wantErr)
			}
			if err != nil {
				return
			}
			if got.ID != want.ID || !got.RevokedAt.IsZero() || got.UserAgent != "test" {
				t.Errorf("Session() = %+v", got)
			}
			if user.ID != up.Principal.ID || len(user.Emails) != 1 || user.Emails[0].Email != "alice@example.com" {
				t.Errorf("Session() user = %+v", user)
			}
		})
	}
}

func TestAuthStoreRevoke(t *testing.T) {
	s, _ := authStore(t)
	ctx := context.Background()
	up := alice(t, s)
	first, second := signIn(t, s, up, "a"), signIn(t, s, up, "b")
	if err := s.Revoke(ctx, up.Principal.ID, first.ID, now.Add(time.Minute)); err != nil {
		t.Fatalf("Revoke() = %v", err)
	}
	if err := s.Revoke(ctx, up.Principal.ID, first.ID, now.Add(time.Minute)); !errors.Is(err, auth.ErrNotFound) {
		t.Errorf("Revoke() twice = %v, want ErrNotFound", err)
	}
	if err := s.Revoke(ctx, uuid.New(), second.ID, now); !errors.Is(err, auth.ErrNotFound) {
		t.Errorf("Revoke() by another user = %v, want ErrNotFound", err)
	}
	got, _, err := s.Session(ctx, auth.Hash("a"))
	if err != nil || !got.RevokedAt.Equal(now.Add(time.Minute)) {
		t.Errorf("Session() after Revoke() = %+v, %v, want it revoked", got, err)
	}
	live, err := s.Sessions(ctx, up.Principal.ID)
	if err != nil || len(live) != 1 || live[0].ID != second.ID {
		t.Errorf("Sessions() = %+v, %v, want the second alone", live, err)
	}
	if kinds := eventKinds(t, s.pool); kinds[len(kinds)-1] != "session.revoked" {
		t.Errorf("events = %v, want session.revoked last", kinds)
	}
}

func TestAuthStoreTouch(t *testing.T) {
	s, _ := authStore(t)
	ctx := context.Background()
	up := alice(t, s)
	session := signIn(t, s, up, "a")
	seen := now.Add(2 * time.Hour)
	if err := s.Touch(ctx, session.ID, seen); err != nil {
		t.Fatalf("Touch() = %v", err)
	}
	got, _, err := s.Session(ctx, auth.Hash("a"))
	if err != nil || !got.LastSeenAt.Equal(seen) || !got.IdleExpiresAt.Equal(seen.Add(auth.SessionIdle)) {
		t.Errorf("Session() after Touch() = %+v, %v: wants it seen at %v, idle until %v", got, err, seen, seen.Add(auth.SessionIdle))
	}
}

func TestAuthStoreDeleteExpired(t *testing.T) {
	tests := []struct {
		name         string
		at           time.Time
		wantFlows    int64
		wantSessions int64
	}{
		{name: "nothing expired", at: now},
		{name: "the flow and the revoked session", at: now.Add(11 * time.Minute), wantFlows: 1, wantSessions: 1},
		{name: "the idle session too", at: now.Add(auth.SessionIdle + time.Hour), wantFlows: 1, wantSessions: 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := expiring(t)
			flows, sessions, err := s.DeleteExpired(context.Background(), tt.at)
			if err != nil || flows != tt.wantFlows || sessions != tt.wantSessions {
				t.Errorf("DeleteExpired() = %d, %d, %v, want %d, %d", flows, sessions, err, tt.wantFlows, tt.wantSessions)
			}
		})
	}
}

// expiring returns a store with a flow that expires at now plus ten minutes, a session revoked then, and a live one.
func expiring(t *testing.T) *AuthStore {
	t.Helper()
	s, _ := authStore(t)
	ctx := context.Background()
	up := alice(t, s)
	if err := s.CreateFlow(ctx, auth.Flow{StateHash: []byte("s"), BrowserHash: []byte("b"), ExpiresAt: now.Add(auth.FlowLifetime)}); err != nil {
		t.Fatal(err)
	}
	revoked := signIn(t, s, up, "a")
	signIn(t, s, up, "b")
	if err := s.Revoke(ctx, up.Principal.ID, revoked.ID, now.Add(10*time.Minute)); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestAuthStoreSeeded(t *testing.T) {
	s, _ := authStore(t)
	ctx := context.Background()
	seeded := signUp(auth.SeedIssuer, "alice", "alice@example.com", true)
	plain := signUp("https://idp.example.com", "1", "bob@example.com", true)
	for _, up := range []auth.SignUp{seeded, plain} {
		if err := s.SignUp(ctx, up); err != nil {
			t.Fatal(err)
		}
	}
	users, err := s.SeededUsers(ctx)
	if err != nil || len(users) != 1 || users[0].Subject != "alice" || users[0].Email != "alice@example.com" || users[0].DisplayName != "Alice" {
		t.Errorf("SeededUsers() = %+v, %v, want the seeded user alone", users, err)
	}
	identity, err := s.SeededUser(ctx, "alice")
	if err != nil || identity.UserID != seeded.Principal.ID {
		t.Errorf("SeededUser() = %+v, %v", identity, err)
	}
	if _, err := s.SeededUser(ctx, "1"); !errors.Is(err, auth.ErrNotFound) {
		t.Errorf("SeededUser() of a real identity's subject = %v, want ErrNotFound", err)
	}
}
