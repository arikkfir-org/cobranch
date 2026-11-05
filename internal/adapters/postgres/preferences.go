package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"cobranch.dev/internal/adapters/postgres/queries"
	"cobranch.dev/internal/services/auth"
)

// preferences is row as the domain sees it.
func preferences(row queries.Preference) auth.Preferences {
	return auth.Preferences{Theme: auth.Theme(row.Theme), Version: row.Version, UpdatedAt: row.UpdatedAt}
}

// Preferences reads a person's preferences: auth.DefaultPreferences, at version 0, until they first change one.
func (s *AuthStore) Preferences(ctx context.Context, userID uuid.UUID) (auth.Preferences, error) {
	row, err := s.q.Preferences(ctx, userID)
	if errors.Is(err, pgx.ErrNoRows) {
		return auth.DefaultPreferences, nil
	}
	if err != nil {
		return auth.Preferences{}, fmt.Errorf("reading the preferences of %s: %w", userID, err)
	}
	return preferences(row), nil
}

// SavePreferences writes a person's preferences as them, if they are still at version: inserting the row at version
// 0, updating it at any other. auth.ErrPreferencesChanged when another change came first.
func (s *AuthStore) SavePreferences(ctx context.Context, userID uuid.UUID, p auth.Preferences, version int64) (auth.Preferences, error) {
	var saved auth.Preferences
	err := s.transact(ctx, "saving the preferences", userID, func(ctx context.Context, q *queries.Queries) error {
		var row queries.Preference
		var err error
		if version == 0 {
			row, err = q.InsertPreferences(ctx, userID, string(p.Theme), p.UpdatedAt)
		} else {
			row, err = q.UpdatePreferences(ctx, queries.UpdatePreferencesParams{Theme: string(p.Theme), UpdatedAt: p.UpdatedAt,
				PrincipalID: userID, Version: version})
		}
		if errors.Is(err, pgx.ErrNoRows) {
			return auth.ErrPreferencesChanged
		}
		if err != nil {
			return fmt.Errorf("writing them: %w", err)
		}
		saved = preferences(row)
		return nil
	})
	if err != nil {
		return auth.Preferences{}, err
	}
	return saved, nil
}
