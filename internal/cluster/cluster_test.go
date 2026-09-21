package cluster

import (
	"testing"
)

const minLen = 100

func collect(tol int, hits [][2]int) []Pair {
	c := NewCollector(minLen, tol)
	for _, h := range hits {
		c.Add(h[0], h[1])
	}
	return c.Pairs()
}

// A 150-frame repeat produces hits at 51 consecutive positions on one diagonal.
func TestCollectorOneRun(t *testing.T) {
	var hits [][2]int
	for pos := 1000; pos <= 1050; pos++ {
		hits = append(hits, [2]int{pos, 5000})
	}
	got := collect(10, hits)
	if len(got) != 1 {
		t.Fatalf("pairs = %d, want 1: %+v", len(got), got)
	}
	want := Pair{A: 1000, B: 6000, Len: 150}
	if got[0] != want {
		t.Errorf("pair = %+v, want %+v", got[0], want)
	}
}

// Two airings far apart share a diagonal only by accident; they must not
// merge into one enormous repeat. This is the bug the month run exposed.
func TestCollectorSplitsDistantHitsOnSameDiagonal(t *testing.T) {
	var hits [][2]int
	for pos := 1000; pos <= 1010; pos++ {
		hits = append(hits, [2]int{pos, 5000})
	}
	for pos := 900000; pos <= 900010; pos++ {
		hits = append(hits, [2]int{pos, 5000})
	}
	got := collect(10, hits)
	if len(got) != 2 {
		t.Fatalf("pairs = %d, want 2: %+v", len(got), got)
	}
	for _, p := range got {
		if p.Len > 200 {
			t.Errorf("pair %+v spans %d frames; runs were joined across a gap", p, p.Len)
		}
	}
}

// Verification fails at the odd position inside a real repeat.
func TestCollectorToleratesHoles(t *testing.T) {
	var hits [][2]int
	for pos := 0; pos <= 40; pos++ {
		if pos == 17 || pos == 18 {
			continue
		}
		hits = append(hits, [2]int{pos, 3000})
	}
	got := collect(5, hits)
	if len(got) != 1 {
		t.Fatalf("pairs = %d, want 1: %+v", len(got), got)
	}
	if got[0].Len != 140 {
		t.Errorf("len = %d, want 140", got[0].Len)
	}
}

func TestCollectorIgnoresNonPositiveDiagonal(t *testing.T) {
	if got := collect(10, [][2]int{{100, 0}, {100, -50}}); len(got) != 0 {
		t.Errorf("pairs = %+v, want none", got)
	}
}

func TestCollectorIsDeterministic(t *testing.T) {
	var hits [][2]int
	for _, d := range []int{9000, 3000, 6000} {
		for pos := 0; pos <= 20; pos++ {
			hits = append(hits, [2]int{pos, d})
		}
	}
	first := collect(5, hits)
	for i := 0; i < 20; i++ {
		got := collect(5, hits)
		if len(got) != len(first) {
			t.Fatalf("run %d: %d pairs, first run had %d", i, len(got), len(first))
		}
		for j := range got {
			if got[j] != first[j] {
				t.Fatalf("run %d differs at %d: %+v vs %+v", i, j, got[j], first[j])
			}
		}
	}
}

// Three airings of one fragment must land in one cluster, not three pairs.
func TestGroupTransitive(t *testing.T) {
	pairs := []Pair{
		{A: 100, B: 5000, Len: 300},
		{A: 100, B: 9000, Len: 300},
		{A: 5000, B: 9000, Len: 300},
	}
	got := Group(pairs, 0.5)
	if len(got) != 1 {
		t.Fatalf("clusters = %d, want 1: %+v", len(got), got)
	}
	if n := len(got[0].Occurrences); n != 3 {
		t.Errorf("occurrences = %d, want 3: %+v", n, got[0].Occurrences)
	}
	if got[0].Len() != 300 {
		t.Errorf("len = %d, want 300", got[0].Len())
	}
	if got[0].ID != 1 {
		t.Errorf("id = %d, want 1", got[0].ID)
	}
}

// Slightly different boundaries for the same airing are one occurrence.
func TestGroupMergesNearIdenticalIntervals(t *testing.T) {
	pairs := []Pair{
		{A: 100, B: 5000, Len: 300},
		{A: 105, B: 9000, Len: 295},
	}
	got := Group(pairs, 0.5)
	if len(got) != 1 {
		t.Fatalf("clusters = %d, want 1", len(got))
	}
	if n := len(got[0].Occurrences); n != 3 {
		t.Errorf("occurrences = %d, want 3 (one merged origin plus two copies)", n)
	}
}

// Two different fragments must stay apart.
func TestGroupKeepsUnrelatedApart(t *testing.T) {
	pairs := []Pair{
		{A: 100, B: 5000, Len: 300},
		{A: 20000, B: 40000, Len: 300},
	}
	got := Group(pairs, 0.5)
	if len(got) != 2 {
		t.Fatalf("clusters = %d, want 2: %+v", len(got), got)
	}
}

// Adjacent but distinct fragments barely touch; they are not one airing.
func TestGroupDoesNotMergeAdjacent(t *testing.T) {
	a := Occurrence{1000, 300}
	b := Occurrence{1290, 300}
	if overlaps(a, b, 0.5) {
		t.Error("intervals sharing 10 frames were treated as the same airing")
	}
}

func TestGroupDropsSingletons(t *testing.T) {
	// Both sides fall inside one merged occurrence, so nothing repeats.
	pairs := []Pair{{A: 100, B: 110, Len: 300}}
	if got := Group(pairs, 0.5); len(got) != 0 {
		t.Errorf("clusters = %+v, want none", got)
	}
}

func TestGroupEmpty(t *testing.T) {
	if got := Group(nil, 0.5); got != nil {
		t.Errorf("got %+v, want nil", got)
	}
}

func TestClusterLenEmpty(t *testing.T) {
	if (Cluster{}).Len() != 0 {
		t.Error("empty cluster should have zero length")
	}
}

func TestOccurrenceEnd(t *testing.T) {
	if got := (Occurrence{100, 50}).End(); got != 150 {
		t.Errorf("End = %d, want 150", got)
	}
}
