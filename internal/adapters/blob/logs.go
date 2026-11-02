package blob

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"gocloud.dev/blob"

	"cobranch.dev/internal/services/runner"
)

// Logs keeps ended runs' logs in the object store, one object per container under logs/<repository>/<run>/:
// runner.Archive (https://github.com/arikkfir-org/cobranch/blob/main/docs/cobranch/designs/jobs.md, "Pods and
// scripts").
type Logs struct {
	Bucket *blob.Bucket
}

var _ runner.Archive = Logs{}

// LogKey is the key of a run's container's log.
func LogKey(repoID, runID uuid.UUID, container string) string {
	return "logs/" + repoID.String() + "/" + runID.String() + "/" + container
}

// Put writes a run's container's log, replacing one written before (runner.Archive).
func (l Logs) Put(ctx context.Context, repoID, runID uuid.UUID, container string, log []byte) error {
	key := LogKey(repoID, runID, container)
	if err := l.Bucket.WriteAll(ctx, key, log, &blob.WriterOptions{ContentType: "text/plain; charset=utf-8"}); err != nil {
		return fmt.Errorf("writing %s: %w", key, err)
	}
	return nil
}
