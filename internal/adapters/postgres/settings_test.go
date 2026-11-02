package postgres

import (
	"context"
	"errors"
	"slices"
	"testing"

	"cobranch.dev/internal/services/admin"
	"cobranch.dev/internal/services/auth"
)

func TestInstallationSettings(t *testing.T) {
	t.Parallel()
	f := newOrgFixture(t)
	ctx := context.Background()
	auths := NewAuthStore(f.pool)
	first, err := auths.InstallationSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if first.SignUp != nil || len(first.SignUpDomains) != 0 || first.SelfServe != nil || first.Version != 1 {
		t.Fatalf("InstallationSettings() = %+v, want every setting unset at version 1", first)
	}
	domains, off := auth.PolicyDomains, false
	tests := []struct {
		name        string
		change      func(s auth.InstallationSettings) auth.InstallationSettings
		wantErr     error
		wantVersion int64
	}{
		{name: "the policy and its domains, self-serve off", change: func(s auth.InstallationSettings) auth.InstallationSettings {
			s.SignUp, s.SignUpDomains, s.SelfServe = &domains, []string{"example.com"}, &off
			return s
		}, wantVersion: 2},
		{name: "from an old version", change: func(s auth.InstallationSettings) auth.InstallationSettings {
			s.Version--
			return s
		}, wantErr: admin.ErrStale, wantVersion: 2},
		{name: "the domains policy without a domain", change: func(s auth.InstallationSettings) auth.InstallationSettings {
			s.SignUpDomains = nil
			return s
		}, wantErr: errAny, wantVersion: 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stored, err := f.s.InstallationSettings(ctx)
			if err != nil {
				t.Fatal(err)
			}
			got, err := f.s.ChangeInstallationSettings(ctx, f.olivia, tt.change(stored), now)
			wantError(t, "ChangeInstallationSettings", err, tt.wantErr)
			after, err := auths.InstallationSettings(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if after.Version != tt.wantVersion {
				t.Errorf("version = %d, want %d", after.Version, tt.wantVersion)
			}
			if tt.wantErr == nil && (got.Version != tt.wantVersion || got.UpdatedBy != f.olivia || !got.UpdatedAt.Equal(now) ||
				*got.SignUp != domains || !slices.Equal(got.SignUpDomains, []string{"example.com"}) || *got.SelfServe) {
				t.Errorf("ChangeInstallationSettings() = %+v", got)
			}
		})
	}
	log := auditLog(t, f.pool, "installation_settings")
	if len(log) != 1 || log[0].Action != "update" || log[0].Principal == nil || *log[0].Principal != f.olivia ||
		!slices.Equal(keys(log[0].After), []string{"self_serve", "sign_up", "sign_up_domains", "updated_at", "updated_by", "version"}) {
		t.Errorf("audit log = %+v, want the one change, by Olivia", log)
	}
}

// errAny is any error.
var errAny = errors.New("any error")

// wantError fails t unless err is want, or any error when want is errAny.
func wantError(t *testing.T, call string, err, want error) {
	t.Helper()
	switch {
	case errors.Is(want, errAny) && err == nil, !errors.Is(want, errAny) && !errors.Is(err, want):
		t.Fatalf("%s() error = %v, want %v", call, err, want)
	}
}
