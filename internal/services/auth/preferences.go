package auth

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// Theme is the colours the Console draws in: light, dark, or system, which follows the operating system's.
type Theme string

// The themes a person picks from.
const (
	ThemeSystem Theme = "system"
	ThemeLight  Theme = "light"
	ThemeDark   Theme = "dark"
)

// Valid reports whether t is one of the themes.
func (t Theme) Valid() bool {
	return t == ThemeSystem || t == ThemeLight || t == ThemeDark
}

// Preferences are how Cobranch looks to a person. They are kept with the person rather than in a browser, so they
// follow the person to every browser they sign in from.
type Preferences struct {
	Theme Theme
	// Version counts the changes, and is the preferences' ETag: 0 until the person first changes one.
	Version   int64
	UpdatedAt time.Time
}

// DefaultPreferences are a person's preferences until they change one: the system's theme.
var DefaultPreferences = Preferences{Theme: ThemeSystem}

// PreferencesPatch changes the preferences it names, from the version the person read; AnyPreferences changes
// whichever version is there (If-Match: *).
type PreferencesPatch struct {
	Theme   *Theme
	Version int64
}

// AnyPreferences is a PreferencesPatch's version that matches any.
const AnyPreferences int64 = -1

// ErrPreferencesChanged is a change to preferences that changed since the version it read.
var ErrPreferencesChanged = errors.New("the preferences changed since they were read")

// Preferences reads the person a signs in as's preferences, the defaults until they change one. Only a person has
// them: an agent's or an API key's credential answers ErrForbidden.
func (s *Service) Preferences(ctx context.Context, a Authenticated) (Preferences, error) {
	if a.User.Kind != KindUser {
		return Preferences{}, fmt.Errorf("%w: preferences are a person's", ErrForbidden)
	}
	p, err := s.store.Preferences(ctx, a.User.ID)
	if err != nil {
		return Preferences{}, fmt.Errorf("reading the preferences of %s: %w", a.User.ID, err)
	}
	return p, nil
}

// UpdatePreferences changes the preferences the patch names, of the person a signs in as, and answers them as they
// now are; ErrPreferencesChanged when they changed since the version the patch read, and a ValidationError for a
// theme there isn't. A patch that changes nothing writes nothing.
func (s *Service) UpdatePreferences(ctx context.Context, a Authenticated, patch PreferencesPatch) (Preferences, error) {
	if a.User.Kind != KindUser {
		return Preferences{}, fmt.Errorf("%w: preferences are a person's", ErrForbidden)
	}
	if patch.Theme != nil && !patch.Theme.Valid() {
		return Preferences{}, &ValidationError{Field: "theme", Code: "preferences.theme_invalid", Message: "a theme is light, dark or system"}
	}
	current, err := s.store.Preferences(ctx, a.User.ID)
	if err != nil {
		return Preferences{}, fmt.Errorf("reading the preferences of %s: %w", a.User.ID, err)
	}
	if patch.Version != AnyPreferences && patch.Version != current.Version {
		return Preferences{}, ErrPreferencesChanged
	}
	next := current
	if patch.Theme != nil {
		next.Theme = *patch.Theme
	}
	if next.Theme == current.Theme {
		return current, nil
	}
	next.UpdatedAt = s.now()
	saved, err := s.store.SavePreferences(ctx, a.User.ID, next, current.Version)
	if err != nil {
		return Preferences{}, fmt.Errorf("saving the preferences of %s: %w", a.User.ID, err)
	}
	return saved, nil
}
