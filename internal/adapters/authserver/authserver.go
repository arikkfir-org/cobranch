// Package authserver is the Auth server's HTTP surface: the only workload that sends browsers to identity providers
// and sets the session cookie. It serves no data: the sign-in page is the Console's, which calls /providers and sends
// the browser to /sign-in (https://github.com/arikkfir-org/cobranch/blob/main/docs/cobranch/designs/sign-in-and-organizations.md, "Sign-in").
package authserver

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"

	"cobranch.dev/internal/adapters/server"
	"cobranch.dev/internal/services/auth"
)

// Handler serves the Auth server's endpoints for service, setting cookie as the session cookie and returning browsers
// to console, the Console's public URL, or below backOffice, the Back-office's, when a sign-in began there (nil when
// the installation runs none). The browser sees it at its public URL, whose path the route strips, so every path here
// is relative to that.
//
//   - GET /providers: the identity providers, and the seeded users while seeding is on, as the sign-in page shows them.
//   - GET /sign-in?provider=<id>&return=<path>: begins a sign-in at the provider; ?user=<subject> signs in as a seeded
//     user instead. The return is a Console path, or a Back-office URL (auth.Service.SafeReturn).
//   - GET /callback: where the provider sends the browser back.
//   - POST /sign-out: revokes the cookie's session, from the Console's or the Back-office's origin alone.
//
// An error on the way back sends the browser to the Console's /sign-in?error=<code>.
func Handler(service *auth.Service, cookie auth.Cookie, console, backOffice *url.URL) http.Handler {
	h := &handler{service: service, cookie: cookie, console: console, backOffice: backOffice}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /providers", h.providers)
	mux.HandleFunc("GET /sign-in", h.signIn)
	mux.HandleFunc("GET /callback", h.callback)
	mux.HandleFunc("POST /sign-out", h.signOut)
	return server.AllowOrigin([]string{origin(console), origin(backOffice)}, mux)
}

// origin is u's origin, or "" without one.
func origin(u *url.URL) string {
	if u == nil || u.Host == "" {
		return ""
	}
	return u.Scheme + "://" + strings.ToLower(u.Host)
}

type handler struct {
	service             *auth.Service
	cookie              auth.Cookie
	console, backOffice *url.URL
}

// provider is a provider as the sign-in page lists it.
type provider struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// seeded is a demo user as the sign-in page lists them, while seeding is on.
type seeded struct {
	Subject     string `json:"subject"`
	DisplayName string `json:"displayName"`
	Email       string `json:"email"`
}

// providers answers the identity providers and, while seeding is on, the seeded users, so the sign-in page knows
// what to offer; an installation with none of either isn't set up yet.
func (h *handler) providers(w http.ResponseWriter, r *http.Request) {
	providers := make([]provider, 0)
	for _, p := range h.service.Providers() {
		providers = append(providers, provider{ID: p.ID(), Name: p.Name()})
	}
	users, err := h.service.SeededUsers(r.Context())
	if err != nil {
		slog.ErrorContext(r.Context(), "Listing the seeded users failed", "error", err)
		http.Error(w, "the seeded users could not be listed", http.StatusInternalServerError)
		return
	}
	seededUsers := make([]seeded, 0, len(users))
	for _, u := range users {
		seededUsers = append(seededUsers, seeded{Subject: u.Subject, DisplayName: u.DisplayName, Email: u.Email})
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(map[string]any{"providers": providers, "seeded": seededUsers}) // the caller is gone
}

// signIn begins a sign-in at the provider the query names, or as the seeded user it names.
func (h *handler) signIn(w http.ResponseWriter, r *http.Request) {
	ctx, q := r.Context(), r.URL.Query()
	if subject := q.Get("user"); subject != "" {
		done, err := h.service.SignInSeeded(ctx, subject, q.Get("return"), client(r))
		if err != nil {
			h.fail(w, r, err)
			return
		}
		h.finish(w, r, done)
		return
	}
	begun, err := h.service.Begin(ctx, q.Get("provider"), q.Get("return"))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name: auth.FlowCookie, Value: begun.BrowserToken, Path: "/", MaxAge: int(auth.FlowLifetime.Seconds()),
		HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode,
	})
	http.Redirect(w, r, begun.RedirectURL, http.StatusFound)
}

// callback finishes the sign-in the provider sent the browser back from.
func (h *handler) callback(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	clearCookie(w, auth.FlowCookie, "")
	if e := q.Get("error"); e != "" {
		slog.InfoContext(r.Context(), "The identity provider refused a sign-in", "error", e, "description", q.Get("error_description"))
		h.fail(w, r, auth.ErrProvider)
		return
	}
	browser, _ := r.Cookie(auth.FlowCookie)
	if browser == nil || q.Get("code") == "" || q.Get("state") == "" {
		h.fail(w, r, auth.ErrFlow)
		return
	}
	done, err := h.service.Complete(r.Context(), q.Get("code"), q.Get("state"), browser.Value, client(r))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	h.finish(w, r, done)
}

// finish sets the session cookie and returns the browser to the Console's path, or to the Back-office's URL, that the
// sign-in began with.
func (h *handler) finish(w http.ResponseWriter, r *http.Request, done auth.Completed) {
	http.SetCookie(w, &http.Cookie{
		Name: h.cookie.Name, Value: done.SessionToken, Domain: h.cookie.Domain, Path: "/",
		MaxAge: int(auth.SessionMax.Seconds()), HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode,
	})
	http.Redirect(w, r, h.returnURL(done.ReturnPath).String(), http.StatusFound)
}

// returnURL is where a finished sign-in sends the browser: a Console path joined to the Console's URL, or the path,
// query and fragment of an absolute URL on the Back-office's host, on the Back-office's URL. The service keeps no other
// return (auth.Service.SafeReturn), but the scheme and host come from the configuration all the same, so no stored
// value can send the browser to another site: anything else returns to the Console's home.
func (h *handler) returnURL(to string) *url.URL {
	u, err := url.Parse(to)
	if err != nil || !u.IsAbs() {
		return h.console.JoinPath(to)
	}
	if h.backOffice == nil || !strings.EqualFold(u.Scheme, h.backOffice.Scheme) || !strings.EqualFold(u.Host, h.backOffice.Host) {
		return h.console.JoinPath("/")
	}
	back := *h.backOffice
	back.Path, back.RawPath, back.RawQuery, back.Fragment = u.Path, u.RawPath, u.RawQuery, u.Fragment
	return &back
}

// signOut revokes the cookie's session and clears the cookie. Only the Console's and the Back-office's pages may ask:
// the request must come from their origin, since the browser sends the cookie wherever the request came from.
func (h *handler) signOut(w http.ResponseWriter, r *http.Request) {
	if !server.SameOrigin(r, origin(h.console)) && !server.SameOrigin(r, origin(h.backOffice)) {
		http.Error(w, "sign-out is the Console's or the Back-office's to ask for", http.StatusForbidden)
		return
	}
	if c, _ := r.Cookie(h.cookie.Name); c != nil {
		if err := h.service.SignOut(r.Context(), c.Value); err != nil {
			slog.ErrorContext(r.Context(), "Signing out failed", "error", err)
			http.Error(w, "the session could not be revoked", http.StatusInternalServerError)
			return
		}
	}
	clearCookie(w, h.cookie.Name, h.cookie.Domain)
	w.WriteHeader(http.StatusNoContent)
}

// fail sends the browser to the Console's sign-in page with an error code it can explain.
func (h *handler) fail(w http.ResponseWriter, r *http.Request, err error) {
	code := "internal"
	var taken *auth.EmailTakenError
	switch {
	case errors.As(err, &taken):
		code = "email_taken"
	case errors.Is(err, auth.ErrSignUpRefused):
		code = "refused"
	case errors.Is(err, auth.ErrUnknownProvider), errors.Is(err, auth.ErrSeedOff), errors.Is(err, auth.ErrNotFound):
		code = "unknown_provider"
	case errors.Is(err, auth.ErrFlow):
		code = "flow"
	case errors.Is(err, auth.ErrProvider):
		code = "provider"
	}
	if code == "internal" {
		slog.ErrorContext(r.Context(), "A sign-in failed", "error", err)
	} else {
		slog.InfoContext(r.Context(), "A sign-in was refused", "code", code, "error", err)
	}
	u := h.console.JoinPath("/sign-in")
	q := url.Values{"error": {code}}
	if taken != nil {
		q.Set("issuer", taken.Issuer)
	}
	u.RawQuery = q.Encode()
	http.Redirect(w, r, u.String(), http.StatusFound)
}

// clearCookie tells the browser to drop the cookie name of domain.
func clearCookie(w http.ResponseWriter, name, domain string) {
	http.SetCookie(w, &http.Cookie{Name: name, Value: "", Domain: domain, Path: "/", MaxAge: -1,
		HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode})
}

// client is what the session records about the browser: its User-Agent and its address, the first of
// X-Forwarded-For when a proxy set it.
func client(r *http.Request) auth.Client {
	ip := r.RemoteAddr
	if host, _, err := net.SplitHostPort(ip); err == nil {
		ip = host
	}
	if fwd := r.Header.Get("X-Forwarded-For"); fwd != "" {
		ip = strings.TrimSpace(strings.Split(fwd, ",")[0])
	}
	return auth.Client{UserAgent: r.UserAgent(), IP: ip}
}

// Check is a readiness check that the Auth server can serve: it needs nothing beyond the database, which the server's
// own check covers, so it passes.
func Check(context.Context) error { return nil }
