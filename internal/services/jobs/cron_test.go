package jobs_test

import (
	"testing"
	"time"

	"cobranch.dev/internal/services/jobs"
)

func TestParseCron(t *testing.T) {
	tests := []struct {
		expr    string
		wantErr string
	}{
		{expr: "0 3 * * *"},
		{expr: "*/15 * * * 1-5"},
		{expr: "0,30 8-18/2 1,15 1-12 0,7"},
		{expr: "5/10 * * * *"},
		{expr: "0 3 * *", wantErr: "a cron expression has five fields, not 4"},
		{expr: "60 * * * *", wantErr: `the minute field takes 0 to 59, not "60"`},
		{expr: "* * 0 * *", wantErr: `the day of the month field takes 1 to 31, not "0"`},
		{expr: "* * * 13 *", wantErr: `the month field takes 1 to 12, not "13"`},
		{expr: "* * * * 8", wantErr: `the day of the week field takes 0 to 7, not "8"`},
		{expr: "*/0 * * * *", wantErr: `the minute field's step "0" must be a positive number`},
		{expr: "5-1 * * * *", wantErr: `the minute field's range "5-1" runs backwards`},
		{expr: "mon * * * *", wantErr: `the minute field takes 0 to 59, not "mon"`},
	}
	for _, tt := range tests {
		t.Run(tt.expr, func(t *testing.T) {
			_, err := jobs.ParseCron(tt.expr)
			switch {
			case tt.wantErr == "" && err != nil:
				t.Fatalf("ParseCron() = %v", err)
			case tt.wantErr != "" && (err == nil || err.Error() != tt.wantErr):
				t.Fatalf("ParseCron() = %v, want %q", err, tt.wantErr)
			}
		})
	}
}

func TestCronNext(t *testing.T) {
	at := func(s string) time.Time {
		t.Helper()
		v, err := time.Parse(time.RFC3339, s)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	tests := []struct {
		expr, after, want string
	}{
		{"0 3 * * *", "2026-11-02T00:10:00Z", "2026-11-02T03:00:00Z"},
		{"0 3 * * *", "2026-11-02T03:00:00Z", "2026-11-03T03:00:00Z"},
		{"*/15 * * * *", "2026-11-02T10:07:30Z", "2026-11-02T10:15:00Z"},
		{"0 9 * * 1", "2026-11-02T10:00:00Z", "2026-11-09T09:00:00Z"},
		{"0 0 1 1 *", "2026-11-02T00:00:00Z", "2027-01-01T00:00:00Z"},
		{"0 0 29 2 *", "2026-11-02T00:00:00Z", "2028-02-29T00:00:00Z"},
		{"0 0 13 * 5", "2026-11-02T00:00:00Z", "2026-11-06T00:00:00Z"},
		{"0 0 * * 7", "2026-11-02T00:00:00Z", "2026-11-08T00:00:00Z"},
		{"30 1 * * *", "2026-11-02T03:00:00+02:00", "2026-11-02T01:30:00Z"},
		{"0 0 31 2 *", "2026-11-02T00:00:00Z", "0001-01-01T00:00:00Z"},
		{"0 3 */2 * *", "2026-11-03T04:00:00Z", "2026-11-05T03:00:00Z"},
		{"0 3 */2 * 1", "2026-11-02T00:00:00Z", "2026-11-09T03:00:00Z"},
		{"0 0 1 * */2", "2026-11-02T00:00:00Z", "2026-12-01T00:00:00Z"},
	}
	for _, tt := range tests {
		t.Run(tt.expr+" after "+tt.after, func(t *testing.T) {
			c, err := jobs.ParseCron(tt.expr)
			if err != nil {
				t.Fatal(err)
			}
			if got := c.Next(at(tt.after)); !got.Equal(at(tt.want)) {
				t.Errorf("Next() = %s, want %s", got, tt.want)
			}
		})
	}
}
