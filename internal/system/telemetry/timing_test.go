package telemetry

import (
	"context"
	"regexp"
	"testing"
)

func TestTimingsHeader(t *testing.T) {
	tests := []struct {
		name  string
		calls []bool // whether each call ended
		want  string // a pattern of the header
	}{
		{name: "no calls", want: `^$`},
		{name: "one call", calls: []bool{true}, want: `^git;dur=\d+(\.\d+)?$`},
		{name: "two calls", calls: []bool{true, true}, want: `^git;dur=\d+(\.\d+)?, git;dur=\d+(\.\d+)?$`},
		{name: "a call still running", calls: []bool{true, false}, want: `^git;dur=\d+(\.\d+)?, git$`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, timings := WithTimings(context.Background())
			for _, ends := range tt.calls {
				end := Time(ctx, "git")
				if ends {
					end()
					end() // a second end changes nothing
				}
			}
			if got := timings.Header(); !regexp.MustCompile(tt.want).MatchString(got) {
				t.Errorf("Header() = %q, want %s", got, tt.want)
			}
		})
	}
}

func TestTimeWithoutTimings(*testing.T) {
	end := Time(context.Background(), "git")
	end() // notes nothing, and doesn't panic
}
