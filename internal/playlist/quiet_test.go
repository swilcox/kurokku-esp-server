package playlist

import (
	"context"
	"testing"
	"time"
)

func TestQuiet(t *testing.T) {
	at := func(h, m int) time.Time { return time.Date(2026, 10, 23, h, m, 0, 0, time.Local) }
	cron := func(s string) *string { return &s }
	tests := []struct {
		name string
		cron *string
		now  time.Time
		want bool
	}{
		{"unset", nil, at(23, 30), false},
		{"empty", cron(""), at(23, 30), false},
		{"inside", cron("* 23,0-6 * * *"), at(23, 30), true},
		{"after midnight", cron("* 23,0-6 * * *"), at(6, 59), true},
		{"outside", cron("* 23,0-6 * * *"), at(7, 0), false},
		{"invalid never matches", cron("not a cron"), at(23, 30), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := makeDevice("dev-1")
			d.QuietCron = tt.cron
			if got := Quiet(d, tt.now); got != tt.want {
				t.Errorf("Quiet = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestResolve_DisplayOffDuringQuietHours(t *testing.T) {
	r, _ := setupResolver(t)
	ctx := context.Background()
	pl := makePlaylist(messageEntry("a", 300))

	always := "* * * * *"
	quiet := makeDevice("dev-1")
	quiet.QuietCron = &always
	// First poll (new instruction) and second (no change) both say off.
	for i := range 2 {
		resp, err := r.Resolve(ctx, quiet, pl)
		if err != nil {
			t.Fatal(err)
		}
		if !resp.DisplayOff {
			t.Errorf("poll %d: DisplayOff = false during quiet hours", i)
		}
	}

	resp, err := r.Resolve(ctx, makeDevice("dev-2"), pl)
	if err != nil {
		t.Fatal(err)
	}
	if resp.DisplayOff {
		t.Error("DisplayOff = true with no quiet hours")
	}
}
