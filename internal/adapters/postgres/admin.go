package postgres

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"cobranch.dev/internal/adapters/postgres/queries"
	"cobranch.dev/internal/services/admin"
	"cobranch.dev/internal/services/auth"
)

// The organizations' store keeps the installation's administration too: the Back-office's lists, an organization made
// for a user, and entering one (db/queries/admin.sql).
var _ admin.Store = (*OrgStore)(nil)

// likeEscaper escapes the wildcards of ILIKE, and its escape character, in a search.
var likeEscaper = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)

// contains is the ILIKE pattern that matches a value holding query.
func contains(query string) string {
	return "%" + likeEscaper.Replace(query) + "%"
}

// after is the cursor's ID as a query takes it: NULL for the first page.
func after(id uuid.UUID) *uuid.UUID {
	if id == uuid.Nil {
		return nil
	}
	return &id
}

// AllOrganizations lists a page of the organizations, newest first, with their member counts.
func (s *OrgStore) AllOrganizations(ctx context.Context, page admin.Page) ([]admin.Organization, error) {
	rows, err := s.q.AdminOrganizations(ctx, after(page.After), contains(page.Query), int32(page.Limit))
	if err != nil {
		return nil, fmt.Errorf("listing the organizations: %w", err)
	}
	out := make([]admin.Organization, 0, len(rows))
	for _, r := range rows {
		o := r.Organization
		out = append(out, admin.Organization{ID: o.ID, Slug: o.Slug, Name: o.Name, CreatedAt: o.CreatedAt, Members: int(r.Members)})
	}
	return out, nil
}

// AllUsers lists a page of the users, newest first.
func (s *OrgStore) AllUsers(ctx context.Context, page admin.Page) ([]admin.User, error) {
	rows, err := s.q.AdminUsers(ctx, after(page.After), contains(page.Query), int32(page.Limit))
	if err != nil {
		return nil, fmt.Errorf("listing the users: %w", err)
	}
	out := make([]admin.User, 0, len(rows))
	for _, r := range rows {
		u := admin.User{ID: r.ID, DisplayName: r.DisplayName, Issuers: r.Issuers, Roles: r.Roles, Organizations: int(r.Organizations),
			Suspended: r.Suspended, CreatedAt: r.CreatedAt, LastSignInAt: r.LastSignInAt}
		for i, e := range r.Emails {
			u.Emails = append(u.Emails, auth.Email{Email: e, Verified: i < len(r.Verified) && r.Verified[i]})
		}
		out = append(out, u)
	}
	return out, nil
}

// VerifiedEmailHolder finds the user who holds email, verified.
func (s *OrgStore) VerifiedEmailHolder(ctx context.Context, email string) (uuid.UUID, error) {
	row, err := s.q.EmailHolder(ctx, email)
	if err != nil {
		return uuid.Nil, wrapOrg("finding the email's holder", err)
	}
	return row.PrincipalID, nil
}

// Enter makes actor an Owner of the organization, with a grant of source entry, joining it first when they aren't a
// member, and records the entry and its reason. It holds the organization's lock, as every grant does.
func (s *OrgStore) Enter(ctx context.Context, actor, orgID uuid.UUID, reason string, at time.Time) error {
	return s.transact(ctx, "entering the organization", actor, func(ctx context.Context, q *queries.Queries) error {
		if _, err := q.LockOrganization(ctx, orgID); err != nil {
			return wrapOrg("locking the organization", err)
		}
		_, err := q.Membership(ctx, orgID, actor)
		member := err == nil
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("reading the membership: %w", err)
		}
		if !member {
			if err := q.InsertMembership(ctx, orgID, actor, at); err != nil {
				return fmt.Errorf("inserting the membership: %w", err)
			}
		}
		granted, err := q.InsertGrant(ctx, queries.InsertGrantParams{
			PrincipalID: actor, RoleID: auth.RoleOwner, Scope: string(auth.ScopeOrganization), ScopeID: &orgID, Source: string(auth.SourceEntry),
		})
		if err != nil {
			return fmt.Errorf("inserting the grant: %w", err)
		}
		switch {
		case !member:
			if err := dropPending(ctx, q, orgID, actor); err != nil {
				return err
			}
			err = orgEvent(ctx, q, &orgID, actor, "member.joined", actor, map[string]any{"role": auth.RoleOwner, "how": "entered"})
		case granted == 1:
			err = orgEvent(ctx, q, &orgID, actor, "role.granted", actor, map[string]any{"role": auth.RoleOwner})
		}
		if err != nil {
			return err
		}
		return orgEvent(ctx, q, &orgID, actor, "org.entered", orgID, map[string]any{"reason": reason})
	})
}
