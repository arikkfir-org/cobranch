package rest

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"
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
