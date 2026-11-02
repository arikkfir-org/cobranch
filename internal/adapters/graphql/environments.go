package graphql

import (
	"strconv"
	"strings"

	"cobranch.dev/internal/services/code"
)

// environment is e, of the repository repositoryID names, as the API shows it.
func environment(repositoryID string, e code.Environment) *Environment {
	out := &Environment{ID: e.ID.String(), Target: commitPrincipal(&e.Target), Branch: e.Branch, Namespace: e.Namespace,
		Revision: nonEmpty(e.Revision), SyncedAt: utc(e.SyncedAt), Sync: EnvironmentSync(strings.ToUpper(string(e.Sync))),
		Health: EnvironmentHealth(strings.ToUpper(string(e.Health))), Paused: e.Paused, CreatedAt: e.CreatedAt.UTC(),
		Version: int(e.Version), RepositoryID: repositoryID}
	if e.Cluster != nil {
		cluster := e.Cluster.String()
		out.Cluster = &cluster
	}
	if e.Latest != nil {
		out.Latest = delivery(*e.Latest)
	}
	return out
}

// environments are list, of the repository repositoryID names, as the API shows them.
func environments(repositoryID string, list []code.Environment) []*Environment {
	return mapAll(list, func(e code.Environment) *Environment { return environment(repositoryID, e) })
}

// delivery is d as the API shows it.
func delivery(d code.Delivery) *Delivery {
	out := &Delivery{ID: d.ID.String(), Number: int(d.Number), EnvironmentID: d.EnvironmentID.String(), Target: d.Target,
		Branch: d.Branch, Kind: DeliveryKind(strings.ToUpper(string(d.Kind))), Sha: nonEmpty(d.SHA),
		Trigger: DeliveryTrigger(strings.ToUpper(string(d.Trigger))), State: DeliveryState(strings.ToUpper(string(d.State))),
		Reason: d.Reason, StartedBy: commitPrincipal(d.StartedBy), QueuedAt: d.QueuedAt.UTC(), StartedAt: utc(d.StartedAt),
		EndedAt: utc(d.EndedAt), Version: int(d.Version)}
	if d.PushID != nil {
		out.Push = idOf(*d.PushID)
	}
	return out
}

// deliveryPage is page as the API shows it.
func deliveryPage(page code.DeliveryPage) *DeliveryPage {
	out := &DeliveryPage{Deliveries: mapAll(page.Deliveries, delivery)}
	if page.Next != 0 {
		next := strconv.FormatInt(page.Next, 10)
		out.NextCursor = &next
	}
	return out
}

// ofRepository is b, a branch of repo, which its environments read.
func ofRepository(repo *Repository, b *Branch) *Branch {
	b.RepositoryID = repo.ID
	return b
}
