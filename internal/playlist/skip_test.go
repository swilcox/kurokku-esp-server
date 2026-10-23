package playlist

import (
	"context"
	"testing"
)

func TestSkip_NextAndPrevWrap(t *testing.T) {
	r, _ := setupResolver(t)
	ctx := context.Background()
	device := makeDevice("dev-1")
	pl := makePlaylist(messageEntry("a", 300), messageEntry("b", 300), messageEntry("c", 300))

	shown := func() string {
		t.Helper()
		resp, err := r.Resolve(ctx, device, pl)
		if err != nil {
			t.Fatal(err)
		}
		if resp.Instruction == nil {
			return ""
		}
		return resp.Instruction.Text
	}

	if got := shown(); got != "a" {
		t.Fatalf("first poll shows %q, want a", got)
	}
	if got := shown(); got != "" {
		t.Fatalf("second poll within duration shows %q, want no change", got)
	}
	steps := []struct {
		delta int
		want  string
	}{{1, "b"}, {1, "c"}, {1, "a"}, {-1, "c"}, {-1, "b"}}
	for _, s := range steps {
		if err := r.Skip(ctx, "dev-1", pl, s.delta); err != nil {
			t.Fatal(err)
		}
		if got := shown(); got != s.want {
			t.Errorf("after skip %+d shows %q, want %q", s.delta, got, s.want)
		}
		// The skipped-to entry gets its full duration.
		if got := shown(); got != "" {
			t.Errorf("poll after skip %+d shows %q, want no change", s.delta, got)
		}
	}
}

func TestSkip_PassesOverCronInactiveEntries(t *testing.T) {
	r, _ := setupResolver(t)
	ctx := context.Background()
	device := makeDevice("dev-1")
	off := messageEntry("off", 300)
	off.CronExpr = "59 3 1 1 *" // only 3:59 AM on Jan 1
	pl := makePlaylist(messageEntry("a", 300), off, messageEntry("c", 300))

	r.Resolve(ctx, device, pl) // shows a
	if err := r.Skip(ctx, "dev-1", pl, 1); err != nil {
		t.Fatal(err)
	}
	resp, _ := r.Resolve(ctx, device, pl)
	if resp.Instruction == nil || resp.Instruction.Text != "c" {
		t.Fatalf("next from a: got %+v, want c", resp.Instruction)
	}
	if err := r.Skip(ctx, "dev-1", pl, -1); err != nil {
		t.Fatal(err)
	}
	resp, _ = r.Resolve(ctx, device, pl)
	if resp.Instruction == nil || resp.Instruction.Text != "a" {
		t.Fatalf("prev from c: got %+v, want a", resp.Instruction)
	}
}

func TestSkip_NoStateOrPlaylist(t *testing.T) {
	r, _ := setupResolver(t)
	ctx := context.Background()
	if err := r.Skip(ctx, "dev-1", nil, 1); err != nil {
		t.Fatal(err)
	}
	// No prior poll: skipping from the start goes to entry 1.
	pl := makePlaylist(messageEntry("a", 300), messageEntry("b", 300))
	if err := r.Skip(ctx, "dev-1", pl, 1); err != nil {
		t.Fatal(err)
	}
	resp, _ := r.Resolve(ctx, makeDevice("dev-1"), pl)
	if resp.Instruction == nil || resp.Instruction.Text != "b" {
		t.Fatalf("got %+v, want b", resp.Instruction)
	}
}
