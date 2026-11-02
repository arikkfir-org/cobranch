package admin

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"

	"cobranch.dev/internal/services/auth"
	"cobranch.dev/internal/services/limits"
	"cobranch.dev/internal/services/orgs"
)

// The settings' names, as the Back-office and the values' locks name them.
const (
	SettingSignUp                      = "signUp"
	SettingSelfServe                   = "selfServe"
	SettingOrganizationsPerUser        = "organizationsPerUser"
	SettingRepositoriesPerOrganization = "repositoriesPerOrganization"
	SettingGitDataPerOrganization      = "gitDataPerOrganization"
)

var (
	// ErrStale is a change to settings, or to a provider, that changed since the change read them.
	ErrStale = errors.New("it changed since it was read")
	// ErrLocked is a change to a setting, or to a provider, the chart's values set: only they change it.
	ErrLocked = errors.New("the chart's values set this")
)

// StoredSettings are the installation's settings as the database keeps them: those a sign-up follows, and the
// installation's limits.
type StoredSettings struct {
	auth.InstallationSettings
	// Limits are the limits an administrator set: nil where they set none.
	Limits limits.Settings
}

// Settings are the installation's settings as the Back-office shows them: those a sign-up follows, the limits, which
// of them the chart's values lock, and when the database's last changed, by whom.
type Settings struct {
	auth.SignUpSettings
	// Limits are the installation's limits: each the values', else an administrator's, else the built-in default.
	Limits limits.Limits
	// Locked names the settings the chart's values set (SettingSignUp, SettingSelfServe and the limits'), which the
	// Back-office only shows.
	Locked []string
	// Version counts the database's changes.
	Version   int64
	UpdatedAt time.Time
	// UpdatedBy is who changed them last: uuid.Nil when nobody has, or they're gone.
	UpdatedBy uuid.UUID
}

// Fingerprint changes whenever what the settings show does: the database's version, or the values' settings and locks.
// The Admin API's ETag is made of it.
func (s Settings) Fingerprint() string {
	sum := sha256.Sum256(fmt.Appendf(nil, "%d|%s|%s|%t|%d|%d|%d|%s", s.Version, s.SignUp, strings.Join(s.SignUpDomains, ","),
		s.SelfServe, s.Limits.OrganizationsPerUser, s.Limits.RepositoriesPerOrganization, s.Limits.GitDataPerOrganization,
		strings.Join(s.Locked, ",")))
	return hex.EncodeToString(sum[:12])
}

// SettingsChange changes some settings: a nil field stays as it is.
type SettingsChange struct {
	SignUp        *auth.Policy
	SignUpDomains *[]string
	SelfServe     *bool
	// The installation's limits, each 0 for none, or more.
	OrganizationsPerUser        *int64
	RepositoriesPerOrganization *int64
	GitDataPerOrganization      *int64
}

// MaxDomains is the most domains the domains policy lists.
const MaxDomains = 100

// domainPattern is a lowercase DNS name of at least two labels.
var domainPattern = regexp.MustCompile(`^(?:[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$`)

// Settings reads the installation's settings; it takes installation.manage.
func (s *Service) Settings(ctx context.Context, v auth.User) (Settings, error) {
	if err := s.require(ctx, v, PermManage); err != nil {
		return Settings{}, err
	}
	stored, err := s.store.InstallationSettings(ctx)
	if err != nil {
		return Settings{}, fmt.Errorf("reading the settings: %w", err)
	}
	return s.show(stored), nil
}

// ChangeSettings makes change to the settings, unless they changed since the caller read them, as their fingerprint
// match says (* for any): then ErrStale. A setting the values lock refuses any change (ErrLocked); it takes
// installation.manage.
func (s *Service) ChangeSettings(ctx context.Context, v auth.User, match string, change SettingsChange) (Settings, error) {
	if err := s.require(ctx, v, PermManage); err != nil {
		return Settings{}, err
	}
	stored, err := s.store.InstallationSettings(ctx)
	if err != nil {
		return Settings{}, fmt.Errorf("reading the settings: %w", err)
	}
	if match != "*" && s.show(stored).Fingerprint() != match {
		return Settings{}, ErrStale
	}
	next, err := s.apply(stored, change)
	if err != nil {
		return Settings{}, err
	}
	written, err := s.store.ChangeInstallationSettings(ctx, v.ID, next, s.now())
	if err != nil {
		return Settings{}, fmt.Errorf("changing the settings: %w", err)
	}
	return s.show(written), nil
}

// show is what the Back-office shows of the stored settings, under the values'.
func (s *Service) show(stored StoredSettings) Settings {
	out := Settings{SignUpSettings: s.values.SignUpSettings(stored.InstallationSettings), Limits: limits.Resolve(s.limitValues, stored.Limits),
		Version: stored.Version, UpdatedAt: stored.UpdatedAt, UpdatedBy: stored.UpdatedBy}
	if s.values.SignUpLocked() {
		out.Locked = append(out.Locked, SettingSignUp)
	}
	if s.values.SelfServeLocked() {
		out.Locked = append(out.Locked, SettingSelfServe)
	}
	for _, l := range s.limitSettings(nil, SettingsChange{}) {
		if l.locked {
			out.Locked = append(out.Locked, l.name)
		}
	}
	return out
}

// apply makes change to the stored settings, or refuses it: a setting the values lock, or a value that isn't one.
func (s *Service) apply(stored StoredSettings, change SettingsChange) (StoredSettings, error) {
	signUp, err := s.applySignUp(stored.InstallationSettings, change)
	if err != nil {
		return stored, err
	}
	next := StoredSettings{InstallationSettings: signUp, Limits: stored.Limits}
	for _, l := range s.limitSettings(&next.Limits, change) {
		switch {
		case l.change == nil:
			continue
		case l.locked:
			return stored, fmt.Errorf("%w: %s", ErrLocked, l.name)
		case *l.change < 0:
			return stored, &orgs.ValidationError{Field: l.name, Code: "limit.invalid", Message: "a limit is 0, for none, or more"}
		}
		value := *l.change
		*l.to = &value
	}
	return next, nil
}

// limitSetting is one of the installation's limits as a change to the settings meets it: its name, whether the values
// lock it, the change's value, and where the change writes it.
type limitSetting struct {
	name   string
	locked bool
	change *int64
	to     **int64
}

// limitSettings are the installation's limits, as change meets them, written to into.
func (s *Service) limitSettings(into *limits.Settings, change SettingsChange) []limitSetting {
	if into == nil {
		into = &limits.Settings{}
	}
	return []limitSetting{
		{SettingOrganizationsPerUser, s.limitValues.OrganizationsPerUser != nil, change.OrganizationsPerUser, &into.OrganizationsPerUser},
		{SettingRepositoriesPerOrganization, s.limitValues.RepositoriesPerOrganization != nil, change.RepositoriesPerOrganization, &into.RepositoriesPerOrganization},
		{SettingGitDataPerOrganization, s.limitValues.GitDataPerOrganization != nil, change.GitDataPerOrganization, &into.GitDataPerOrganization},
	}
}

// applySignUp makes change to the stored sign-up settings, or refuses it: a setting the values lock, or a value that
// isn't one.
func (s *Service) applySignUp(stored auth.InstallationSettings, change SettingsChange) (auth.InstallationSettings, error) {
	next := stored
	if change.SignUp != nil || change.SignUpDomains != nil {
		if s.values.SignUpLocked() {
			return stored, fmt.Errorf("%w: %s", ErrLocked, SettingSignUp)
		}
		if change.SignUp != nil {
			policy, err := auth.ParsePolicy(string(*change.SignUp))
			if err != nil {
				return stored, &orgs.ValidationError{Field: "signUp", Code: "sign_up.invalid", Message: "the sign-up policy is anyone, domains or invited"}
			}
			next.SignUp = &policy
		}
		if change.SignUpDomains != nil {
			domains, err := normalizeDomains(*change.SignUpDomains)
			if err != nil {
				return stored, err
			}
			next.SignUpDomains = domains
		}
		if next.SignUp != nil && *next.SignUp == auth.PolicyDomains && len(next.SignUpDomains) == 0 {
			return stored, &orgs.ValidationError{Field: "signUpDomains", Code: "sign_up_domains.required", Message: "the domains policy needs at least one domain"}
		}
	}
	if change.SelfServe != nil {
		if s.values.SelfServeLocked() {
			return stored, fmt.Errorf("%w: %s", ErrLocked, SettingSelfServe)
		}
		selfServe := *change.SelfServe
		next.SelfServe = &selfServe
	}
	return next, nil
}

// normalizeDomains lowercases and trims domains, drops empty and repeated ones and sorts them, or refuses one that
// isn't a domain, or more than MaxDomains.
func normalizeDomains(domains []string) ([]string, error) {
	out := make([]string, 0, len(domains))
	for _, d := range domains {
		d = strings.ToLower(strings.TrimSpace(d))
		switch {
		case d == "" || slices.Contains(out, d):
			continue
		case len(d) > 253 || !domainPattern.MatchString(d):
			return nil, &orgs.ValidationError{Field: "signUpDomains", Code: "domain.invalid", Message: fmt.Sprintf("%q isn't a domain, such as example.com", d)}
		}
		out = append(out, d)
	}
	if len(out) > MaxDomains {
		return nil, &orgs.ValidationError{Field: "signUpDomains", Code: "sign_up_domains.too_many", Message: fmt.Sprintf("list at most %d domains", MaxDomains)}
	}
	slices.Sort(out)
	return out, nil
}
