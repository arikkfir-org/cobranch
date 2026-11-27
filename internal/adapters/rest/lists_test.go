package rest

import (
	"context"
	"errors"
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
