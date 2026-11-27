package rest

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"cobranch.dev/internal/services/orgs"
)

// listWait is how long a read of a list waits for it to change, as wait asks: none unless tag names the list the
// caller holds, and wait.invalid past maxWait.
func listWait(wait *WaitForChange, tag *IfNoneMatch) (time.Duration, error) {
	if wait == nil {
		return 0, nil
	}
	if *wait < 0 || time.Duration(*wait)*time.Second > maxWait {
		return 0, refuse(http.StatusBadRequest, "wait.invalid", "A read waits 0 to 15 seconds for the list to change.")
	}
	if tag == nil {
		return 0, nil
	}
	return time.Duration(*wait) * time.Second, nil
}

// readList answers a list that a client may follow: read now when waits is zero, and otherwise followed until it
// differs from the one the caller holds, or until waits passes, when it answers the list unchanged. answer makes a
// list the response and says whether it is the one the caller holds (304). The Console follows its live lists this
// way, asking again with each answer's ETag.
func readList[T, R any](ctx context.Context, waits time.Duration, read func(context.Context) (T, error),
	follow func(context.Context) (<-chan T, error), answer func(T) (R, bool, error)) (R, error) {
	var none R
	if waits == 0 {
		list, err := read(ctx)
		if err != nil {
			return none, err
		}
		out, _, err := answer(list)
		return out, err
	}
	following, cancel := context.WithTimeout(ctx, waits)
	defer cancel()
	lists, err := follow(following)
	if err != nil {
		return none, err
	}
	last := none
	for list := range lists {
		out, same, err := answer(list)
		if err != nil || !same {
			return out, err
		}
		last = out
	}
	if following.Err() == nil {
		return none, errors.New("following the list stopped before the wait ended")
	}
	if err := ctx.Err(); err != nil {
		return none, fmt.Errorf("waiting for the list to change: %w", err)
	}
	return last, nil
}

// listAnswer is a list's body as a read answers it: ok with the body's ETag, or notModified while tag names it, which
// it says.
func listAnswer[R any](kind string, body any, tag *IfNoneMatch, ok, notModified func(etag string) R) (R, bool, error) {
	etag, err := bodyTag(kind, body, nil)
	if err != nil {
		var none R
		return none, false, err
	}
	if matches(tag, etag) {
		return notModified(etag), true, nil
	}
	return ok(etag), false, nil
}

// orgList reads what a read of one of the organization at slug's lists needs: the viewer, the organization they may
// read, and how long the read waits for the list to change.
func (a api) orgList(ctx context.Context, slug string, wait *WaitForChange, tag *IfNoneMatch) (orgs.Viewer, orgs.Organization, time.Duration, error) {
	v, err := viewer(ctx)
	if err != nil {
		return orgs.Viewer{}, orgs.Organization{}, 0, err
	}
	org, err := a.org(ctx, v, slug)
	if err != nil {
		return orgs.Viewer{}, orgs.Organization{}, 0, err
	}
	waits, err := listWait(wait, tag)
	return v, org, waits, err
}
