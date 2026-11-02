// Package limits is the domain of quotas: how many organizations a user may create, how many repositories an
// organization may hold and how many bytes of Git data it may store, which limit applies to whom, and the refusal of
// whatever would cross one. The services that create organizations and repositories and record pushes check them in
// the transactions that would cross them
// (https://github.com/arikkfir-org/cobranch/blob/main/docs/cobranch/designs/quotas.md).
package limits

import (
	"fmt"
	"math"
	"strconv"
)

// Limits are the limits that apply: each a number of 0 or more, 0 capping nothing.
type Limits struct {
	// OrganizationsPerUser caps the organizations a user creates for themselves.
	OrganizationsPerUser int64
	// RepositoriesPerOrganization caps an organization's repositories.
	RepositoriesPerOrganization int64
	// GitDataPerOrganization caps the bytes of an organization's repositories' active packs.
	GitDataPerOrganization int64
}

// Defaults are the built-in limits, which apply until the chart's values or an administrator set others: an
// installation open to anyone's sign-up by default is capped by default too.
var Defaults = Limits{OrganizationsPerUser: 5, RepositoriesPerOrganization: 100, GitDataPerOrganization: 1 << 30}

// Settings are the installation's limits as one source gives them, the chart's values or the database: nil where it
// sets none.
type Settings struct {
	OrganizationsPerUser        *int64
	RepositoriesPerOrganization *int64
	GitDataPerOrganization      *int64
}

// Overrides are an organization's own limits, which an administrator sets: nil where it takes the installation's.
type Overrides struct {
	Repositories *int64
	GitData      *int64
}

// Resolve returns the limits that apply to an installation whose chart's values set values and whose database sets
// db: each the values', else the database's, else the default.
func Resolve(values, db Settings) Limits {
	return Limits{
		OrganizationsPerUser:        first(Defaults.OrganizationsPerUser, values.OrganizationsPerUser, db.OrganizationsPerUser),
		RepositoriesPerOrganization: first(Defaults.RepositoriesPerOrganization, values.RepositoriesPerOrganization, db.RepositoriesPerOrganization),
		GitDataPerOrganization:      first(Defaults.GitDataPerOrganization, values.GitDataPerOrganization, db.GitDataPerOrganization),
	}
}

// For returns the limits that apply to an organization with its own limits o, under the installation's l: its own
// where it has them, which win over the values' too.
func (l Limits) For(o Overrides) Limits {
	l.RepositoriesPerOrganization = first(l.RepositoriesPerOrganization, o.Repositories)
	l.GitDataPerOrganization = first(l.GitDataPerOrganization, o.GitData)
	return l
}

// first is the first of sources that is set, or fallback.
func first(fallback int64, sources ...*int64) int64 {
	for _, s := range sources {
		if s != nil {
			return *s
		}
	}
	return fallback
}

// Exceeds reports whether adding to used crosses limit; 0 caps nothing.
func Exceeds(limit, used, adding int64) bool {
	return limit > 0 && used+adding > limit
}

// Kind names a limit, as the APIs' problem codes do (limit.<kind>).
type Kind string

// The limits.
const (
	KindOrganizations Kind = "organizations"
	KindRepositories  Kind = "repositories"
	KindGitData       Kind = "git_data"
)

// ExceededError is what a limit refuses: Used is what there is, Limit what may be, and Adding what the refused change
// brings, the bytes of a push's pack for KindGitData. Its message names the limit, as clients show it.
type ExceededError struct {
	Kind   Kind
	Limit  int64
	Used   int64
	Adding int64
}

func (e *ExceededError) Error() string {
	switch e.Kind {
	case KindOrganizations:
		return fmt.Sprintf("You created %s, the most a user may create here. An administrator can create more for you.",
			count(e.Used, "organization", "organizations"))
	case KindRepositories:
		return fmt.Sprintf("This organization holds %s, the most it may hold. An administrator can raise its limit.",
			count(e.Used, "repository", "repositories"))
	default:
		return fmt.Sprintf("This organization stores %s of Git data, and this push adds %s: over its limit of %s. Nothing changed; an administrator can raise the limit.",
			Bytes(e.Used), Bytes(e.Adding), Bytes(e.Limit))
	}
}

// count writes n things, one or many of them.
func count(n int64, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return strconv.FormatInt(n, 10) + " " + many
}

// Bytes writes n bytes in binary units, as the chart's values write sizes, rounded down so it never overstates: 1 GiB,
// 980 MiB, 2.5 KiB, 300 B.
func Bytes(n int64) string {
	units := []string{"B", "KiB", "MiB", "GiB", "TiB", "PiB", "EiB"}
	value, unit := float64(n), 0
	for value >= 1024 && unit < len(units)-1 {
		value /= 1024
		unit++
	}
	if tenths := math.Floor(value*10) / 10; unit > 0 && value < 10 && tenths != math.Floor(tenths) {
		return strconv.FormatFloat(tenths, 'f', 1, 64) + " " + units[unit]
	}
	return strconv.FormatFloat(math.Floor(value), 'f', 0, 64) + " " + units[unit]
}
