package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"cobranch.dev/internal/adapters/postgres/queries"
	"cobranch.dev/internal/services/admin"
	"cobranch.dev/internal/services/auth"
	"cobranch.dev/internal/services/limits"
)

// installationSettings is the domain's view of the installation_settings row.
func installationSettings(row queries.InstallationSetting) auth.InstallationSettings {
	out := auth.InstallationSettings{SignUpDomains: row.SignUpDomains, SelfServe: row.SelfServe, Version: row.Version,
		UpdatedAt: row.UpdatedAt}
	if row.SignUp != nil {
		policy := auth.Policy(*row.SignUp)
		out.SignUp = &policy
	}
	if row.UpdatedBy != nil {
		out.UpdatedBy = *row.UpdatedBy
	}
	return out
}

// readInstallationSettings reads the installation_settings row with q.
func readInstallationSettings(ctx context.Context, q *queries.Queries) (auth.InstallationSettings, error) {
	row, err := q.InstallationSettings(ctx)
	if err != nil {
		return auth.InstallationSettings{}, wrap("reading the installation's settings", err)
	}
	return installationSettings(row), nil
}

// InstallationSettings reads the settings the Back-office changes, for a sign-up.
func (s *AuthStore) InstallationSettings(ctx context.Context) (auth.InstallationSettings, error) {
	return readInstallationSettings(ctx, s.q)
}

// storedSettings is the Back-office's view of the installation_settings row: sign-up's settings and the limits.
func storedSettings(row queries.InstallationSetting) admin.StoredSettings {
	return admin.StoredSettings{InstallationSettings: installationSettings(row), Limits: limits.Settings{
		OrganizationsPerUser: row.OrganizationsPerUser, RepositoriesPerOrganization: row.RepositoriesPerOrganization,
		GitDataPerOrganization: row.GitDataPerOrganization,
	}}
}

// InstallationSettings reads the settings the Back-office changes.
func (s *OrgStore) InstallationSettings(ctx context.Context) (admin.StoredSettings, error) {
	row, err := s.q.InstallationSettings(ctx)
	if err != nil {
		return admin.StoredSettings{}, wrap("reading the installation's settings", err)
	}
	return storedSettings(row), nil
}

// ChangeInstallationSettings writes settings, acting for actor, unless the row's version is no longer theirs: then
// admin.ErrStale.
func (s *OrgStore) ChangeInstallationSettings(ctx context.Context, actor uuid.UUID, settings admin.StoredSettings, at time.Time) (admin.StoredSettings, error) {
	params := queries.ChangeInstallationSettingsParams{SignUpDomains: settings.SignUpDomains, SelfServe: settings.SelfServe,
		OrganizationsPerUser: settings.Limits.OrganizationsPerUser, RepositoriesPerOrganization: settings.Limits.RepositoriesPerOrganization,
		GitDataPerOrganization: settings.Limits.GitDataPerOrganization, UpdatedAt: at, UpdatedBy: &actor, Version: settings.Version}
	if params.SignUpDomains == nil {
		params.SignUpDomains = []string{}
	}
	if settings.SignUp != nil {
		policy := string(*settings.SignUp)
		params.SignUp = &policy
	}
	var written admin.StoredSettings
	err := s.transact(ctx, "changing the installation's settings", actor, func(ctx context.Context, q *queries.Queries) error {
		row, err := q.ChangeInstallationSettings(ctx, params)
		if errors.Is(err, pgx.ErrNoRows) {
			return admin.ErrStale
		}
		if err != nil {
			return fmt.Errorf("writing them: %w", err)
		}
		written = storedSettings(row)
		return nil
	})
	if err != nil {
		return admin.StoredSettings{}, err
	}
	return written, nil
}
