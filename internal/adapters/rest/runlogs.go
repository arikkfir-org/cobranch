package rest

import (
	"bytes"
	"context"
	"strings"
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
		out.Logs = append(out.Logs, RunLog{Container: l.Container, Text: strings.ToValidUTF8(string(l.Text), "�"), Live: l.Live})
	}
	return out, nil
}

// GetRunLog answers one of a run's containers' logs as text, as it was logged.
func (a api) GetRunLog(ctx context.Context, req GetRunLogRequestObject) (GetRunLogResponseObject, error) {
	v, repo, err := a.viewerAndRepo(ctx, req.Org, req.Repo)
	if err != nil {
		return nil, err
	}
	l, err := a.code.RunLog(ctx, v, repo.ID, req.Number, req.Container)
	if err != nil {
		return nil, fail(err, "log")
	}
	return GetRunLog200TextplainCharsetUtf8Response{Body: bytes.NewReader(l.Text), ContentLength: int64(len(l.Text)),
		Headers: GetRunLog200ResponseHeaders{XContentTypeOptions: "nosniff"}}, nil
}
