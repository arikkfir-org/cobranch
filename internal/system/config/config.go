// Package config reads the workloads' configuration from environment variables, the only configuration DevHub reads.
// The Helm chart renders them.
package config

import (
	"fmt"
	"time"

	"github.com/kelseyhightower/envconfig"
)

// Prefix starts every variable's name: DEVHUB_HTTP_ADDR, DEVHUB_BLOB_URL and so on.
const Prefix = "DEVHUB"

// APIServer configures the API server.
type APIServer struct {
	// HTTPAddr is the address the server listens on.
	HTTPAddr string `envconfig:"HTTP_ADDR" default:":8080"`
	// DatabaseURL is PostgreSQL's connection URL. When empty, pgx reads libpq's variables (PGHOST, PGPASSWORD and so
	// on), as the chart sets them for its own PostgreSQL.
	DatabaseURL string `envconfig:"DATABASE_URL"`
	// BlobURL is the bucket's Go Cloud URL: s3://, gs://, azblob:// or file://.
	BlobURL string `envconfig:"BLOB_URL" required:"true"`
	// ShutdownTimeout bounds how long the server waits for requests in flight once asked to stop. It must end before
	// the pod's grace period does.
	ShutdownTimeout time.Duration `envconfig:"SHUTDOWN_TIMEOUT" default:"20s"`
}

// LoadAPIServer reads the API server's configuration from the environment.
func LoadAPIServer() (APIServer, error) {
	var c APIServer
	if err := envconfig.Process(Prefix, &c); err != nil {
		return APIServer{}, fmt.Errorf("reading the API server's configuration from the DEVHUB_ variables: %w", err)
	}
	return c, nil
}
