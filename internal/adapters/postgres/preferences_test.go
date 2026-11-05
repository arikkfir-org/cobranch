package postgres

import (
	"context"
	"errors"
	"testing"
	"time"

	"cobranch.dev/internal/services/auth"
)

func TestAuthStorePreferences(t *testing.T) {
	t.Parallel()
	s, _ := authStore(t)
	ctx := context.Background()
	up := alice(t, s)
	if got, err := s.Preferences(ctx, up.Principal.ID); err != nil || got != auth.DefaultPreferences {
		t.Fatalf("Preferences() before a change = %+v, %v, want the defaults at version 0", got, err)
	}
	// The saves run in order, each on the row the ones before it left.
	saves := []struct {
		name    string
		theme   auth.Theme
		version int64
		want    int64 // the version written, or 0 for ErrPreferencesChanged
	}{
		{name: "the first change inserts the row", theme: auth.ThemeDark, version: 0, want: 1},
		{name: "a second first change finds the row there", theme: auth.ThemeLight, version: 0},
		{name: "a change from the version read updates it", theme: auth.ThemeLight, version: 1, want: 2},
		{name: "a change from an older version is refused", theme: auth.ThemeSystem, version: 1},
		{name: "a change from a later version is refused", theme: auth.ThemeSystem, version: 7},
		{name: "the next change from the version read updates it", theme: auth.ThemeSystem, version: 2, want: 3},
	}
	for i, c := range saves {
		at := now.Add(time.Duration(i) * time.Minute)
		got, err := s.SavePreferences(ctx, up.Principal.ID, auth.Preferences{Theme: c.theme, UpdatedAt: at}, c.version)
		if c.want == 0 {
			if !errors.Is(err, auth.ErrPreferencesChanged) {
				t.Errorf("%s: SavePreferences() = %+v, %v, want ErrPreferencesChanged", c.name, got, err)
			}
			continue
		}
		if err != nil || got.Theme != c.theme || got.Version != c.want || !got.UpdatedAt.Equal(at) {
			t.Errorf("%s: SavePreferences() = %+v, %v, want %s at version %d", c.name, got, err, c.theme, c.want)
		}
		if read, err := s.Preferences(ctx, up.Principal.ID); err != nil || read != got {
			t.Errorf("%s: Preferences() = %+v, %v, want %+v", c.name, read, err, got)
		}
	}
	if n := count(t, s.pool, "SELECT count(*) FROM audit_log WHERE table_name = 'preferences' AND principal_id = $1", up.Principal.ID); n != 3 {
		t.Errorf("%d audit rows of the preferences name Alice, want 3: one inserted, two updated", n)
	}
}
