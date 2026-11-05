package rest

import (
	"bytes"
	"context"
	"strings"
	"time"

	"cobranch.dev/internal/services/jobs"
)

// GetRunLogs answers one of the repository's runs' logs, each as UTF-8 text.
func (a api) GetRunLogs(ctx context.Context, req GetRunLogsRequestObject) (GetRunLogsResponseObject, error) {
	v, repo, err := a.viewerAndRepo(ctx, req.Org, req.Repo)
	if err != nil {
		return nil, err
	}
	logs, err := a.code.RunLogs(ctx, v, repo.ID, req.Number)
	if err != nil {
		return nil, fail(err, "run")
	}
	out := GetRunLogs200JSONResponse{Logs: make([]RunLog, 0, len(logs))}
	for _, l := range logs {
		var times *[]time.Time
		if l.Times != nil {
			times = &l.Times
		}
		out.Logs = append(out.Logs, RunLog{Container: l.Container, Text: strings.ToValidUTF8(string(l.Text), "�"), Times: times, Live: l.Live})
	}
	return out, nil
}

// GetRunSteps answers one of the repository's runs' steps, in the order they run.
func (a api) GetRunSteps(ctx context.Context, req GetRunStepsRequestObject) (GetRunStepsResponseObject, error) {
	v, repo, err := a.viewerAndRepo(ctx, req.Org, req.Repo)
	if err != nil {
		return nil, err
	}
	steps, err := a.code.RunSteps(ctx, v, repo.ID, req.Number)
	if err != nil {
		return nil, fail(err, "run")
	}
	return GetRunSteps200JSONResponse{Steps: mapAll(steps, runStep)}, nil
}

// runStep is a run's step as the API shows it.
func runStep(s jobs.Step) RunStep {
	return RunStep{Name: s.Name, State: RunStepState(s.State), ExitCode: s.ExitCode, Reason: s.Reason, StartedAt: utc(s.StartedAt), EndedAt: utc(s.EndedAt)}
}

// GetRunLog answers one of a run's containers' logs as UTF-8 text, as its content type says: bytes that aren't UTF-8
// read as U+FFFD, as GetRunLogs has them.
func (a api) GetRunLog(ctx context.Context, req GetRunLogRequestObject) (GetRunLogResponseObject, error) {
	v, repo, err := a.viewerAndRepo(ctx, req.Org, req.Repo)
	if err != nil {
		return nil, err
	}
	l, err := a.code.RunLog(ctx, v, repo.ID, req.Number, req.Container)
	if err != nil {
		return nil, fail(err, "log")
	}
	text := []byte(strings.ToValidUTF8(string(l.Text), "�"))
	return GetRunLog200TextplainCharsetUtf8Response{Body: bytes.NewReader(text), ContentLength: int64(len(text)),
		Headers: GetRunLog200ResponseHeaders{XContentTypeOptions: "nosniff"}}, nil
}
