package miner

import (
	"miner/internal/cluster"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"
)

const sampleDir = "testdata/enerji_fm-2026-08"

func dayPaths(t *testing.T) []string {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join(sampleDir, "2026-08-01-*_enerji_fm_*.mp3.bin"))
	if err != nil || len(paths) == 0 {
		t.Skip("testdata not present")
	}
	sort.Strings(paths)
	return paths
}

func TestOptionsDefaults(t *testing.T) {
	got := Options{}.Defaults()
	if got.Min != 10*time.Second || got.Max != 120*time.Second {
		t.Errorf("lengths = %v..%v", got.Min, got.Max)
	}
	if got.MinCount != 2 {
		t.Errorf("MinCount = %d", got.MinCount)
	}
	if got.Rate <= 0 || got.Rate > 1 {
		t.Errorf("Rate = %v", got.Rate)
	}
	// Explicit values survive.
	set := Options{Min: time.Second, Max: time.Minute, MinCount: 5, Rate: 0.9}.Defaults()
	if set.Min != time.Second || set.MinCount != 5 || set.Rate != 0.9 {
		t.Errorf("Defaults overwrote explicit options: %+v", set)
	}
}

func TestFindRejectsBadRange(t *testing.T) {
	if _, err := Find(nil, Options{Min: time.Minute, Max: time.Second}); err == nil {
		t.Error("want error when min exceeds max")
	}
}

func TestFindRejectsNoFiles(t *testing.T) {
	if _, err := Find(nil, Options{}); err == nil {
		t.Error("want error for no files")
	}
}

func TestFindOneDay(t *testing.T) {
	res, err := Find(dayPaths(t), Options{})
	if err != nil {
		t.Fatal(err)
	}
	if res.Channel != "enerji_fm" {
		t.Errorf("channel = %q", res.Channel)
	}
	if len(res.Clusters) == 0 {
		t.Fatal("no clusters found in a day of radio")
	}
	if !res.To.After(res.From) {
		t.Errorf("period %v..%v", res.From, res.To)
	}

	for _, c := range res.Clusters {
		if c.Count < 2 {
			t.Errorf("cluster %d has %d airings", c.ID, c.Count)
		}
		if c.Count != len(c.Occurrences) {
			t.Errorf("cluster %d: count %d, occurrences %d", c.ID, c.Count, len(c.Occurrences))
		}
		if c.DurationSec < 9.9 || c.DurationSec > 120.1 {
			t.Errorf("cluster %d: duration %.1fs outside 10-120s", c.ID, c.DurationSec)
		}
		var prev time.Time
		for i, o := range c.Occurrences {
			if !o.End.After(o.Start) {
				t.Errorf("cluster %d occurrence %d: end %v not after start %v", c.ID, i, o.End, o.Start)
			}
			if i > 0 && !o.Start.After(prev) {
				t.Errorf("cluster %d: occurrences out of order", c.ID)
			}
			prev = o.Start
			if o.File == "" || o.OffsetSec < 0 {
				t.Errorf("cluster %d occurrence %d: file %q offset %v", c.ID, i, o.File, o.OffsetSec)
			}
		}
	}

	// IDs are 1..n in time order.
	for i, c := range res.Clusters {
		if c.ID != i+1 {
			t.Errorf("cluster at %d has ID %d", i, c.ID)
		}
	}
}

// Acceptance requirement 5: the same input gives the same clusters.
func TestFindIsDeterministic(t *testing.T) {
	paths := dayPaths(t)[:6]
	first, err := Find(paths, Options{})
	if err != nil {
		t.Fatal(err)
	}
	second, err := Find(paths, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Clusters) != len(second.Clusters) {
		t.Fatalf("%d clusters then %d", len(first.Clusters), len(second.Clusters))
	}
	for i := range first.Clusters {
		a, b := first.Clusters[i], second.Clusters[i]
		if a.ID != b.ID || a.Count != b.Count || a.DurationSec != b.DurationSec {
			t.Fatalf("cluster %d differs: %+v vs %+v", i, a, b)
		}
		for j := range a.Occurrences {
			if a.Occurrences[j] != b.Occurrences[j] {
				t.Fatalf("cluster %d occurrence %d differs", i, j)
			}
		}
	}
}

func TestFindHonoursLengthBounds(t *testing.T) {
	res, err := Find(dayPaths(t)[:8], Options{Min: 20 * time.Second, Max: 40 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range res.Clusters {
		if c.DurationSec < 19.9 || c.DurationSec > 40.1 {
			t.Errorf("cluster %d: %.1fs outside the requested 20-40s", c.ID, c.DurationSec)
		}
	}
}

func TestFindHonoursMinCount(t *testing.T) {
	res, err := Find(dayPaths(t)[:8], Options{MinCount: 5})
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range res.Clusters {
		if c.Count < 5 {
			t.Errorf("cluster %d has %d airings, want at least 5", c.ID, c.Count)
		}
	}
}

// Acceptance requirement 6: a corrupt file is skipped, not fatal.
func TestFindSurvivesCorruptFile(t *testing.T) {
	paths := dayPaths(t)[:6]
	dir := t.TempDir()
	bad := filepath.Join(dir, "2026-08-01-23-00-00_enerji_fm_srv4-61min.mp3.bin")
	if err := os.WriteFile(bad, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := Find(append([]string{bad}, paths...), Options{})
	if err != nil {
		t.Fatalf("a corrupt file killed the run: %v", err)
	}
	if len(res.Skipped) != 1 {
		t.Errorf("skipped = %v, want the one bad file", res.Skipped)
	}
	if len(res.Clusters) == 0 {
		t.Error("no clusters after skipping the bad file")
	}
}

func TestRepresentativeIsAMember(t *testing.T) {
	res, err := Find(dayPaths(t)[:8], Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Clusters) == 0 {
		t.Skip("no clusters")
	}
	for _, c := range res.Clusters {
		rep := c.Representative()
		found := false
		for _, o := range c.Occurrences {
			if o == rep {
				found = true
			}
		}
		if !found {
			t.Errorf("cluster %d: representative is not one of its occurrences", c.ID)
		}
	}
}

func TestChunks(t *testing.T) {
	for _, tc := range []struct {
		n, w, want int
	}{
		{100, 4, 4},
		{100, 1, 1},
		{3, 8, 3},
		{0, 4, 0},
		{100, 0, 1},
	} {
		got := chunks(tc.n, tc.w)
		if len(got) != tc.want {
			t.Errorf("chunks(%d,%d) gave %d ranges, want %d", tc.n, tc.w, len(got), tc.want)
		}
		// Ranges must tile [0,n) exactly, with no gaps or overlaps.
		at := 0
		for _, r := range got {
			if r[0] != at {
				t.Fatalf("chunks(%d,%d): range starts at %d, want %d", tc.n, tc.w, r[0], at)
			}
			if r[1] <= r[0] {
				t.Fatalf("chunks(%d,%d): empty range %v", tc.n, tc.w, r)
			}
			at = r[1]
		}
		if at != tc.n {
			t.Errorf("chunks(%d,%d) covered %d, want %d", tc.n, tc.w, at, tc.n)
		}
	}
}

// The worker count must not change what is found. Extents can differ by a
// fraction of a second on a fragment-pair that straddles a chunk boundary,
// since the two workers seed it at different positions, so the clusters are
// compared rather than their exact lengths.
func TestFindWorkerCountDoesNotChangeResult(t *testing.T) {
	paths := dayPaths(t)[:8]
	one, err := Find(paths, Options{Workers: 1})
	if err != nil {
		t.Fatal(err)
	}
	many, err := Find(paths, Options{Workers: 8})
	if err != nil {
		t.Fatal(err)
	}
	if len(one.Clusters) != len(many.Clusters) {
		t.Fatalf("1 worker gave %d clusters, 8 gave %d", len(one.Clusters), len(many.Clusters))
	}
	for i := range one.Clusters {
		a, b := one.Clusters[i], many.Clusters[i]
		if a.Count != b.Count {
			t.Errorf("cluster %d: %d airings with one worker, %d with eight", i+1, a.Count, b.Count)
		}
		if d := a.DurationSec - b.DurationSec; d > 1 || d < -1 {
			t.Errorf("cluster %d: %.2fs with one worker, %.2fs with eight", i+1, a.DurationSec, b.DurationSec)
		}
		for j := range a.Occurrences {
			if !a.Occurrences[j].Start.Equal(b.Occurrences[j].Start) {
				t.Errorf("cluster %d occurrence %d starts differ: %v vs %v",
					i+1, j, a.Occurrences[j].Start, b.Occurrences[j].Start)
			}
		}
	}
}

func TestFindParallelIsDeterministic(t *testing.T) {
	paths := dayPaths(t)[:8]
	first, err := Find(paths, Options{Workers: 8})
	if err != nil {
		t.Fatal(err)
	}
	for run := 0; run < 3; run++ {
		got, err := Find(paths, Options{Workers: 8})
		if err != nil {
			t.Fatal(err)
		}
		if len(got.Clusters) != len(first.Clusters) {
			t.Fatalf("run %d: %d clusters, first had %d", run, len(got.Clusters), len(first.Clusters))
		}
		for i := range got.Clusters {
			a, b := first.Clusters[i], got.Clusters[i]
			if a.ID != b.ID || a.Count != b.Count || a.DurationSec != b.DurationSec {
				t.Fatalf("run %d cluster %d differs: %+v vs %+v", run, i+1, a, b)
			}
			for j := range a.Occurrences {
				if a.Occurrences[j] != b.Occurrences[j] {
					t.Fatalf("run %d cluster %d occurrence %d differs", run, i+1, j)
				}
			}
		}
	}
}

func TestDedupeKeepsEarliestPerDiagonal(t *testing.T) {
	// Two readings of one fragment-pair on diagonal 5000, plus an unrelated
	// pair further along the same diagonal and one on another diagonal.
	in := []cluster.Pair{
		{A: 1000, B: 6000, Len: 3000},
		{A: 1200, B: 6200, Len: 2900}, // overlaps the first, same diagonal
		{A: 9000, B: 14000, Len: 2000},
		{A: 1000, B: 8000, Len: 3000}, // different diagonal
	}
	got := dedupe(append([]cluster.Pair(nil), in...))
	if len(got) != 3 {
		t.Fatalf("kept %d pairs, want 3: %+v", len(got), got)
	}
	for _, p := range got {
		if p.A == 1200 {
			t.Errorf("kept the overlapping duplicate: %+v", got)
		}
	}
}

func TestDedupeTrivial(t *testing.T) {
	if got := dedupe(nil); len(got) != 0 {
		t.Errorf("got %+v", got)
	}
	one := []cluster.Pair{{A: 1, B: 2, Len: 3}}
	if got := dedupe(one); len(got) != 1 {
		t.Errorf("got %+v", got)
	}
}
