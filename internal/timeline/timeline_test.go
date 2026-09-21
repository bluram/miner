package timeline

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"miner/internal/fingerprint"
)

const sampleDir = "../../testdata/enerji_fm-2026-08"

func dayPaths(t *testing.T, day string) []string {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join(sampleDir, day+"-*_enerji_fm_*.mp3.bin"))
	if err != nil || len(paths) == 0 {
		t.Skip("testdata not present")
	}
	return paths
}

func TestBuildOneDay(t *testing.T) {
	tl, err := Build(dayPaths(t, "2026-08-01"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if tl.Channel != "enerji_fm" {
		t.Errorf("channel = %q", tl.Channel)
	}
	if len(tl.Segs) != 24 {
		t.Errorf("segments = %d, want 24", len(tl.Segs))
	}
	if len(tl.Skipped) != 0 {
		t.Errorf("skipped %v", tl.Skipped)
	}
	// 24 hours of audio, not 24 * 61 minutes: the overlap must be gone.
	got := fingerprint.FramesToDuration(tl.Len())
	if d := got - 24*time.Hour; d < -2*time.Minute || d > 2*time.Minute {
		t.Errorf("timeline spans %v, want ~24h", got)
	}
}

// Without trimming, each file contributes 61 minutes and the day comes out
// 24 minutes too long.
func TestOverlapIsTrimmed(t *testing.T) {
	paths := dayPaths(t, "2026-08-01")
	tl, err := Build(paths, nil)
	if err != nil {
		t.Fatal(err)
	}
	var untrimmed int
	for _, p := range paths {
		st, err := os.Stat(p)
		if err != nil {
			t.Fatal(err)
		}
		n, err := fingerprint.FrameCount(st.Size())
		if err != nil {
			t.Fatal(err)
		}
		untrimmed += n
	}
	if tl.Len() >= untrimmed {
		t.Errorf("timeline kept %d frames of %d; nothing was trimmed", tl.Len(), untrimmed)
	}
	cut := fingerprint.FramesToDuration(untrimmed - tl.Len())
	if cut < 20*time.Minute || cut > 28*time.Minute {
		t.Errorf("trimmed %v, want ~24m (24 files x 1m overlap)", cut)
	}
}

// Segments must tile the frame slice with no holes and no double-counting.
func TestSegmentsTileFrames(t *testing.T) {
	tl, err := Build(dayPaths(t, "2026-08-01"), nil)
	if err != nil {
		t.Fatal(err)
	}
	want := 0
	for i, s := range tl.Segs {
		if s.Global != want {
			t.Fatalf("segment %d starts at %d, want %d", i, s.Global, want)
		}
		want += s.N
	}
	if want != tl.Len() {
		t.Errorf("segments cover %d frames, timeline has %d", want, tl.Len())
	}
}

func TestAtMapsBackToWallClock(t *testing.T) {
	tl, err := Build(dayPaths(t, "2026-08-01"), nil)
	if err != nil {
		t.Fatal(err)
	}
	ts, path, off, err := tl.At(0)
	if err != nil {
		t.Fatal(err)
	}
	if off != 0 {
		t.Errorf("first frame offset = %d, want 0", off)
	}
	if want := time.Date(2026, 8, 1, 0, 0, 22, 0, time.UTC); !ts.Equal(want) {
		t.Errorf("first frame at %v, want %v", ts, want)
	}
	if filepath.Ext(path) != ".bin" {
		t.Errorf("path = %q", path)
	}

	// One hour in, we should be inside the second file and roughly an hour on.
	hour := fingerprint.DurationToFrames(time.Hour)
	ts, _, _, err = tl.At(hour)
	if err != nil {
		t.Fatal(err)
	}
	if d := ts.Sub(time.Date(2026, 8, 1, 1, 0, 22, 0, time.UTC)); d < -time.Second || d > time.Second {
		t.Errorf("frame at +1h maps to %v, off by %v", ts, d)
	}

	// Time must increase across the whole timeline.
	prev := tl.Time(0)
	for i := 1; i < len(tl.Segs); i++ {
		cur := tl.Time(tl.Segs[i].Global)
		if !cur.After(prev) {
			t.Fatalf("segment %d time %v is not after %v", i, cur, prev)
		}
		prev = cur
	}
}

func TestAtRejectsOutOfRange(t *testing.T) {
	tl, err := Build(dayPaths(t, "2026-08-01"), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, i := range []int{-1, tl.Len(), tl.Len() + 1000} {
		if _, _, _, err := tl.At(i); err == nil {
			t.Errorf("At(%d): want error", i)
		}
	}
}

// A match that spans two recordings is not one piece of audio.
func TestContiguous(t *testing.T) {
	tl, err := Build(dayPaths(t, "2026-08-01"), nil)
	if err != nil {
		t.Fatal(err)
	}
	s0 := tl.Segs[0]
	if !tl.Contiguous(10, 20) {
		t.Error("range inside the first segment reported as non-contiguous")
	}
	if tl.Contiguous(s0.N-5, s0.N+5) {
		t.Error("range straddling two segments reported as contiguous")
	}
	if tl.Contiguous(5, 5) || tl.Contiguous(-1, 10) || tl.Contiguous(0, tl.Len()+1) {
		t.Error("degenerate range accepted")
	}
}

// Requirement 6 of the acceptance: a corrupt file is skipped, not fatal.
func TestCorruptFileIsSkipped(t *testing.T) {
	paths := dayPaths(t, "2026-08-01")
	dir := t.TempDir()
	// Half-written and empty .bin files both occur when extraction is
	// interrupted; neither may abort the run.
	bad := []struct {
		name string
		data []byte
	}{
		{"2026-08-01-05-00-00_enerji_fm_srv4-61min.mp3.bin", nil},
		{"2026-08-01-06-00-00_enerji_fm_srv4-61min.mp3.bin", []byte{1, 2, 3, 4, 5}},
	}
	var extra []string
	for _, b := range bad {
		p := filepath.Join(dir, b.name)
		if err := os.WriteFile(p, b.data, 0o644); err != nil {
			t.Fatal(err)
		}
		extra = append(extra, p)
	}
	tl, err := Build(append(extra, paths...), nil)
	if err != nil {
		t.Fatalf("bad files killed the build: %v", err)
	}
	if len(tl.Skipped) != len(bad) {
		t.Errorf("skipped = %v, want %d bad files", tl.Skipped, len(bad))
	}
	if len(tl.Segs) != 24 {
		t.Errorf("segments = %d, want the 24 good files", len(tl.Segs))
	}
}

func TestBuildRejectsMixedChannels(t *testing.T) {
	dir := t.TempDir()
	for _, n := range []string{
		"2026-08-01-00-00-00_enerji_fm_srv4-61min.mp3.bin",
		"2026-08-01-01-00-00_araz_fm_srv4-61min.mp3.bin",
	} {
		if err := os.WriteFile(filepath.Join(dir, n), make([]byte, 12), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	paths, _ := Glob(dir)
	if _, err := Build(paths, nil); err == nil {
		t.Error("want error for mixed channels")
	}
}

func TestBuildRejectsEmpty(t *testing.T) {
	if _, err := Build(nil, nil); err == nil {
		t.Error("want error for no files")
	}
}

func TestGlobMissingDir(t *testing.T) {
	if _, err := Glob(filepath.Join(t.TempDir(), "nope")); err == nil {
		t.Error("want error for directory with no fingerprints")
	}
}
