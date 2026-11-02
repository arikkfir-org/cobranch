package telemetry

import (
	"context"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Timings collects the calls a request makes to Cobranch's other workloads, such as the Git server's internal call,
// for its answer's Server-Timing header: a browser's developer tools show them beside the request, and the Console
// counts the Git reads behind each page view from them. The server that answers adds it (package server).
type Timings struct {
	mu    sync.Mutex
	calls []call
}

// call is one call a request made: its Server-Timing metric's name, when it started, and how long it took once it
// ended.
type call struct {
	name  string
	start time.Time
	dur   time.Duration
	done  bool
}

type timingsKey struct{}

// WithTimings returns ctx carrying a new Timings, for a request whose answer names its calls.
func WithTimings(ctx context.Context) (context.Context, *Timings) {
	t := &Timings{}
	return context.WithValue(ctx, timingsKey{}, t), t
}

// Time notes a call named name that starts now, on the Timings ctx carries, and returns what ends it. Without
// Timings, outside a request that names its calls, it notes nothing.
func Time(ctx context.Context, name string) (end func()) {
	t, ok := ctx.Value(timingsKey{}).(*Timings)
	if !ok {
		return func() {}
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	i := len(t.calls)
	t.calls = append(t.calls, call{name: name, start: time.Now()})
	return func() {
		t.mu.Lock()
		defer t.mu.Unlock()
		if c := &t.calls[i]; !c.done {
			c.dur, c.done = time.Since(c.start), true
		}
	}
}

// Header is the Server-Timing header's value for the calls noted so far, in the order they started: each call's name,
// and its duration in milliseconds once it ended, such as `git;dur=12.4, git`. It is empty when there were none.
func (t *Timings) Header() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	entries := make([]string, 0, len(t.calls))
	for _, c := range t.calls {
		entry := c.name
		if c.done {
			entry += ";dur=" + strconv.FormatFloat(float64(c.dur.Microseconds())/1000, 'f', -1, 64)
		}
		entries = append(entries, entry)
	}
	return strings.Join(entries, ", ")
}
