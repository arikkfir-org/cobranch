package rest

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"
)

func TestReadListFollowerStops(t *testing.T) {
	gone := errors.New("gone")
	tests := []struct {
		name    string
		now     int
		nowErr  error
		want    int
		wantErr error
	}{
		{name: "what a read answers now", now: 2, want: 2},
		{name: "what a read fails with now", nowErr: gone, wantErr: gone},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			follow := func(context.Context) (<-chan int, error) {
				ch := make(chan int, 1)
				ch <- 1
				close(ch)
				return ch, nil
			}
			read := func(context.Context) (int, error) { return tt.now, tt.nowErr }
			// answer answers a list as itself, the caller holding 1.
			answer := func(list int) (int, bool, error) { return list, list == 1, nil }
			got, err := readList(t.Context(), 15*time.Second, read, follow, answer)
			if got != tt.want || !errors.Is(err, tt.wantErr) {
				t.Errorf("readList = %d, %v; want %d, %v", got, err, tt.want, tt.wantErr)
			}
		})
	}
}

func TestPaired(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		// as and bs are what each follower sends, in turn, a first; then as closes, or with cancel ctx ends.
		as, bs []int
		cancel bool
		want   []string
	}{
		{name: "nothing until both send", as: []int{1, 2}, want: []string{}},
		{name: "the latest of each, each time either sends", as: []int{1, 2}, bs: []int{10, 20}, want: []string{"1-10", "2-10", "2-20"}},
		{name: "ctx ending stops it", as: []int{1}, cancel: true, want: []string{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := runPaired(t, tt.as, tt.bs, tt.cancel)
			if fmt.Sprint(got) != fmt.Sprint(tt.want) {
				t.Errorf("sent %v; want %v", got, tt.want)
			}
		})
	}
}

// runPaired pairs two followers that send as and bs, in turn, a first, then ends them by closing as, or with cancel by
// ending ctx, and answers what paired sent, joined as "a-b".
func runPaired(t *testing.T, as, bs []int, cancel bool) []string {
	t.Helper()
	ctx, stop := context.WithCancel(t.Context())
	defer stop()
	aCh, bCh := make(chan int), make(chan int)
	out := paired(ctx, aCh, bCh, func(a, b int) string { return fmt.Sprintf("%d-%d", a, b) })
	got := []string{}
	done := make(chan struct{})
	go func() {
		defer close(done)
		for s := range out {
			got = append(got, s)
		}
	}()
	for i := range max(len(as), len(bs)) {
		if i < len(as) {
			aCh <- as[i]
		}
		if i < len(bs) {
			bCh <- bs[i]
		}
	}
	// paired sends what it has before it reads again, so it sees as close, or ctx end, after its last send.
	if cancel {
		stop()
	} else {
		close(aCh)
	}
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("paired never closed")
	}
	return got
}
