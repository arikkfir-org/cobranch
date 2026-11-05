package code

import (
	"bytes"
	"cmp"
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel/attribute"

	"cobranch.dev/internal/services/jobs"
	"cobranch.dev/internal/services/repos"
)

// CheckoutContainer is the init container that checks a run's commit out, whose log comes first.
const CheckoutContainer = "cobranch-checkout"

// RunLog is one of a run's containers' logs, with the values its run masks hidden: as the controller kept it once the
// run ended, or so far while the run runs.
type RunLog struct {
	Container string
	Text      []byte
	// Times are when each line of Text was logged, one per line; none for a log kept before logs kept their times.
	Times []time.Time
	// Live reports it was read from the running pod: it grows until the run ends.
	Live bool
}

// ArchivedLogs reads the logs the controller kept of ended runs: the object store.
type ArchivedLogs interface {
	// Read reads a run's containers' logs, by container; none when it kept none.
	Read(ctx context.Context, repoID, runID uuid.UUID) (map[string][]byte, error)
}

// LiveLogs reads running runs' logs and steps from their pods: Kubernetes.
type LiveLogs interface {
	// Logs reads each of the run's pod's containers' logs so far, at most limit bytes each, each line with its time
	// before it (jobs.Stamp) and the run's masked values hidden, by container; none once its pod is gone, or for a
	// container that never started.
	Logs(ctx context.Context, orgID, runID uuid.UUID, limit int64) (map[string][]byte, error)
	// Steps reads the run's pod's containers as its steps, in the order they run; none once its pod is gone.
	Steps(ctx context.Context, orgID, runID uuid.UUID) ([]jobs.Step, error)
}

// runLogs are where the Service reads runs' logs, and the most of each container's it reads from a pod.
type runLogs struct {
	archived ArchivedLogs
	live     LiveLogs
	limit    int64
}

// WithRunLogs has the Service read ended runs' logs from archived and running runs' from live, at most limit bytes of
// each container's, and answers it. Without either, those runs show no logs: an installation without an object store
// keeps none, and one that runs no pods has no running pods to read.
func (s *Service) WithRunLogs(archived ArchivedLogs, live LiveLogs, limit int64) *Service {
	s.logs = runLogs{archived: archived, live: live, limit: limit}
	return s
}

// RunLogs reads one of the repository's runs' logs, the checkout's first and then by container: as the controller kept
// them once it ended, from its pod while it runs, with the values the run masks hidden either way; it takes
// repo.read. A run that isn't there is repos.ErrNotFound; one that never started a pod, such as a webhook's, has none.
func (s *Service) RunLogs(ctx context.Context, v Viewer, repoID uuid.UUID, number int64) ([]RunLog, error) {
	ctx, span := tracer.Start(ctx, "code.Service.RunLogs")
	defer span.End()
	span.SetAttributes(attribute.String("cobranch.repository.id", repoID.String()), attribute.Int64("cobranch.run.number", number))
	r, err := s.Run(ctx, v, repoID, number)
	if err != nil {
		return nil, err
	}
	logs, live, err := s.readLogs(ctx, repoID, r)
	if err != nil {
		return nil, failed(span, "reading the run's logs", err)
	}
	out := make([]RunLog, 0, len(logs))
	for container, log := range logs {
		text, times := unstamp(log)
		out = append(out, RunLog{Container: container, Text: text, Times: times, Live: live})
	}
	slices.SortFunc(out, func(a, b RunLog) int {
		return cmp.Or(cmp.Compare(logRank(a.Container), logRank(b.Container)), cmp.Compare(a.Container, b.Container))
	})
	return out, nil
}

// RunLog reads one of a run's containers' logs, as RunLogs does; it takes repo.read. A run, or a container of its, that
// isn't there, or hasn't logged yet, is repos.ErrNotFound.
func (s *Service) RunLog(ctx context.Context, v Viewer, repoID uuid.UUID, number int64, container string) (RunLog, error) {
	logs, err := s.RunLogs(ctx, v, repoID, number)
	if err != nil {
		return RunLog{}, err
	}
	for _, l := range logs {
		if l.Container == container {
			return l, nil
		}
	}
	return RunLog{}, fmt.Errorf("container %q of run #%d: %w", container, number, repos.ErrNotFound)
}

// readLogs reads r's logs where they are: the archive's once it ended, its pod's while it runs, none before. live
// reports they came from its pod.
func (s *Service) readLogs(ctx context.Context, repoID uuid.UUID, r Run) (_ map[string][]byte, live bool, _ error) {
	switch {
	case r.Definition.Kind() == jobs.KindWebhook || r.StartedAt == nil:
		return nil, false, nil
	case r.State == jobs.StateRunning:
		if s.logs.live == nil {
			return nil, true, nil
		}
		logs, err := s.logs.live.Logs(ctx, r.OrgID, r.ID, s.logs.limit)
		if err != nil {
			return nil, false, fmt.Errorf("reading run %s's pod's logs: %w", r.ID, err)
		}
		return logs, true, nil
	case r.State.Active() || s.logs.archived == nil:
		return nil, false, nil
	}
	logs, err := s.logs.archived.Read(ctx, repoID, r.ID)
	if err != nil {
		return nil, false, fmt.Errorf("reading run %s's archived logs: %w", r.ID, err)
	}
	return logs, false, nil
}

// logRank orders a run's logs: the checkout's, which runs first, before its other containers'.
func logRank(container string) int {
	if container == CheckoutContainer {
		return 0
	}
	return 1
}

// unstamp splits a log the controller kept into its text and when each of its lines was logged (jobs.Stamp). A log kept
// before logs kept their times answers as it is, with none; a line without its time, which the log's limit cut short,
// takes the time of the line before.
func unstamp(log []byte) ([]byte, []time.Time) {
	lines := bytes.SplitAfter(log, []byte("\n"))
	if len(lines[len(lines)-1]) == 0 {
		lines = lines[:len(lines)-1]
	}
	if len(lines) == 0 {
		return log, nil
	}
	if _, _, ok := jobs.Stamp(lines[0]); !ok {
		return log, nil
	}
	text, times := make([]byte, 0, len(log)), make([]time.Time, 0, len(lines))
	var last time.Time
	for _, line := range lines {
		if at, rest, ok := jobs.Stamp(line); ok {
			last, line = at.UTC(), rest
		}
		text = append(text, line...)
		times = append(times, last)
	}
	return text, times
}

// RunSteps lists one of the repository's runs' steps, its pod's containers in the order they run: as the controller
// kept them once it ended, from its pod while it runs; it takes repo.read. A run that isn't there is
// repos.ErrNotFound; one that never started a pod, such as a webhook's, has none.
func (s *Service) RunSteps(ctx context.Context, v Viewer, repoID uuid.UUID, number int64) ([]jobs.Step, error) {
	ctx, span := tracer.Start(ctx, "code.Service.RunSteps")
	defer span.End()
	span.SetAttributes(attribute.String("cobranch.repository.id", repoID.String()), attribute.Int64("cobranch.run.number", number))
	r, err := s.Run(ctx, v, repoID, number)
	if err != nil {
		return nil, err
	}
	var steps []jobs.Step
	switch {
	case r.Definition.Kind() == jobs.KindWebhook || r.StartedAt == nil:
	case r.State == jobs.StateRunning && s.logs.live != nil:
		steps, err = s.logs.live.Steps(ctx, r.OrgID, r.ID)
	case !r.State.Active():
		steps, err = s.store.RunSteps(ctx, r.ID)
	}
	if err != nil {
		return nil, failed(span, "reading the run's steps", err)
	}
	return steps, nil
}
