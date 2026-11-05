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
	out.ClusterName, out.URL = nonEmpty(e.ClusterName), nonEmpty(e.URL)
	out.Renders = mapAll(e.Renders, func(r code.Render) *Render {
		return &Render{Kind: RenderKind(strings.ToUpper(r.Kind)), Path: r.Path}
	})
	if e.Latest != nil {
		out.Latest = delivery(*e.Latest)
	}
	return out
}

// environmentObject is o as the API shows it.
func environmentObject(o code.EnvironmentObject) *EnvironmentObject {
	out := &EnvironmentObject{Group: o.Group, Version: o.Version, Kind: o.Kind, Namespace: o.Namespace, Name: o.Name,
		Health: EnvironmentHealth(strings.ToUpper(string(o.Health))), Message: o.Message, Ready: nonEmpty(o.Ready),
		CreatedAt: utc(o.CreatedAt)}
	if o.Delivery != 0 {
		number := int(o.Delivery)
		out.Delivery = &number
	}
	return out
}

// deliveryTarget is t as the API shows it.
func deliveryTarget(t code.DeliveryTarget) *DeliveryTarget {
	out := &DeliveryTarget{Name: t.Name, Branches: t.Branches, ClusterName: nonEmpty(t.ClusterName), Namespace: t.Namespace,
		URL: nonEmpty(t.URL)}
	if t.Cluster != nil {
		cluster := t.Cluster.String()
		out.Cluster = &cluster
	}
	return out
}

// deliveryKinds are kinds as the domain names them.
func deliveryKinds(kinds []DeliveryKind) []code.DeliveryKind {
	out := make([]code.DeliveryKind, 0, len(kinds))
	for _, k := range kinds {
		out = append(out, code.DeliveryKind(strings.ToLower(string(k))))
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
		Branch: d.Branch, Kind: DeliveryKind(strings.ToUpper(string(d.Kind))), Sha: nonEmpty(d.SHA), Subject: d.Subject,
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
