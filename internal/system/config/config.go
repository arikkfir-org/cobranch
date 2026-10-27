// Package config reads the workloads' configuration from environment variables, the only configuration DevHub reads.
// The Helm chart renders them.
package config

import (
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/kelseyhightower/envconfig"
)

// Prefix starts every variable's name: DEVHUB_HTTP_ADDR, DEVHUB_BLOB_URL and so on.
const Prefix = "DEVHUB"

// Server configures the servers, each of which serves one API: rest-server and graphql-server.
type Server struct {
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
	// ConsoleOrigin is the origin of the Console's public URL, whose pages may call the server from their own (CORS).
	// When empty, no other origin may.
	ConsoleOrigin Origin `envconfig:"CONSOLE_URL"`
}

// Origin is a web origin, a scheme and a host, read from a URL.
type Origin string

// Decode reads the origin of value, an absolute http or https URL.
func (o *Origin) Decode(value string) error {
	u, err := url.Parse(value)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return fmt.Errorf("%q is not an absolute http or https URL", value)
	}
	*o = Origin(u.Scheme + "://" + strings.ToLower(u.Host))
	return nil
}

// LoadServer reads a server's configuration from the environment.
func LoadServer() (Server, error) {
	var c Server
	if err := envconfig.Process(Prefix, &c); err != nil {
		return Server{}, fmt.Errorf("reading the server's configuration from the DEVHUB_ variables: %w", err)
	}
	return c, nil
}

// Migrate configures the migrate Job.
type Migrate struct {
	// DatabaseURL is PostgreSQL's connection URL, as the servers'.
	DatabaseURL string `envconfig:"DATABASE_URL"`
}

// LoadMigrate reads the migrate Job's configuration from the environment.
func LoadMigrate() (Migrate, error) {
	var c Migrate
	if err := envconfig.Process(Prefix, &c); err != nil {
		return Migrate{}, fmt.Errorf("reading the migrate Job's configuration from the DEVHUB_ variables: %w", err)
	}
	return c, nil
}
