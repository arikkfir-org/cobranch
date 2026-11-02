package taskrunner

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/google/uuid"

	"cobranch.dev/internal/services/auth"
	"cobranch.dev/internal/services/runner"
	"cobranch.dev/internal/services/secrets"
)

// Dispatch claims the queued task that waited longest and starts its first pod, and reports whether one waited. A task
// that can't start fails, naming why; one the quota refuses waits for room; one that fails on the way is taken again
// once its claim lapses.
func (s *Service) Dispatch(ctx context.Context) (_ bool, err error) {
	ctx, span := tracer.Start(ctx, "taskrunner.Service.Dispatch")
	defer span.End()
	defer func() { record(span, err) }()
	t, ok, err := s.store.Claim(ctx, s.now().Add(s.settings.Lease))
	if err != nil {
		return false, fmt.Errorf("claiming a queued task: %w", err)
	}
	if !ok {
		return false, nil
	}
	span.SetAttributes(taskAttrs(t.ID, t.OrgID, t.Number)...)
	return true, s.start(ctx, t)
}

// start starts a claimed task's first pod, then marks the task running; a task cancelled meanwhile loses its pod.
func (s *Service) start(ctx context.Context, t Task) error {
	started, err := s.startPod(ctx, t, 1)
	if err != nil || !started {
		return err
	}
	marked, err := s.store.MarkRunning(ctx, t.ID)
	switch {
	case err != nil:
		return fmt.Errorf("marking task %s running: %w", t.ID, err)
	case !marked:
		return wrapIf(s.cluster.RemoveTask(ctx, t.OrgID, t.ID), "removing ended task "+t.ID.String()+"'s pod")
	}
	return nil
}

// startPod starts the task's pod numbered pod, and reports whether it did: a task that can't start fails, naming why,
// and one the quota refuses waits for room, a queued one until RoomRetry has passed and a running one until the next
// pass.
func (s *Service) startPod(ctx context.Context, t Task, pod int) (bool, error) {
	launch, refusal, err := s.launch(ctx, t, pod)
	if err != nil {
		return false, err
	}
	if refusal != "" {
		return false, s.fail(ctx, t, refusal)
	}
	if err := s.cluster.Prepare(ctx, t.OrgID); err != nil {
		return false, fmt.Errorf("preparing task %s's namespace: %w", t.ID, err)
	}
	switch err := s.cluster.StartTask(ctx, launch); {
	case errors.Is(err, runner.ErrNoRoom) && pod == 1:
		return false, wrapIf(s.store.Defer(ctx, t.ID, s.now().Add(s.settings.RoomRetry)), "putting task "+t.ID.String()+" off")
	case errors.Is(err, runner.ErrNoRoom):
		return false, nil
	case errors.Is(err, runner.ErrRefused):
		return false, s.fail(ctx, t, "the cluster refused its pod"+strings.TrimPrefix(err.Error(), runner.ErrRefused.Error()))
	case err != nil:
		return false, fmt.Errorf("starting task %s's pod: %w", t.ID, err)
	}
	if pod > 1 {
		if err := s.store.Started(ctx, t.ID, pod); err != nil {
			return false, fmt.Errorf("recording task %s's pod %d: %w", t.ID, pod, err)
		}
	}
	return true, nil
}

// launch makes what the cluster starts the task's pod with, or answers why the task can't start: the installation runs
// no pods, its agent has no model, or its agent may not read its provider's key or its secrets.
func (s *Service) launch(ctx context.Context, t Task, pod int) (Launch, string, error) {
	switch {
	case !s.settings.Pods:
		return Launch{}, "this installation runs no pods", nil
	case t.Provider == "":
		return Launch{}, "its agent has no model: an admin picks one", nil
	case t.KeyID == uuid.Nil:
		return Launch{}, "the organization's " + t.Provider + " provider has no secret with its key", nil
	}
	values, refusal, err := s.receive(ctx, t)
	if err != nil || refusal != "" {
		return Launch{}, refusal, err
	}
	token, err := s.token(ctx, t.ID)
	if err != nil {
		return Launch{}, "", err
	}
	l := Launch{Task: t, Pod: pod, Token: token, Key: values[t.KeyID], Env: s.harnessEnv(t), Timeout: s.settings.Timeout}
	for name, id := range t.Secrets {
		l.Env[name] = string(values[id])
	}
	return l, "", nil
}

// receive reads the provider's key and the agent's secrets, or answers why the agent may not have them.
func (s *Service) receive(ctx context.Context, t Task) (map[uuid.UUID][]byte, string, error) {
	ids := []uuid.UUID{t.KeyID}
	for _, id := range t.Secrets {
		ids = append(ids, id)
	}
	values, err := s.secrets.Receive(ctx, t.AgentID, ids)
	var refused *secrets.RefusedError
	switch {
	case errors.As(err, &refused) && refused.Missing:
		return nil, fmt.Sprintf("secret %s isn't there", refused.ID), nil
	case errors.As(err, &refused):
		return nil, fmt.Sprintf("its agent may not read secret %s: grant it Secret reader on it", refused.ID), nil
	case errors.Is(err, secrets.ErrNoSecretsKey):
		return nil, "this installation has no secrets key to open its secrets with", nil
	case err != nil:
		return nil, "", fmt.Errorf("receiving task %s's secrets: %w", t.ID, err)
	}
	return values, "", nil
}

// token issues the task a token for TokenLife.
func (s *Service) token(ctx context.Context, taskID uuid.UUID) (string, error) {
	token := auth.NewSecret(auth.PrefixTaskToken)
	if err := s.store.Token(ctx, taskID, auth.Hash(token), s.now().Add(TokenLife)); err != nil {
		return "", fmt.Errorf("recording task %s's token: %w", taskID, err)
	}
	return token, nil
}

// harnessEnv is what the harness gets from Cobranch: where Cobranch is, the agent's model, and the installation's
// bounds. The cluster adds where the pod keeps the token and where the model proxy listens.
func (s *Service) harnessEnv(t Task) map[string]string {
	env := map[string]string{
		"COBRANCH_REST_URL": s.settings.RESTURL, "COBRANCH_MODEL_PROVIDER": t.Provider, "COBRANCH_MODEL": t.Model,
		"COBRANCH_MAX_TURNS": strconv.Itoa(s.settings.MaxTurns), "COBRANCH_SHELL_TIMEOUT": s.settings.ShellTimeout.String(),
		"COBRANCH_MODEL_MAX_TOKENS": strconv.Itoa(s.settings.MaxTokens),
	}
	if s.settings.GitURL != "" {
		env["COBRANCH_GIT_URL"] = s.settings.GitURL
	}
	return env
}

// fail ends the task Failed, naming why.
func (s *Service) fail(ctx context.Context, t Task, reason string) error {
	if _, err := s.store.Fail(ctx, t.ID, reason); err != nil {
		return fmt.Errorf("failing task %s: %w", t.ID, err)
	}
	return nil
}

// wrapIf wraps err with what was being done, or answers nil without one.
func wrapIf(err error, what string) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s: %w", what, err)
}
