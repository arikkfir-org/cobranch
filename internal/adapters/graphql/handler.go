// Package graphql serves DevHub's GraphQL API at /graphql, the Console's surface, through the executor gqlgen
// generates from api/graphql/ (generated.go).
package graphql

import (
	"net/http"

	"github.com/99designs/gqlgen/graphql/handler"
	"github.com/99designs/gqlgen/graphql/handler/extension"
	"github.com/99designs/gqlgen/graphql/handler/transport"
)

// NewHandler serves queries over GET and POST, for a server built as version. Subscriptions over WebSocket come with
// live updates.
func NewHandler(version string) http.Handler {
	srv := handler.New(NewExecutableSchema(Config{Resolvers: &Resolver{Version: version}}))
	srv.AddTransport(transport.GET{})
	srv.AddTransport(transport.POST{})
	srv.Use(extension.Introspection{})
	return srv
}
