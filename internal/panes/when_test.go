package panes

import (
	"strings"
	"testing"
	"time"
)

func TestWhen(t *testing.T) {
	now := time.Date(2026, 10, 22, 14, 0, 0, 0, time.Local)
	cases := []struct {
		ago  time.Duration
		want string
	}{
		{10 * time.Second, "just now"},
		{4 * time.Minute, "4 min ago"},
		{3 * time.Hour, "3h ago"},
		{30 * time.Hour, now.Add(-30 * time.Hour).Format("Mon 15:04")},
		{20 * 24 * time.Hour, "2 Oct"},
		{400 * 24 * time.Hour, now.Add(-400 * 24 * time.Hour).Format("2 Jan 2006")},
	}
	for _, c := range cases {
		got := When(now.Add(-c.ago), now)
		if strings.TrimSpace(got) != c.want || len(got) < whenWidth {
			t.Errorf("%v: got %q want %q", c.ago, got, c.want)
		}
	}
}
