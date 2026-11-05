package graphql

import (
	"context"
	"errors"
	"strings"

	"cobranch.dev/internal/services/auth"
)

// preferencesOf is p as the schema shows them.
func preferencesOf(p auth.Preferences) *Preferences {
	return &Preferences{Theme: Theme(strings.ToUpper(string(p.Theme))), Version: int(p.Version)}
}

// failPreferences maps the preferences' answers, then fail's.
func failPreferences(ctx context.Context, err error, what string) error {
	switch {
	case errors.Is(err, auth.ErrForbidden):
		return refuse(ctx, forbidden, "preferences.people_only", "only a person has preferences, never an agent, a job or an API key")
	case errors.Is(err, auth.ErrPreferencesChanged):
		return refuse(ctx, conflict, "precondition.failed", "your preferences changed since you read them: read them again")
	}
	return fail(ctx, err, "preferences", what)
}

// preferences reads the viewer's preferences.
func (r *Resolver) preferences(ctx context.Context) (*Preferences, error) {
	v, err := viewer(ctx)
	if err != nil {
		return nil, err
	}
	p, err := r.Sessions.Preferences(ctx, v)
	if err != nil {
		return nil, failPreferences(ctx, err, "Reading the viewer's preferences")
	}
	return preferencesOf(p), nil
}

// updatePreferences changes the viewer's preferences input names, from expectedVersion when given and from whichever
// are there otherwise.
func (r *Resolver) updatePreferences(ctx context.Context, input PreferencesPatch, expectedVersion *int) (*Preferences, error) {
	v, err := viewer(ctx)
	if err != nil {
		return nil, err
	}
	patch := auth.PreferencesPatch{Version: auth.AnyPreferences}
	if expectedVersion != nil {
		if *expectedVersion < 0 {
			return nil, refuse(ctx, badUserInput, "version.invalid", "a version of the preferences, from 0")
		}
		patch.Version = int64(*expectedVersion)
	}
	if input.Theme != nil {
		t := auth.Theme(strings.ToLower(string(*input.Theme)))
		patch.Theme = &t
	}
	p, err := r.Sessions.UpdatePreferences(ctx, v, patch)
	if err != nil {
		return nil, failPreferences(ctx, err, "Changing the viewer's preferences")
	}
	return preferencesOf(p), nil
}
