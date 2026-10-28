package graphql

// Resolver resolves api/graphql/'s schema. gqlgen generates its methods' stubs into <schema>.resolvers.go and keeps
// their bodies when it regenerates.
type Resolver struct {
	// Version is the version the server was built as.
	Version string
}
