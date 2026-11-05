package auth_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"cobranch.dev/internal/services/auth"
)

// themeOf is a pointer to t, for a patch.
func themeOf(t auth.Theme) *auth.Theme {
	return &t
}

func TestUpdatePreferences(t *testing.T) {
	person := auth.Authenticated{User: auth.User{Principal: auth.Principal{ID: uuid.New(), Kind: auth.KindUser}}}
	dark := auth.Preferences{Theme: auth.ThemeDark, Version: 3, UpdatedAt: now}
	tests := []struct {
		name string
		as   auth.Authenticated
		// saved is what the person saved before, if anything, and racing the changes that land between a read and a
		// save.
		saved  *auth.Preferences
		racing int
		patch  auth.PreferencesPatch
		want   auth.Preferences
		err    error
		// invalid is the field a ValidationError names.
		invalid string
	}{
		{name: "a first change saves version 1", as: person, patch: auth.PreferencesPatch{Theme: themeOf(auth.ThemeDark), Version: 0},
			want: auth.Preferences{Theme: auth.ThemeDark, Version: 1, UpdatedAt: now}},
		{name: "a change from the version read saves the next", as: person, saved: &dark, patch: auth.PreferencesPatch{Theme: themeOf(auth.ThemeLight), Version: 3},
			want: auth.Preferences{Theme: auth.ThemeLight, Version: 4, UpdatedAt: now}},
		{name: "any version changes the one there", as: person, saved: &dark, patch: auth.PreferencesPatch{Theme: themeOf(auth.ThemeSystem), Version: auth.AnyPreferences},
			want: auth.Preferences{Theme: auth.ThemeSystem, Version: 4, UpdatedAt: now}},
		{name: "any version reads again when another change lands first", as: person, saved: &dark, racing: 1,
			patch: auth.PreferencesPatch{Theme: themeOf(auth.ThemeSystem), Version: auth.AnyPreferences},
			want:  auth.Preferences{Theme: auth.ThemeSystem, Version: 5, UpdatedAt: now}},
		{name: "any version gives up to changes that keep landing first", as: person, saved: &dark, racing: 10,
			patch: auth.PreferencesPatch{Theme: themeOf(auth.ThemeSystem), Version: auth.AnyPreferences}, err: auth.ErrPreferencesChanged},
		{name: "the version read is refused when another change lands first", as: person, saved: &dark, racing: 1,
			patch: auth.PreferencesPatch{Theme: themeOf(auth.ThemeLight), Version: 3}, err: auth.ErrPreferencesChanged},
		{name: "a change from another version is refused", as: person, saved: &dark, patch: auth.PreferencesPatch{Theme: themeOf(auth.ThemeLight), Version: 2},
			err: auth.ErrPreferencesChanged},
		{name: "a first change from a version there never was is refused", as: person, patch: auth.PreferencesPatch{Theme: themeOf(auth.ThemeLight), Version: 1},
			err: auth.ErrPreferencesChanged},
		{name: "a change to the theme there writes nothing", as: person, saved: &dark, patch: auth.PreferencesPatch{Theme: themeOf(auth.ThemeDark), Version: 3},
			want: dark},
		{name: "an empty patch writes nothing", as: person, patch: auth.PreferencesPatch{Version: 0}, want: auth.DefaultPreferences},
		{name: "a theme there isn't is refused", as: person, patch: auth.PreferencesPatch{Theme: themeOf("sepia"), Version: 0}, invalid: "theme"},
		{name: "an agent has no preferences", as: auth.Authenticated{User: auth.User{Principal: auth.Principal{ID: uuid.New(), Kind: auth.KindAgent}}},
			patch: auth.PreferencesPatch{Theme: themeOf(auth.ThemeDark)}, err: auth.ErrForbidden},
		{name: "an API key has no preferences", as: auth.Authenticated{User: auth.User{Principal: auth.Principal{ID: uuid.New(), Kind: auth.KindAPIKey}}},
			patch: auth.PreferencesPatch{Theme: themeOf(auth.ThemeDark)}, err: auth.ErrForbidden},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, store, _ := newService(auth.Settings{}, alice)
			if tt.saved != nil {
				store.Saved[tt.as.User.ID] = *tt.saved
			}
			store.Racing = tt.racing
			got, err := s.UpdatePreferences(context.Background(), tt.as, tt.patch)
			if !answered(t, got, err, tt.want, tt.err, tt.invalid) {
				return
			}
			if read, err := s.Preferences(context.Background(), tt.as); err != nil || read != tt.want {
				t.Errorf("Preferences() after = %+v, %v, want %+v", read, err, tt.want)
			}
		})
	}
}

// answered checks what UpdatePreferences answered: a ValidationError of invalid when it names one, else wantErr when
// it is one, else want. It reports whether the change was made.
func answered(t *testing.T, got auth.Preferences, err error, want auth.Preferences, wantErr error, invalid string) bool {
	t.Helper()
	var verr *auth.ValidationError
	switch {
	case invalid != "":
		if !errors.As(err, &verr) || verr.Field != invalid {
			t.Fatalf("UpdatePreferences() = %+v, %v, want a ValidationError of %s", got, err, invalid)
		}
		return false
	case wantErr != nil:
		if !errors.Is(err, wantErr) {
			t.Fatalf("UpdatePreferences() = %+v, %v, want %v", got, err, wantErr)
		}
		return false
	case err != nil || got != want:
		t.Fatalf("UpdatePreferences() = %+v, %v, want %+v", got, err, want)
	}
	return true
}

func TestPreferences(t *testing.T) {
	tests := []struct {
		name string
		kind auth.Kind
		want auth.Preferences
		err  error
	}{
		{name: "a person who changed nothing has the defaults", kind: auth.KindUser, want: auth.DefaultPreferences},
		{name: "an agent has none", kind: auth.KindAgent, err: auth.ErrForbidden},
		{name: "a job has none", kind: auth.KindJob, err: auth.ErrForbidden},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, _, _ := newService(auth.Settings{}, alice)
			as := auth.Authenticated{User: auth.User{Principal: auth.Principal{ID: uuid.New(), Kind: tt.kind}}}
			got, err := s.Preferences(context.Background(), as)
			if !errors.Is(err, tt.err) || got != tt.want {
				t.Errorf("Preferences() = %+v, %v, want %+v, %v", got, err, tt.want, tt.err)
			}
		})
	}
}
