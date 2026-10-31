package auth_test

import (
	"context"
	"errors"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"cobranch.dev/internal/services/auth"
	"cobranch.dev/internal/services/auth/authtest"
)

var now = time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)

// alice is Google's claims about alice@example.com.
var alice = auth.Claims{Issuer: "https://accounts.google.com", Subject: "g-1", Email: "Alice@Example.com", EmailVerified: true, Name: "Alice"}

// newService returns a auth.Service over a fake store and a Google provider that signs in as claims, at now.
func newService(settings auth.Settings, claims auth.Claims) (*auth.Service, *authtest.Store, *authtest.Provider) {
	store, google := authtest.NewStore(), &authtest.Provider{ProviderID: "google", Claims: claims}
	s := auth.NewService(store, []auth.Provider{google}, settings)
	s.SetClock(func() time.Time { return now })
	return s, store, google
}

// stateOf reads the state parameter of the redirect URL Begin returned.
func stateOf(t *testing.T, redirect string) string {
	t.Helper()
	_, q, _ := strings.Cut(redirect, "state=")
	state, _, _ := strings.Cut(q, "&")
	if state == "" {
		t.Fatalf("no state in %s", redirect)
	}
	return state
}

func TestBegin(t *testing.T) {
	tests := []struct {
		name       string
		provider   string
		returnPath string
		fail       error
		wantErr    error
		wantReturn string
	}{
		{name: "Google", provider: "google", returnPath: "/orgs/acme", wantReturn: "/orgs/acme"},
		{name: "an unsafe return", provider: "google", returnPath: "https://evil.example.com", wantReturn: "/"},
		{name: "an unknown provider", provider: "github", wantErr: auth.ErrUnknownProvider},
		{name: "a store that fails", provider: "google", fail: errors.New("down"), wantErr: errDown},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, store, _ := newService(auth.Settings{SignUp: auth.PolicyAnyone}, alice)
			if tt.fail != nil {
				store.Fail = errDown
			}
			got, err := s.Begin(context.Background(), tt.provider, tt.returnPath)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("Begin() error = %v, want %v", err, tt.wantErr)
			}
			if err != nil {
				return
			}
			checkBegun(t, store, got, tt.wantReturn)
		})
	}
}

// checkBegun checks that store holds got's flow, returning to wantReturn, and that got's redirect carries it.
func checkBegun(t *testing.T, store *authtest.Store, got auth.Begun, wantReturn string) {
	t.Helper()
	flow, ok := store.Flows[string(auth.Hash(stateOf(t, got.RedirectURL)))]
	if !ok {
		t.Fatal("Begin() recorded no flow under the state's hash")
	}
	if string(flow.BrowserHash) != string(auth.Hash(got.BrowserToken)) || flow.ProviderID != "google" ||
		flow.ReturnPath != wantReturn || !flow.ExpiresAt.Equal(now.Add(auth.FlowLifetime)) {
		t.Errorf("flow = %+v", flow)
	}
	if !strings.Contains(got.RedirectURL, "nonce="+flow.Nonce) || !strings.Contains(got.RedirectURL, "challenge="+auth.PKCEChallenge(flow.PKCEVerifier)) {
		t.Errorf("redirect %s doesn't carry the flow's nonce and PKCE challenge", got.RedirectURL)
	}
}

var errDown = errors.New("down")

func TestSafeReturnPath(t *testing.T) {
	tests := []struct {
		path string
		want string
	}{
		{path: "/orgs/acme", want: "/orgs/acme"},
		{path: "/", want: "/"},
		{path: "", want: "/"},
		{path: "orgs", want: "/"},
		{path: "//evil.example.com", want: "/"},
		{path: "/\\evil.example.com", want: "/"},
		{path: "https://evil.example.com", want: "/"},
		{path: "/a b", want: "/"},
		{path: "/a\r\nSet-Cookie: x", want: "/"},
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			if got := auth.SafeReturnPath(tt.path); got != tt.want {
				t.Errorf("auth.SafeReturnPath(%q) = %q, want %q", tt.path, got, tt.want)
			}
		})
	}
}

func TestSafeReturn(t *testing.T) {
	backOffice, _ := url.Parse("https://admin.example.com/back-office")
	s := auth.NewService(nil, nil, auth.Settings{ReturnURLs: []*url.URL{backOffice}})
	tests := []struct {
		name, to, want string
	}{
		{name: "a Console path", to: "/acme", want: "/acme"},
		{name: "the Back-office's URL", to: "https://admin.example.com/back-office", want: "https://admin.example.com/back-office"},
		{name: "a page of the Back-office", to: "https://Admin.example.com/back-office/users?q=ada", want: "https://Admin.example.com/back-office/users?q=ada"},
		{name: "another path of its host", to: "https://admin.example.com/back-officer", want: "/"},
		{name: "a path that climbs out of it", to: "https://admin.example.com/back-office/../acme", want: "/"},
		{name: "another scheme", to: "http://admin.example.com/back-office", want: "/"},
		{name: "another host", to: "https://evil.example.com/back-office", want: "/"},
		{name: "credentials", to: "https://x@admin.example.com/back-office", want: "/"},
		{name: "a line break", to: "https://admin.example.com/back-office\r\nSet-Cookie: x", want: "/"},
		{name: "a protocol-relative URL", to: "//admin.example.com/back-office", want: "/"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := s.SafeReturn(tt.to); got != tt.want {
				t.Errorf("SafeReturn(%q) = %q, want %q", tt.to, got, tt.want)
			}
		})
	}
}

// begun starts a sign-in and returns its state and browser token.
func begun(t *testing.T, s *auth.Service) (state, browser string) {
	t.Helper()
	b, err := s.Begin(context.Background(), "google", "/orgs/acme")
	if err != nil {
		t.Fatal(err)
	}
	return stateOf(t, b.RedirectURL), b.BrowserToken
}

// completeCase is a sign-in for TestComplete and TestCompleteSettings to finish.
type completeCase struct {
	name     string
	settings auth.Settings
	// db is what the Back-office set; nil: self-serve off, the rest unset.
	db       *auth.InstallationSettings
	claims   auth.Claims
	existing *auth.SignUp // a user already there
	code     string
	late     bool
	wrongB   bool
	wrongS   bool
	wantErr  error
	wantName string
	wantRole []uuid.UUID
}

func TestComplete(t *testing.T) {
	unverified := alice
	unverified.EmailVerified = false
	nameless := alice
	nameless.Name = ""
	on := true
	tests := []completeCase{
		{name: "a new user, anyone may", settings: auth.Settings{SignUp: auth.PolicyAnyone}, claims: alice, code: "good", wantName: "Alice"},
		{name: "a new user with self-serve", settings: auth.Settings{SignUp: auth.PolicyAnyone, SelfServe: &on}, claims: alice, code: "good", wantName: "Alice", wantRole: []uuid.UUID{auth.RoleInstallationUser}},
		{name: "a first administrator", settings: auth.Settings{SignUp: auth.PolicyInvited, Admins: []string{"alice@example.com"}}, claims: alice, code: "good", wantName: "Alice", wantRole: []uuid.UUID{auth.RoleInstallationAdmin}},
		{name: "an administrator's email, unverified", settings: auth.Settings{SignUp: auth.PolicyInvited, Admins: []string{"alice@example.com"}}, claims: unverified, code: "good", wantErr: auth.ErrSignUpRefused},
		{name: "a domain the policy admits", settings: auth.Settings{SignUp: auth.PolicyDomains, SignUpDomains: []string{"example.com"}}, claims: alice, code: "good", wantName: "Alice"},
		{name: "a domain the policy refuses", settings: auth.Settings{SignUp: auth.PolicyDomains, SignUpDomains: []string{"example.org"}}, claims: alice, code: "good", wantErr: auth.ErrSignUpRefused},
		{name: "a domain, unverified", settings: auth.Settings{SignUp: auth.PolicyDomains, SignUpDomains: []string{"example.com"}}, claims: unverified, code: "good", wantErr: auth.ErrSignUpRefused},
		{name: "invited only", settings: auth.Settings{SignUp: auth.PolicyInvited}, claims: alice, code: "good", wantErr: auth.ErrSignUpRefused},
		{name: "no name: the email's local part", settings: auth.Settings{SignUp: auth.PolicyAnyone}, claims: nameless, code: "good", wantName: "alice"},
		{name: "a known identity", settings: auth.Settings{SignUp: auth.PolicyInvited}, claims: alice, existing: &auth.SignUp{Principal: auth.Principal{ID: uuid.New(), Kind: auth.KindUser, DisplayName: "Alice"}, Identity: auth.Identity{Issuer: alice.Issuer, Subject: alice.Subject}}, code: "good", wantName: "Alice"},
		{name: "an email another user holds", settings: auth.Settings{SignUp: auth.PolicyAnyone}, claims: alice, existing: &auth.SignUp{Principal: auth.Principal{ID: uuid.New(), Kind: auth.KindUser, DisplayName: "Alice"}, Identity: auth.Identity{Issuer: "https://other.example.com", Subject: "o-1", Email: "alice@example.com", EmailVerified: true}}, code: "good", wantErr: auth.ErrSignUpRefused},
		{name: "the provider refuses the code", settings: auth.Settings{SignUp: auth.PolicyAnyone}, claims: alice, code: "bad", wantErr: auth.ErrProvider},
		{name: "late", settings: auth.Settings{SignUp: auth.PolicyAnyone}, claims: alice, code: "good", late: true, wantErr: auth.ErrFlow},
		{name: "another browser", settings: auth.Settings{SignUp: auth.PolicyAnyone}, claims: alice, code: "good", wrongB: true, wantErr: auth.ErrFlow},
		{name: "an unknown state", settings: auth.Settings{SignUp: auth.PolicyAnyone}, claims: alice, code: "good", wrongS: true, wantErr: auth.ErrFlow},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) { runComplete(t, tt) })
	}
}

// TestCompleteSettings finishes sign-ups under the settings the Back-office keeps, and the values' over them.
func TestCompleteSettings(t *testing.T) {
	on, off := true, false
	invited, domains := auth.PolicyInvited, auth.PolicyDomains
	tests := []completeCase{
		{name: "the defaults: anyone, self-serve", db: &auth.InstallationSettings{}, claims: alice, code: "good", wantName: "Alice", wantRole: []uuid.UUID{auth.RoleInstallationUser}},
		{name: "the Back-office's policy", db: &auth.InstallationSettings{SignUp: &invited}, claims: alice, code: "good", wantErr: auth.ErrSignUpRefused},
		{name: "the Back-office's domains", db: &auth.InstallationSettings{SignUp: &domains, SignUpDomains: []string{"example.com"}, SelfServe: &off}, claims: alice, code: "good", wantName: "Alice"},
		{name: "the values' policy over the Back-office's", settings: auth.Settings{SignUp: auth.PolicyAnyone}, db: &auth.InstallationSettings{SignUp: &invited, SelfServe: &off}, claims: alice, code: "good", wantName: "Alice"},
		{name: "the values' self-serve over the Back-office's", settings: auth.Settings{SelfServe: &off}, db: &auth.InstallationSettings{SelfServe: &on}, claims: alice, code: "good", wantName: "Alice"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) { runComplete(t, tt) })
	}
}

// runComplete begins tt's sign-in, tampers with it as tt says, finishes it and checks the outcome.
func runComplete(t *testing.T, tt completeCase) {
	t.Helper()
	off := false
	s, store, google := newService(tt.settings, tt.claims)
	store.Installation = auth.InstallationSettings{SelfServe: &off}
	if tt.db != nil {
		store.Installation = *tt.db
	}
	if tt.existing != nil {
		tt.existing.Identity.UserID = tt.existing.Principal.ID
		_ = store.SignUp(context.Background(), *tt.existing)
	}
	state, browser := begun(t, s)
	state, browser = tamper(s, state, browser, tt.late, tt.wrongB, tt.wrongS)
	got, err := s.Complete(context.Background(), tt.code, state, browser, auth.Client{UserAgent: "test", IP: "::1"})
	if !errors.Is(err, tt.wantErr) {
		t.Fatalf("Complete() error = %v, want %v", err, tt.wantErr)
	}
	if _, left := store.Flows[string(auth.Hash(state))]; left {
		t.Error("the flow is still there after Complete()")
	}
	if err != nil {
		checkTaken(t, err)
		return
	}
	if google.Got.Code != "good" || google.Got.Nonce == "" || google.Got.Verifier == "" {
		t.Errorf("Exchange got %+v: wants the code, the flow's nonce and its PKCE verifier", google.Got)
	}
	checkCompleted(t, store, got, tt.wantName, tt.wantRole)
}

// tamper spoils a begun sign-in the way a case asks: late, from another browser, or with an unknown state.
func tamper(s *auth.Service, state, browser string, late, wrongB, wrongS bool) (string, string) {
	if late {
		s.SetClock(func() time.Time { return now.Add(auth.FlowLifetime) })
	}
	if wrongB {
		browser = "other"
	}
	if wrongS {
		state = "other"
	}
	return state, browser
}

// checkTaken checks an EmailTakenError's details, when err is one.
func checkTaken(t *testing.T, err error) {
	t.Helper()
	var taken *auth.EmailTakenError
	if errors.As(err, &taken) && (taken.Email != "alice@example.com" || taken.Issuer != "https://other.example.com") {
		t.Errorf("EmailTakenError = %+v", taken)
	}
}

// checkCompleted checks that got signed Alice in: her session, her identity's email lowercased, and the roles she
// was granted.
func checkCompleted(t *testing.T, store *authtest.Store, got auth.Completed, wantName string, wantRoles []uuid.UUID) {
	t.Helper()
	if got.ReturnPath != "/orgs/acme" || !auth.ValidSecret(auth.PrefixSession, got.SessionToken) {
		t.Errorf("Complete() = %+v", got)
	}
	session, user, err := store.Session(context.Background(), auth.Hash(got.SessionToken))
	if err != nil || user.DisplayName != wantName || session.Issuer != alice.Issuer || session.UserAgent != "test" ||
		!session.IdleExpiresAt.Equal(now.Add(auth.SessionIdle)) || !session.ExpiresAt.Equal(now.Add(auth.SessionMax)) {
		t.Errorf("session = %+v, user = %+v, %v", session, user, err)
	}
	identity := store.Identities[alice.Issuer+"|"+alice.Subject]
	if identity.Email != "alice@example.com" || !identity.LastSignInAt.Equal(now) {
		t.Errorf("identity = %+v: wants the email lowercase and the sign-in recorded", identity)
	}
	var roles []uuid.UUID
	for _, g := range store.Grants[user.ID] {
		roles = append(roles, g.RoleID)
	}
	if !slices.Equal(roles, wantRoles) {
		t.Errorf("grants = %v, want %v", roles, wantRoles)
	}
}

// signedIn signs alice in and returns her session's token.
func signedIn(t *testing.T, s *auth.Service) string {
	t.Helper()
	state, browser := begun(t, s)
	c, err := s.Complete(context.Background(), "good", state, browser, auth.Client{})
	if err != nil {
		t.Fatal(err)
	}
	return c.SessionToken
}

func TestAuthenticate(t *testing.T) {
	tests := []struct {
		name      string
		token     func(t *testing.T, s *auth.Service, store *authtest.Store) string
		at        time.Time
		wantErr   error
		wantTouch bool
	}{
		{name: "a live session", token: func(t *testing.T, s *auth.Service, _ *authtest.Store) string { return signedIn(t, s) }, at: now.Add(time.Second)},
		{name: "a session used a while ago", token: func(t *testing.T, s *auth.Service, _ *authtest.Store) string { return signedIn(t, s) }, at: now.Add(2 * time.Minute), wantTouch: true},
		{name: "an idle session", token: func(t *testing.T, s *auth.Service, _ *authtest.Store) string { return signedIn(t, s) }, at: now.Add(auth.SessionIdle), wantErr: auth.ErrUnauthenticated},
		{name: "an aged session", token: func(t *testing.T, s *auth.Service, _ *authtest.Store) string { return signedIn(t, s) }, at: now.Add(auth.SessionMax), wantErr: auth.ErrUnauthenticated},
		{name: "a revoked session", token: func(t *testing.T, s *auth.Service, _ *authtest.Store) string {
			token := signedIn(t, s)
			if err := s.SignOut(context.Background(), token); err != nil {
				t.Fatal(err)
			}
			return token
		}, at: now.Add(time.Second), wantErr: auth.ErrUnauthenticated},
		{name: "a suspended user", token: func(t *testing.T, s *auth.Service, store *authtest.Store) string {
			token := signedIn(t, s)
			for id, u := range store.Users {
				u.Suspended = true
				store.Users[id] = u
			}
			return token
		}, at: now.Add(time.Second), wantErr: auth.ErrUnauthenticated},
		{name: "a token nobody has", token: func(*testing.T, *auth.Service, *authtest.Store) string { return auth.NewSecret(auth.PrefixSession) }, at: now, wantErr: auth.ErrUnauthenticated},
		{name: "not a token", token: func(*testing.T, *auth.Service, *authtest.Store) string { return "cbs_nonsense" }, at: now, wantErr: auth.ErrUnauthenticated},
		{name: "a store that fails", token: func(t *testing.T, s *auth.Service, store *authtest.Store) string {
			token := signedIn(t, s)
			store.Fail = errDown
			return token
		}, at: now, wantErr: errDown},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, store, _ := newService(auth.Settings{SignUp: auth.PolicyAnyone}, alice)
			token := tt.token(t, s, store)
			s.SetClock(func() time.Time { return tt.at })
			got, err := s.Authenticate(context.Background(), token)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("Authenticate() error = %v, want %v", err, tt.wantErr)
			}
			if err != nil {
				return
			}
			if got.User.DisplayName != "Alice" || got.Session.UserID != got.User.ID {
				t.Errorf("Authenticate() = %+v", got)
			}
			if touched := got.Session.LastSeenAt.Equal(tt.at); touched != tt.wantTouch {
				t.Errorf("last seen %v at %v, want touched %t", got.Session.LastSeenAt, tt.at, tt.wantTouch)
			}
		})
	}
}

func TestSessionsAndRevoke(t *testing.T) {
	s, store, _ := newService(auth.Settings{SignUp: auth.PolicyAnyone}, alice)
	ctx := context.Background()
	first, second := signedIn(t, s), signedIn(t, s)
	a, err := s.Authenticate(ctx, first)
	if err != nil {
		t.Fatal(err)
	}
	if live, err := s.Sessions(ctx, a.User.ID); err != nil || len(live) != 2 {
		t.Fatalf("Sessions() = %d, %v, want 2", len(live), err)
	}
	other, _ := s.Authenticate(ctx, second)
	if err := s.Revoke(ctx, a.User.ID, other.Session.ID); err != nil {
		t.Fatalf("Revoke() = %v", err)
	}
	if err := s.Revoke(ctx, a.User.ID, other.Session.ID); !errors.Is(err, auth.ErrNotFound) {
		t.Errorf("Revoke() twice = %v, want auth.ErrNotFound", err)
	}
	if live, err := s.Sessions(ctx, a.User.ID); err != nil || len(live) != 1 || live[0].ID != a.Session.ID {
		t.Errorf("Sessions() after Revoke() = %+v, %v, want the first alone", live, err)
	}
	if err := s.SignOut(ctx, second); err != nil {
		t.Errorf("SignOut() of a revoked session = %v, want nil", err)
	}
	s.SetClock(func() time.Time { return now.Add(auth.SessionMax + time.Hour) })
	if live, err := s.Sessions(ctx, a.User.ID); err != nil || len(live) != 0 {
		t.Errorf("Sessions() once aged = %d, %v, want none", len(live), err)
	}
	flows, sessions, err := s.DeleteExpired(ctx)
	if err != nil || flows != 0 || sessions != 2 {
		t.Errorf("DeleteExpired() = %d, %d, %v, want 0 flows and 2 sessions", flows, sessions, err)
	}
	store.Fail = errDown
	if _, _, err := s.DeleteExpired(ctx); !errors.Is(err, errDown) {
		t.Errorf("DeleteExpired() with a failing store = %v", err)
	}
}

func TestSeeded(t *testing.T) {
	seeded := auth.SignUp{Principal: auth.Principal{ID: uuid.New(), Kind: auth.KindUser, DisplayName: "Demo"},
		Identity: auth.Identity{Issuer: auth.SeedIssuer, Subject: "demo", Email: "demo@example.com", EmailVerified: true}}
	seeded.Identity.UserID = seeded.Principal.ID
	tests := []struct {
		name      string
		seed      bool
		subject   string
		wantErr   error
		wantUsers int
	}{
		{name: "seeding on", seed: true, subject: "demo", wantUsers: 1},
		{name: "an unknown demo user", seed: true, subject: "nobody", wantErr: auth.ErrNotFound, wantUsers: 1},
		{name: "seeding off", subject: "demo", wantErr: auth.ErrSeedOff},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, store, _ := newService(auth.Settings{SignUp: auth.PolicyAnyone, Seed: tt.seed}, alice)
			_ = store.SignUp(context.Background(), seeded)
			users, err := s.SeededUsers(context.Background())
			if err != nil || len(users) != tt.wantUsers {
				t.Errorf("SeededUsers() = %d, %v, want %d", len(users), err, tt.wantUsers)
			}
			got, err := s.SignInSeeded(context.Background(), tt.subject, "/orgs", auth.Client{})
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("SignInSeeded() error = %v, want %v", err, tt.wantErr)
			}
			if err != nil {
				return
			}
			a, err := s.Authenticate(context.Background(), got.SessionToken)
			if err != nil || a.User.ID != seeded.Principal.ID || got.ReturnPath != "/orgs" {
				t.Errorf("SignInSeeded() = %+v, then Authenticate() = %+v, %v", got, a, err)
			}
		})
	}
}

func TestParsePolicy(t *testing.T) {
	tests := []struct {
		s       string
		want    auth.Policy
		wantErr bool
	}{
		{s: "anyone", want: auth.PolicyAnyone},
		{s: "domains", want: auth.PolicyDomains},
		{s: "invited", want: auth.PolicyInvited},
		{s: "everyone", wantErr: true},
		{s: "", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.s, func(t *testing.T) {
			got, err := auth.ParsePolicy(tt.s)
			if (err != nil) != tt.wantErr || got != tt.want {
				t.Errorf("auth.ParsePolicy(%q) = %q, %v, want %q, error %t", tt.s, got, err, tt.want, tt.wantErr)
			}
		})
	}
}

func TestEmailTakenError(t *testing.T) {
	err := error(&auth.EmailTakenError{Email: "alice@example.com", Issuer: "https://accounts.google.com"})
	if !errors.Is(err, auth.ErrSignUpRefused) || errors.Is(err, auth.ErrFlow) {
		t.Error("an auth.EmailTakenError is an auth.ErrSignUpRefused and nothing else")
	}
	if !strings.Contains(err.Error(), "alice@example.com") {
		t.Errorf("Error() = %q", err)
	}
}

func TestSessionLive(t *testing.T) {
	session := auth.Session{IdleExpiresAt: now.Add(time.Hour), ExpiresAt: now.Add(2 * time.Hour)}
	revoked := session
	revoked.RevokedAt = now
	tests := []struct {
		name    string
		session auth.Session
		at      time.Time
		want    bool
	}{
		{name: "fresh", session: session, at: now, want: true},
		{name: "idle", session: session, at: now.Add(time.Hour)},
		{name: "aged", session: session, at: now.Add(2 * time.Hour)},
		{name: "revoked", session: revoked, at: now},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.session.Live(tt.at); got != tt.want {
				t.Errorf("Live() = %t, want %t", got, tt.want)
			}
		})
	}
}

// TestAuthenticateAPIKey resolves API keys' secrets: bearer tokens that act as a key's principal.
func TestAuthenticateAPIKey(t *testing.T) {
	key := auth.Principal{ID: uuid.New(), Kind: auth.KindAPIKey, DisplayName: "Deploy"}
	tests := []struct {
		name      string
		principal auth.Principal
		expiresAt time.Time
		revoked   bool
		lastUsed  time.Time
		token     string // instead of the credential's secret
		at        time.Time
		wantErr   error
		wantTouch bool
	}{
		{name: "a live secret", principal: key, expiresAt: now.Add(time.Hour), at: now, wantTouch: true},
		{name: "a secret used lately", principal: key, expiresAt: now.Add(time.Hour), lastUsed: now.Add(-time.Second), at: now},
		{name: "a secret that never expires", principal: key, at: now.AddDate(10, 0, 0), wantTouch: true},
		{name: "an expired secret", principal: key, expiresAt: now, at: now, wantErr: auth.ErrUnauthenticated},
		{name: "a revoked secret", principal: key, expiresAt: now.Add(time.Hour), revoked: true, at: now, wantErr: auth.ErrUnauthenticated},
		{name: "a suspended key", principal: auth.Principal{ID: key.ID, Kind: auth.KindAPIKey, Suspended: true}, at: now, wantErr: auth.ErrUnauthenticated},
		{name: "a secret nobody has", principal: key, token: auth.NewSecret(auth.PrefixAPIKey), at: now, wantErr: auth.ErrUnauthenticated},
		{name: "not a secret", principal: key, token: "cbk_nonsense", at: now, wantErr: auth.ErrUnauthenticated},
		{name: "a session's token", principal: key, token: auth.NewSecret(auth.PrefixSession), at: now, wantErr: auth.ErrUnauthenticated},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, store, _ := newService(auth.Settings{}, alice)
			secret := auth.NewSecret(auth.PrefixAPIKey)
			c := store.AddCredential(secret, tt.principal, tt.expiresAt)
			c.LastUsedAt = tt.lastUsed
			if tt.revoked {
				c.RevokedAt = now.Add(-time.Minute)
			}
			store.Credentials[string(auth.Hash(secret))] = c
			s.SetClock(func() time.Time { return tt.at })
			token := secret
			if tt.token != "" {
				token = tt.token
			}
			got, err := s.Authenticate(context.Background(), token)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("Authenticate() error = %v, want %v", err, tt.wantErr)
			}
			if err != nil {
				return
			}
			if got.User.ID != key.ID || got.User.Kind != auth.KindAPIKey || got.Credential.ID != c.ID || got.Session.ID != uuid.Nil {
				t.Errorf("Authenticate() = %+v", got)
			}
			if touched := got.Credential.LastUsedAt.Equal(tt.at); touched != tt.wantTouch {
				t.Errorf("last used %v at %v, want touched %t", got.Credential.LastUsedAt, tt.at, tt.wantTouch)
			}
		})
	}
}

func TestSignUpSettings(t *testing.T) {
	on, off := true, false
	invited := auth.PolicyInvited
	tests := []struct {
		name          string
		values        auth.Settings
		db            auth.InstallationSettings
		want          auth.SignUpSettings
		wantSignUpLck bool
		wantSelfLck   bool
	}{
		{name: "neither: the defaults", want: auth.SignUpSettings{SignUp: auth.PolicyAnyone, SelfServe: true}},
		{name: "the Back-office's", db: auth.InstallationSettings{SignUp: &invited, SignUpDomains: []string{"example.com"}, SelfServe: &off},
			want: auth.SignUpSettings{SignUp: auth.PolicyInvited, SignUpDomains: []string{"example.com"}}},
		{name: "the values', locked", values: auth.Settings{SignUp: auth.PolicyDomains, SignUpDomains: []string{"example.org"}, SelfServe: &on},
			db:   auth.InstallationSettings{SignUp: &invited, SignUpDomains: []string{"example.com"}, SelfServe: &off},
			want: auth.SignUpSettings{SignUp: auth.PolicyDomains, SignUpDomains: []string{"example.org"}, SelfServe: true}, wantSignUpLck: true, wantSelfLck: true},
		{name: "the values' self-serve, the Back-office's policy", values: auth.Settings{SelfServe: &off}, db: auth.InstallationSettings{SignUp: &invited},
			want: auth.SignUpSettings{SignUp: auth.PolicyInvited}, wantSelfLck: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.values.SignUpSettings(tt.db)
			if got.SignUp != tt.want.SignUp || !slices.Equal(got.SignUpDomains, tt.want.SignUpDomains) || got.SelfServe != tt.want.SelfServe {
				t.Errorf("SignUpSettings() = %+v, want %+v", got, tt.want)
			}
			if tt.values.SignUpLocked() != tt.wantSignUpLck || tt.values.SelfServeLocked() != tt.wantSelfLck {
				t.Errorf("locked = %t, %t, want %t, %t", tt.values.SignUpLocked(), tt.values.SelfServeLocked(), tt.wantSignUpLck, tt.wantSelfLck)
			}
		})
	}
}
