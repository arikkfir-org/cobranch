package runner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"cobranch.dev/internal/services/auth"
	"cobranch.dev/internal/services/jobs"
	"cobranch.dev/internal/services/secrets"
)

// tokenSlack is how long a run's token outlives its timeout: the pod's deadline counts from when its node takes it,
// and scheduling and pulling come first.
const tokenSlack = 15 * time.Minute

// ErrRefused is a pod the cluster won't create as it is: a spec Kubernetes or its admission refuses. Its run fails,
// naming why.
var ErrRefused = errors.New("the cluster refused the run's pod")

// Dispatch claims the queued run that waited longest and starts it, and reports whether one waited. A run that can't
// start fails, naming why; one the quota refuses waits for room; one that fails on the way is taken again once its
// claim lapses.
func (s *Service) Dispatch(ctx context.Context) (_ bool, err error) {
	ctx, span := tracer.Start(ctx, "runner.Service.Dispatch")
	defer span.End()
	defer func() { record(span, err) }()
	run, ok, err := s.store.Claim(ctx, s.now().Add(s.settings.Lease))
	if err != nil {
		return false, fmt.Errorf("claiming a queued run: %w", err)
	}
	if !ok {
		return false, nil
	}
	span.SetAttributes(runAttrs(run.ID, run.OrgID, run.RepoID, run.Number)...)
	span.SetAttributes(attribute.String("cobranch.job", run.Job))
	return true, s.start(ctx, run)
}

// start starts a claimed run: it makes the run's launch, prepares its organization's namespace, and creates its pod.
func (s *Service) start(ctx context.Context, run Run) error {
	launch, refusal, err := s.launch(ctx, run)
	if err != nil {
		return err
	}
	if refusal != "" {
		return s.fail(ctx, run.ID, refusal)
	}
	if err := s.cluster.Prepare(ctx, run.OrgID); err != nil {
		return fmt.Errorf("preparing run %s's namespace: %w", run.ID, err)
	}
	switch err := s.cluster.Start(ctx, launch); {
	case errors.Is(err, ErrNoRoom):
		return s.putOff(ctx, run.ID, "waits for room in the organization's quota")
	case errors.Is(err, ErrRefused):
		return s.fail(ctx, run.ID, err.Error())
	case err != nil:
		return fmt.Errorf("starting run %s's pod: %w", run.ID, err)
	}
	marked, err := s.store.MarkRunning(ctx, run.ID)
	switch {
	case err != nil:
		return fmt.Errorf("marking run %s running: %w", run.ID, err)
	case !marked:
		// A member cancelled the run, or a newer run superseded it, while its pod was being made.
		return wrapIf(s.cluster.Remove(ctx, run.OrgID, run.ID), "removing ended run "+run.ID.String()+"'s pod")
	}
	return nil
}

// launch makes what the cluster starts the run with, or answers why the run can't start: the installation runs no
// pods, its pod spec isn't one jobs may use, or it names a secret its job may not read.
func (s *Service) launch(ctx context.Context, run Run) (Launch, string, error) {
	job := run.Definition
	if !s.settings.Pods {
		return Launch{}, "this installation runs no pods", nil
	}
	if refusal := checkPod(job.Pod); refusal != "" {
		return Launch{}, refusal, nil
	}
	if job.Checkout && s.settings.GitURL == "" {
		return Launch{}, "this installation serves no Git over HTTPS to check its commit out from", nil
	}
	values, refusal, err := s.receive(ctx, run)
	if err != nil || refusal != "" {
		return Launch{}, refusal, err
	}
	timeout := s.timeout(job)
	token := auth.NewSecret(auth.PrefixRunToken)
	if err := s.store.Token(ctx, run.ID, auth.Hash(token), auth.Hint(token), s.now().Add(timeout+tokenSlack)); err != nil {
		return Launch{}, "", fmt.Errorf("recording run %s's token: %w", run.ID, err)
	}
	l := Launch{Run: run, Pod: job.Pod, Script: job.Script, Image: job.Image, Checkout: job.Checkout, Timeout: timeout,
		Env: map[string]string{}, Masked: []string{token}}
	if job.Kind() == jobs.KindScript && l.Image == "" {
		l.Image = s.settings.ScriptImage
	}
	for name, id := range job.Secrets {
		l.Env[name] = string(values[id])
		if len(values[id]) > 0 {
			l.Masked = append(l.Masked, string(values[id]))
		}
	}
	for name, value := range s.cobranchEnv(run, token) {
		l.Env[name] = value
	}
	return l, "", nil
}

// checkPod checks a pod's job's spec again, as the controller builds its pod from it: why it may not run, or "".
func checkPod(pod json.RawMessage) string {
	if pod == nil {
		return ""
	}
	var spec map[string]any
	if err := json.Unmarshal(pod, &spec); err != nil {
		return "its pod isn't a pod spec: " + err.Error()
	}
	if err := jobs.CheckPod(spec); err != nil {
		return "its pod: " + err.Error()
	}
	return ""
}

// receive reads the values of the secrets the run's job names, or answers why it may not have them.
func (s *Service) receive(ctx context.Context, run Run) (map[uuid.UUID][]byte, string, error) {
	ids := make([]uuid.UUID, 0, len(run.Definition.Secrets))
	for _, id := range run.Definition.Secrets {
		ids = append(ids, id)
	}
	values, err := s.secrets.Receive(ctx, run.JobID, ids)
	var refused *secrets.RefusedError
	switch {
	case errors.As(err, &refused) && refused.Missing:
		return nil, fmt.Sprintf("secret %s isn't there", refused.ID), nil
	case errors.As(err, &refused):
		return nil, fmt.Sprintf("job %s may not read secret %s: grant it Secret reader on it or on the repository", run.Job, refused.ID), nil
	case errors.Is(err, secrets.ErrNoSecretsKey):
		return nil, "this installation has no secrets key to open its secrets with", nil
	case err != nil:
		return nil, "", fmt.Errorf("receiving run %s's secrets: %w", run.ID, err)
	}
	return values, "", nil
}

// cobranchEnv is what every container of a run gets from Cobranch: where Cobranch is, the run's token, and what the
// run is.
func (s *Service) cobranchEnv(run Run, token string) map[string]string {
	env := map[string]string{
		"COBRANCH_URL": s.settings.RESTURL, "COBRANCH_TOKEN": token, "COBRANCH_ORG": run.OrgSlug, "COBRANCH_REPO": run.RepoSlug,
		"COBRANCH_SHA": run.SHA, "COBRANCH_REF": run.Ref, "COBRANCH_JOB": run.Job, "COBRANCH_RUN": strconv.FormatInt(run.Number, 10),
		"COBRANCH_EVENT": eventKind(run.Event),
	}
	if s.settings.GitURL != "" {
		env["COBRANCH_GIT_URL"] = strings.TrimSuffix(s.settings.GitURL, "/") + "/" + run.OrgSlug + "/" + run.RepoSlug + ".git"
	}
	return env
}

// eventKind is the kind of the event that triggered a run, as the run recorded it.
func eventKind(event json.RawMessage) string {
	var e struct {
		Kind string `json:"kind"`
	}
	_ = json.Unmarshal(event, &e) // a run records its event as JSON; none leaves the kind empty
	return e.Kind
}

// fail ends a claimed run Failed, naming why it couldn't start.
func (s *Service) fail(ctx context.Context, runID uuid.UUID, reason string) error {
	if _, err := s.store.Finish(ctx, runID, jobs.StateFailed, reason); err != nil {
		return fmt.Errorf("failing run %s: %w", runID, err)
	}
	return nil
}

// putOff puts a claimed run off for RoomRetry, saying why.
func (s *Service) putOff(ctx context.Context, runID uuid.UUID, reason string) error {
	if err := s.store.Defer(ctx, runID, s.now().Add(s.settings.RoomRetry), reason); err != nil {
		return fmt.Errorf("putting run %s off: %w", runID, err)
	}
	return nil
}

// runAttrs are the attributes of a span about a run.
func runAttrs(runID, orgID, repoID uuid.UUID, number int64) []attribute.KeyValue {
	return []attribute.KeyValue{attribute.String("cobranch.run.id", runID.String()), attribute.Int64("cobranch.run.number", number),
		attribute.String("cobranch.org.id", orgID.String()), attribute.String("cobranch.repo.id", repoID.String())}
}

// record marks span failed with err, if any.
func record(span trace.Span, err error) {
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
	}
}

// sortedKeys are m's keys in order, so what the controller writes from m doesn't change from one pass to the next.
func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}
