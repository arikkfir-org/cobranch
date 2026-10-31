package auth

import (
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
)

// The settings' defaults, which an installation follows until its values or the Back-office set them: anyone signs up,
// and creates organizations.
const (
	DefaultPolicy    = PolicyAnyone
	DefaultSelfServe = true
)

// InstallationSettings are the sign-up settings an administrator changes in the Back-office, as the database keeps
// them. A nil setting is unset and takes its default; one the chart's values set wins over both
// (Settings.SignUpSettings).
type InstallationSettings struct {
	// SignUp is the sign-up policy, and SignUpDomains the domains PolicyDomains admits.
	SignUp        *Policy
	SignUpDomains []string
	// SelfServe grants every new user Installation user, which creates organizations.
	SelfServe *bool
	// Version counts the changes; a change names the version it read, so one made meanwhile refuses it.
	Version   int64
	UpdatedAt time.Time
	// UpdatedBy is who changed them last: uuid.Nil when nobody has, or they're gone.
	UpdatedBy uuid.UUID
}

// SignUpSettings are the settings a sign-up follows.
type SignUpSettings struct {
	SignUp        Policy
	SignUpDomains []string
	SelfServe     bool
}

// SignUpLocked reports whether the chart's values set the sign-up policy, and its domains, which the Back-office then
// only shows.
func (s Settings) SignUpLocked() bool {
	return s.SignUp != ""
}

// SelfServeLocked reports whether the chart's values set self-serve organizations, which the Back-office then only
// shows.
func (s Settings) SelfServeLocked() bool {
	return s.SelfServe != nil
}

// SignUpSettings are the settings a sign-up follows: each the values' where they set it, else the database's, else its
// default.
func (s Settings) SignUpSettings(db InstallationSettings) SignUpSettings {
	out := SignUpSettings{SignUp: DefaultPolicy, SelfServe: DefaultSelfServe}
	switch {
	case s.SignUpLocked():
		out.SignUp, out.SignUpDomains = s.SignUp, s.SignUpDomains
	case db.SignUp != nil:
		out.SignUp, out.SignUpDomains = *db.SignUp, db.SignUpDomains
	}
	switch {
	case s.SelfServeLocked():
		out.SelfServe = *s.SelfServe
	case db.SelfServe != nil:
		out.SelfServe = *db.SelfServe
	}
	return out
}

// admits reports whether the policy lets c sign up.
func (s SignUpSettings) admits(c Claims, verified bool) bool {
	switch s.SignUp {
	case PolicyAnyone:
		return true
	case PolicyDomains:
		if !verified {
			return false
		}
		_, domain, _ := strings.Cut(c.Email, "@")
		return slices.Contains(s.SignUpDomains, domain)
	default:
		// PolicyInvited: invitations come with organizations, so nobody but a first administrator yet.
		return false
	}
}
