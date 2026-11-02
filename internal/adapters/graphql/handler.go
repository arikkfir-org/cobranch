// Package graphql serves Cobranch's GraphQL API, the Console's surface, through the executor gqlgen generates from
// api/graphql/ (generated.go).
package graphql

import (
	"net/http"

	"github.com/99designs/gqlgen/graphql/handler"
	"github.com/99designs/gqlgen/graphql/handler/extension"
	"github.com/99designs/gqlgen/graphql/handler/transport"
)

// NewHandler serves queries over GET and POST, for a server built as version, with sessions for the viewer's,
// organizations and repositories for theirs, code for the repositories' branches and commits, and issues for the
// organizations' issues.
// Subscriptions over WebSocket come with live updates. A mutation runs over POST alone: GET's transport refuses it, so a
// cross-site navigation changes nothing. It times each root field of a query or a mutation.
func NewHandler(version string, sessions Sessions, organizations Organizations, repositories Repositories, code Code, issues Issues, opts ...Option) http.Handler {
	var o options
	for _, opt := range opts {
		opt(&o)
	}
	srv := handler.New(NewExecutableSchema(Config{Resolvers: &Resolver{Version: version, Sessions: sessions, Orgs: organizations, Repos: repositories,
		Code: code, IssueService: issues}}))
	srv.AddTransport(transport.GET{})
	srv.AddTransport(transport.POST{})
	srv.Use(extension.Introspection{})
	srv.Use(newFieldTimer(o.meter))
	return srv
}
