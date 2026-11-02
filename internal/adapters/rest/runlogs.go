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
