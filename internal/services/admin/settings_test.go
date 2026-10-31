package admin_test

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"

	"cobranch.dev/internal/services/admin"
	"cobranch.dev/internal/services/auth"
)

// withValues is f's service under the values' sign-up settings.
func withValues(f *fixture, values auth.Settings) *admin.Service {
	s := admin.NewService(f.store, auth.NewAuthorizer(f.store), values)
	s.SetClock(func() time.Time { return now })
	return s
}

func TestSettings(t *testing.T) {
	off := false
	invited := auth.PolicyInvited
	tests := []struct {
		name       string
		values     auth.Settings
		stored     auth.InstallationSettings
		viewer     func(f *fixture) auth.User
		want       auth.SignUpSettings
		wantLocked []string
		wantErr    error
	}{
		{name: "the defaults", stored: auth.InstallationSettings{Version: 1}, want: auth.SignUpSettings{SignUp: auth.PolicyAnyone, SelfServe: true}},
		{name: "the Back-office's", stored: auth.InstallationSettings{SignUp: &invited, SelfServe: &off, Version: 3},
			want: auth.SignUpSettings{SignUp: auth.PolicyInvited}},
		{name: "the values', locked", values: auth.Settings{SignUp: auth.PolicyDomains, SignUpDomains: []string{"example.com"}, SelfServe: &off},
			stored: auth.InstallationSettings{SignUp: &invited, Version: 3},
			want:   auth.SignUpSettings{SignUp: auth.PolicyDomains, SignUpDomains: []string{"example.com"}}, wantLocked: []string{admin.SettingSignUp, admin.SettingSelfServe}},
		{name: "not an administrator", viewer: func(f *fixture) auth.User { return f.uma }, wantErr: auth.ErrForbidden},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture(t)
			f.store.Installation = tt.stored
			v := f.ada
			if tt.viewer != nil {
				v = tt.viewer(f)
			}
			got, err := withValues(f, tt.values).Settings(context.Background(), v)
			if failed(t, "Settings", err, tt.wantErr, "") {
				return
			}
			if got.SignUp != tt.want.SignUp || !slices.Equal(got.SignUpDomains, tt.want.SignUpDomains) || got.SelfServe != tt.want.SelfServe ||
				!slices.Equal(got.Locked, tt.wantLocked) || got.Version != tt.stored.Version {
				t.Errorf("Settings() = %+v, want %+v locked %v", got, tt.want, tt.wantLocked)
			}
		})
	}
}

func TestChangeSettings(t *testing.T) {
	on, off := true, false
	anyone, domains, unknown := auth.PolicyAnyone, auth.PolicyDomains, auth.Policy("everyone")
	list := func(d ...string) *[]string { return &d }
	tests := []struct {
		name        string
		values      auth.Settings
		viewer      func(f *fixture) auth.User
		stale       bool
		change      admin.SettingsChange
		want        auth.SignUpSettings
		wantVersion int64
		wantErr     error
		wantCode    string
	}{
		{name: "the policy and its domains, normalized", change: admin.SettingsChange{SignUp: &domains, SignUpDomains: list(" Example.COM", "example.org", "example.com", "")},
			want: auth.SignUpSettings{SignUp: auth.PolicyDomains, SignUpDomains: []string{"example.com", "example.org"}, SelfServe: true}, wantVersion: 2},
		{name: "self-serve alone", change: admin.SettingsChange{SelfServe: &off}, want: auth.SignUpSettings{SignUp: auth.PolicyAnyone}, wantVersion: 2},
		{name: "nothing", want: auth.SignUpSettings{SignUp: auth.PolicyAnyone, SelfServe: true}, wantVersion: 2},
		{name: "self-serve while the values lock sign-up", values: auth.Settings{SignUp: auth.PolicyInvited}, change: admin.SettingsChange{SelfServe: &off},
			want: auth.SignUpSettings{SignUp: auth.PolicyInvited}, wantVersion: 2},
		{name: "a policy the values lock", values: auth.Settings{SignUp: auth.PolicyInvited}, change: admin.SettingsChange{SignUp: &anyone}, wantErr: admin.ErrLocked},
		{name: "domains the values lock", values: auth.Settings{SignUp: auth.PolicyInvited}, change: admin.SettingsChange{SignUpDomains: list("example.com")}, wantErr: admin.ErrLocked},
		{name: "self-serve the values lock", values: auth.Settings{SelfServe: &on}, change: admin.SettingsChange{SelfServe: &off}, wantErr: admin.ErrLocked},
		{name: "settings changed meanwhile", stale: true, change: admin.SettingsChange{SelfServe: &off}, wantErr: admin.ErrStale},
		{name: "not a policy", change: admin.SettingsChange{SignUp: &unknown}, wantCode: "sign_up.invalid"},
		{name: "the domains policy without a domain", change: admin.SettingsChange{SignUp: &domains}, wantCode: "sign_up_domains.required"},
		{name: "the domains policy, its domains emptied", change: admin.SettingsChange{SignUp: &domains, SignUpDomains: list(" ")}, wantCode: "sign_up_domains.required"},
		{name: "not a domain", change: admin.SettingsChange{SignUpDomains: list("example")}, wantCode: "domain.invalid"},
		{name: "an email for a domain", change: admin.SettingsChange{SignUpDomains: list("a@example.com")}, wantCode: "domain.invalid"},
		{name: "not an administrator", viewer: func(f *fixture) auth.User { return f.uma }, change: admin.SettingsChange{SelfServe: &off}, wantErr: auth.ErrForbidden},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture(t)
			s := withValues(f, tt.values)
			read, err := s.Settings(context.Background(), f.ada)
			if err != nil {
				t.Fatal(err)
			}
			if tt.stale {
				f.store.Installation.Version++
			}
			v := f.ada
			if tt.viewer != nil {
				v = tt.viewer(f)
			}
			got, err := s.ChangeSettings(context.Background(), v, read.Fingerprint(), tt.change)
			if failed(t, "ChangeSettings", err, tt.wantErr, tt.wantCode) {
				if f.store.Installation.UpdatedBy != uuid.Nil {
					t.Errorf("a refused change was written: %+v", f.store.Installation)
				}
				return
			}
			checkChanged(t, got, read, tt.want, tt.wantVersion, f.ada.ID)
		})
	}
}

// checkChanged fails t unless got holds want at version, changed by who now, with a fingerprint other than before's.
func checkChanged(t *testing.T, got, before admin.Settings, want auth.SignUpSettings, version int64, who uuid.UUID) {
	t.Helper()
	if got.SignUp != want.SignUp || !slices.Equal(got.SignUpDomains, want.SignUpDomains) || got.SelfServe != want.SelfServe ||
		got.Version != version || got.UpdatedBy != who || !got.UpdatedAt.Equal(now) {
		t.Errorf("ChangeSettings() = %+v, want %+v at version %d by %s", got, want, version, who)
	}
	if got.Fingerprint() == before.Fingerprint() {
		t.Error("the fingerprint didn't change with the version")
	}
}

func TestChangeSettingsAnyVersion(t *testing.T) {
	f := newFixture(t)
	off := false
	got, err := f.service.ChangeSettings(context.Background(), f.ada, "*", admin.SettingsChange{SelfServe: &off})
	if err != nil || got.SelfServe {
		t.Errorf("ChangeSettings(*) = %+v, %v; want self-serve off", got, err)
	}
	if _, err := f.service.ChangeSettings(context.Background(), f.ada, "not-a-fingerprint", admin.SettingsChange{SelfServe: &off}); !errors.Is(err, admin.ErrStale) {
		t.Errorf("ChangeSettings(another fingerprint) error = %v, want ErrStale", err)
	}
}
