package blob

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/google/uuid"
	"gocloud.dev/blob"

	"cobranch.dev/internal/services/code"
	"cobranch.dev/internal/services/runner"
)

// Logs keeps ended runs' logs in the object store, one object per container under logs/<repository>/<run>/:
// runner.Archive, and code.ArchivedLogs for the REST and GraphQL servers
// (https://github.com/arikkfir-org/cobranch/blob/main/docs/cobranch/designs/jobs.md, "Pods and scripts").
type Logs struct {
	Bucket *blob.Bucket
}

var (
	_ runner.Archive    = Logs{}
	_ code.ArchivedLogs = Logs{}
)

// logsPrefix starts every log's key.
const logsPrefix = "logs/"

// LogKey is the key of a run's container's log.
func LogKey(repoID, runID uuid.UUID, container string) string {
	return runPrefix(repoID, runID) + container
}

// runPrefix starts the keys of a run's logs.
func runPrefix(repoID, runID uuid.UUID) string {
	return logsPrefix + repoID.String() + "/" + runID.String() + "/"
}

// Put writes a run's container's log, replacing one written before (runner.Archive).
func (l Logs) Put(ctx context.Context, repoID, runID uuid.UUID, container string, log []byte) error {
	key := LogKey(repoID, runID, container)
	if err := l.Bucket.WriteAll(ctx, key, log, &blob.WriterOptions{ContentType: "text/plain; charset=utf-8"}); err != nil {
		return fmt.Errorf("writing %s: %w", key, err)
	}
	return nil
}

// Read reads a run's containers' logs, by container; none when it kept none (code.ArchivedLogs).
func (l Logs) Read(ctx context.Context, repoID, runID uuid.UUID) (map[string][]byte, error) {
	prefix := runPrefix(repoID, runID)
	out := map[string][]byte{}
	it := l.Bucket.List(&blob.ListOptions{Prefix: prefix})
	for {
		obj, err := it.Next(ctx)
		if errors.Is(err, io.EOF) {
			return out, nil
		}
		if err != nil {
			return nil, fmt.Errorf("listing %s: %w", prefix, err)
		}
		log, err := l.Bucket.ReadAll(ctx, obj.Key)
		if err != nil {
			return nil, fmt.Errorf("reading %s: %w", obj.Key, err)
		}
		out[strings.TrimPrefix(obj.Key, prefix)] = log
	}
}

// Expire deletes the logs written before before, past the failures to delete some, and counts those it deleted:
// maintenance keeps them for the chart's jobs.logs.retention.
func (l Logs) Expire(ctx context.Context, before time.Time) (int, error) {
	deleted := 0
	var errs []error
	it := l.Bucket.List(&blob.ListOptions{Prefix: logsPrefix})
	for {
		obj, err := it.Next(ctx)
		if errors.Is(err, io.EOF) {
			return deleted, errors.Join(errs...)
		}
		if err != nil {
			return deleted, errors.Join(append(errs, fmt.Errorf("listing %s: %w", logsPrefix, err))...)
		}
		if obj.IsDir || !obj.ModTime.Before(before) {
			continue
		}
		if err := l.Bucket.Delete(ctx, obj.Key); err != nil {
			errs = append(errs, fmt.Errorf("deleting %s: %w", obj.Key, err))
			continue
		}
		deleted++
	}
}
