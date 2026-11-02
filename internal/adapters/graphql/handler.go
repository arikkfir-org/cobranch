// Package graphql serves Cobranch's GraphQL API, the Console's surface, through the executor gqlgen generates from
// api/graphql/ (generated.go).
package graphql

import (
	"context"
	"net/http"
	"time"

	"github.com/99designs/gqlgen/graphql/handler"
	"github.com/99designs/gqlgen/graphql/handler/extension"
	"github.com/99designs/gqlgen/graphql/handler/transport"
	"github.com/coder/websocket"
)

// maxSubscription is how long a WebSocket stays open: its client connects again, and its credential is checked again
// then, so a revoked session follows nothing for longer.
const maxSubscription = time.Hour

// NewHandler serves queries over GET and POST, for a server built as version, with sessions for the viewer's,
// organizations and repositories for theirs, code for the repositories' branches and commits, and chats for the
// branches' chats. Subscriptions run over WebSocket (graphql-transport-ws), for the API's own pages, from origin pages,
// and for clients that send no Origin; each closes after an hour. A mutation runs over POST and WebSocket alone: GET's
// transport refuses it, so a cross-site navigation changes nothing, and a page from another origin opens no WebSocket.
// It times each root field of a query or a mutation.
func NewHandler(version, pages string, sessions Sessions, organizations Organizations, repositories Repositories, code Code, chats Chats, opts ...Option) http.Handler {
	var o options
	for _, opt := range opts {
		opt(&o)
	}
	srv := handler.New(NewExecutableSchema(Config{Resolvers: &Resolver{Version: version, Sessions: sessions, Orgs: organizations, Repos: repositories,
		Code: code, Chats: chats}}))
	srv.AddTransport(transport.GET{})
	srv.AddTransport(transport.POST{})
	var origins []string
	if pages != "" {
		origins = []string{pages}
	}
	srv.AddTransport(transport.Websocket{
		Implementation:        transport.CoderWebsocketImplementation{AcceptOptions: websocket.AcceptOptions{OriginPatterns: origins}},
		KeepAlivePingInterval: 30 * time.Second,
		PingPongInterval:      30 * time.Second,
		MissingPongOk:         true,
	})
	srv.Use(extension.Introspection{})
	srv.Use(newFieldTimer(o.meter))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Upgrade") != "" {
			ctx, cancel := context.WithTimeout(r.Context(), maxSubscription)
			defer cancel()
			r = r.WithContext(ctx)
		}
		srv.ServeHTTP(w, r)
	})
}
