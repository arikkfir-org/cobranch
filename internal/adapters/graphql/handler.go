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
// organizations and repositories for theirs, and code for the repositories' branches and commits.
// Subscriptions over WebSocket come with live updates. A mutation runs over POST alone: GET's transport refuses it, so a
// cross-site navigation changes nothing.
func NewHandler(version string, sessions Sessions, organizations Organizations, repositories Repositories, code Code) http.Handler {
	srv := handler.New(NewExecutableSchema(Config{Resolvers: &Resolver{Version: version, Sessions: sessions, Orgs: organizations, Repos: repositories,
		Code: code}}))
	srv.AddTransport(transport.GET{})
	srv.AddTransport(transport.POST{})
	srv.Use(extension.Introspection{})
	return srv
}
