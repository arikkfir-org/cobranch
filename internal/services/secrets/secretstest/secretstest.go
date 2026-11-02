// Package secretstest holds a fake of the secrets domain's port, for the tests of its service and of the adapters
// that serve it: a Store in memory over an orgstest.Store, which keeps the grants the authorizer reads.
package secretstest

import (
	"context"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"

	"cobranch.dev/internal/services/auth"
	"cobranch.dev/internal/services/orgs/orgstest"
	"cobranch.dev/internal/services/secrets"
)

// Other is a principal of an organization besides its members and API keys: one of its jobs, of RepoID, or agents.
type Other struct {
	OrgID, RepoID uuid.UUID
	Principal     secrets.Principal
}

// Store is a secrets.Store in memory. Its grants are Orgs', so one authorizer over Orgs reads both. Fail, when set,
// fails every write.
type Store struct {
	Orgs    *orgstest.Store
	Secrets map[uuid.UUID]secrets.Secret
	// Values holds each secret's sealed value.
	Values map[uuid.UUID][]byte
	Others map[uuid.UUID]Other
	Events []string
	Fail   error
	// Now is the time the store writes.
	Now time.Time
}

var _ secrets.Store = (*Store)(nil)

// NewStore returns an empty Store over orgs.
func NewStore(o *orgstest.Store) *Store {
	return &Store{Orgs: o, Secrets: map[uuid.UUID]secrets.Secret{}, Values: map[uuid.UUID][]byte{}, Others: map[uuid.UUID]Other{},
		Now: time.Date(2026, 11, 2, 9, 0, 0, 0, time.UTC)}
}

// Add records a secret as it stands, with its sealed value.
func (s *Store) Add(secret secrets.Secret, sealed []byte) {
	if secret.Version == 0 {
		secret.Version = 1
	}
	s.Secrets[secret.ID], s.Values[secret.ID] = secret, sealed
	s.Orgs.Secrets[secret.ID] = [2]uuid.UUID{secret.OrgID, secret.RepoID}
}

// List lists the secrets where they live, by name.
func (s *Store) List(_ context.Context, orgID, repoID uuid.UUID) ([]secrets.Secret, error) {
	var out []secrets.Secret
	for _, secret := range s.Secrets {
		if secret.OrgID == orgID && secret.RepoID == repoID {
			out = append(out, secret)
		}
	}
	slices.SortFunc(out, func(a, b secrets.Secret) int { return strings.Compare(a.Name, b.Name) })
	return out, nil
}

// Get finds a secret.
func (s *Store) Get(_ context.Context, id uuid.UUID) (secrets.Secret, error) {
	secret, ok := s.Secrets[id]
	if !ok {
		return secrets.Secret{}, secrets.ErrNotFound
	}
	return secret, nil
}

// taken reports whether another secret where secret lives has its name.
func (s *Store) taken(secret secrets.Secret) bool {
	for _, other := range s.Secrets {
		if other.ID != secret.ID && other.OrgID == secret.OrgID && other.RepoID == secret.RepoID && other.Name == secret.Name {
			return true
		}
	}
	return false
}

// Create writes the secret.
func (s *Store) Create(_ context.Context, actor uuid.UUID, secret secrets.Secret, sealed []byte) (secrets.Secret, error) {
	if s.Fail != nil {
		return secrets.Secret{}, s.Fail
	}
	if secret.RepoID != uuid.Nil && s.Orgs.Repos[secret.RepoID] != secret.OrgID {
		return secrets.Secret{}, secrets.ErrNotFound
	}
	if s.taken(secret) {
		return secrets.Secret{}, secrets.ErrNameTaken
	}
	secret.Version, secret.CreatedBy, secret.CreatedAt, secret.UpdatedBy, secret.UpdatedAt = 1, actor, s.Now, actor, s.Now
	s.Add(secret, sealed)
	s.Events = append(s.Events, "secret.created")
	return secret, nil
}

// current finds the secret at version.
func (s *Store) current(id uuid.UUID, version int64) (secrets.Secret, error) {
	secret, ok := s.Secrets[id]
	switch {
	case !ok:
		return secrets.Secret{}, secrets.ErrNotFound
	case version != secrets.AnyVersion && version != secret.Version:
		return secrets.Secret{}, secrets.ErrChanged
	}
	return secret, nil
}

// Replace renames the secret, and replaces its value unless sealed is nil.
func (s *Store) Replace(_ context.Context, actor uuid.UUID, id uuid.UUID, version int64, name string, sealed []byte) (secrets.Secret, error) {
	if s.Fail != nil {
		return secrets.Secret{}, s.Fail
	}
	secret, err := s.current(id, version)
	if err != nil {
		return secrets.Secret{}, err
	}
	if secret.Name = name; s.taken(secret) {
		return secrets.Secret{}, secrets.ErrNameTaken
	}
	secret.Version, secret.UpdatedBy, secret.UpdatedAt = secret.Version+1, actor, s.Now
	s.Secrets[id] = secret
	if sealed != nil {
		s.Values[id] = sealed
	}
	s.Events = append(s.Events, "secret.replaced")
	return secret, nil
}

// Delete deletes the secret and the grants on it.
func (s *Store) Delete(_ context.Context, _ uuid.UUID, id uuid.UUID, version int64) error {
	if s.Fail != nil {
		return s.Fail
	}
	if _, err := s.current(id, version); err != nil {
		return err
	}
	delete(s.Secrets, id)
	delete(s.Values, id)
	delete(s.Orgs.Secrets, id)
	s.Orgs.Grants = slices.DeleteFunc(s.Orgs.Grants, func(g orgstest.Grant) bool { return g.Scope == auth.ScopeSecret && g.ScopeID == id })
	s.Events = append(s.Events, "secret.deleted")
	return nil
}

// Sealed reads the sealed values of those of ids that are there.
func (s *Store) Sealed(_ context.Context, ids []uuid.UUID) (map[uuid.UUID][]byte, error) {
	out := map[uuid.UUID][]byte{}
	for _, id := range ids {
		if v, ok := s.Values[id]; ok {
			out[id] = v
		}
	}
	return out, nil
}

// Access lists the principals holding a grant on the secret, by display name.
func (s *Store) Access(ctx context.Context, id uuid.UUID) ([]secrets.Principal, error) {
	var out []secrets.Principal
	for _, g := range s.Orgs.Grants {
		if g.Scope != auth.ScopeSecret || g.ScopeID != id || slices.ContainsFunc(out, func(p secrets.Principal) bool { return p.ID == g.Principal }) {
			continue
		}
		p, err := s.Principal(ctx, s.Secrets[id].OrgID, g.Principal)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	slices.SortFunc(out, func(a, b secrets.Principal) int { return strings.Compare(a.DisplayName, b.DisplayName) })
	return out, nil
}

// Jobs lists the repository's jobs among Others, by name.
func (s *Store) Jobs(_ context.Context, orgID, repoID uuid.UUID) ([]secrets.Principal, error) {
	var out []secrets.Principal
	for _, o := range s.Others {
		if o.OrgID == orgID && o.RepoID == repoID && o.Principal.Kind == auth.KindJob {
			out = append(out, o.Principal)
		}
	}
	slices.SortFunc(out, func(a, b secrets.Principal) int { return strings.Compare(a.DisplayName, b.DisplayName) })
	return out, nil
}

// Principal finds one of the organization's members, API keys, jobs or agents.
func (s *Store) Principal(_ context.Context, orgID, principalID uuid.UUID) (secrets.Principal, error) {
	if slices.Contains(s.Orgs.Memberships[orgID], principalID) {
		return secrets.Principal{ID: principalID, Kind: auth.KindUser, DisplayName: s.Orgs.Names[principalID]}, nil
	}
	if k, ok := s.Orgs.Keys[principalID]; ok && k.OrgID == orgID {
		return secrets.Principal{ID: principalID, Kind: auth.KindAPIKey, DisplayName: k.Name}, nil
	}
	if o, ok := s.Others[principalID]; ok && o.OrgID == orgID {
		return o.Principal, nil
	}
	return secrets.Principal{}, secrets.ErrNotFound
}

// Grant gives the principal Secret reader on the secret.
func (s *Store) Grant(_ context.Context, _ uuid.UUID, secret secrets.Secret, principalID uuid.UUID) error {
	if s.Fail != nil {
		return s.Fail
	}
	g := orgstest.Grant{Principal: principalID, Role: auth.RoleSecretReader, Scope: auth.ScopeSecret, ScopeID: secret.ID}
	if slices.Contains(s.Orgs.Grants, g) {
		return secrets.ErrGranted
	}
	s.Orgs.Grants = append(s.Orgs.Grants, g)
	s.Events = append(s.Events, "secret.granted")
	return nil
}

// Revoke takes Secret reader on the secret from the principal.
func (s *Store) Revoke(_ context.Context, _ uuid.UUID, secret secrets.Secret, principalID uuid.UUID) error {
	if s.Fail != nil {
		return s.Fail
	}
	g := orgstest.Grant{Principal: principalID, Role: auth.RoleSecretReader, Scope: auth.ScopeSecret, ScopeID: secret.ID}
	i := slices.Index(s.Orgs.Grants, g)
	if i < 0 {
		return secrets.ErrNotFound
	}
	s.Orgs.Grants = slices.Delete(s.Orgs.Grants, i, i+1)
	s.Events = append(s.Events, "secret.revoked")
	return nil
}
