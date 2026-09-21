package cluster

import (
	"testing"
)

const (
	minLen = 100
	tol    = 0.5 // overlap fraction at which two intervals are one airing
)

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
	got := Group(pairs, tol)
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
	got := Group(pairs, tol)
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
	got := Group(pairs, tol)
	if len(got) != 2 {
		t.Fatalf("clusters = %d, want 2: %+v", len(got), got)
	}
}

// Adjacent but distinct fragments must not collapse into one airing.
func TestGroupDoesNotMergeAdjacent(t *testing.T) {
	pairs := []Pair{
		{A: 1000, B: 50000, Len: 300},
		{A: 1290, B: 80000, Len: 300},
	}
	got := Group(pairs, tol)
	if len(got) != 2 {
		t.Fatalf("clusters = %d, want 2: %+v", len(got), got)
	}
}

func TestGroupDropsSingletons(t *testing.T) {
	// Both sides land in the same airing, so nothing actually repeats.
	pairs := []Pair{{A: 100, B: 110, Len: 300}}
	if got := Group(pairs, tol); len(got) != 0 {
		t.Errorf("clusters = %+v, want none", got)
	}
}

func TestGroupEmpty(t *testing.T) {
	if got := Group(nil, tol); got != nil {
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

// Taking the shortest occurrence pinned every large cluster to the
// verification floor, because some occurrence is always barely over it.
func TestClusterLenIsMedianNotMinimum(t *testing.T) {
	c := Cluster{Occurrences: []Occurrence{
		{0, 100}, {1000, 3000}, {2000, 3100}, {3000, 3050}, {4000, 2900},
	}}
	if got := c.Len(); got != 3000 {
		t.Errorf("Len = %d, want the median 3000, not the minimum 100", got)
	}
}

// A chain of intervals each overlapping the next must not weld the whole
// timeline into one airing. Growing the merged extent used to allow exactly
// that, producing clusters with thousands of bogus occurrences.
func TestGroupDoesNotChainAcrossTimeline(t *testing.T) {
	// Intervals marching forward in tol-sized steps, each overlapping its
	// neighbour heavily.
	var pairs []Pair
	for i := 0; i < 200; i++ {
		pairs = append(pairs, Pair{A: i * 20, B: 500000 + i*20, Len: 3000})
	}
	got := Group(pairs, tol)
	for _, c := range got {
		for _, o := range c.Occurrences {
			if o.Start > 200*20 && o.Start < 500000 {
				t.Fatalf("airing at %d is in neither group; intervals were chained", o.Start)
			}
		}
	}
	// The march spans 200*tol frames, so it cannot be one airing.
	for _, c := range got {
		if len(c.Occurrences) > 250 {
			t.Errorf("cluster has %d occurrences from 200 pairs; chaining", len(c.Occurrences))
		}
	}
}

// Two intervals a hair apart are the same airing found by different pairs.
func TestGroupMergesWithinTolerance(t *testing.T) {
	pairs := []Pair{
		{A: 1000, B: 50000, Len: 3000},
		{A: 1000 + 1000, B: 90000, Len: 2950},
	}
	got := Group(pairs, tol)
	if len(got) != 1 {
		t.Fatalf("clusters = %d, want 1: %+v", len(got), got)
	}
	if n := len(got[0].Occurrences); n != 3 {
		t.Errorf("occurrences = %d, want 3", n)
	}
}

func TestGroupIsDeterministic(t *testing.T) {
	pairs := []Pair{
		{A: 5000, B: 9000, Len: 300},
		{A: 100, B: 5000, Len: 300},
		{A: 100, B: 9000, Len: 300},
		{A: 40000, B: 70000, Len: 500},
	}
	first := Group(pairs, tol)
	for i := 0; i < 20; i++ {
		got := Group(pairs, tol)
		if len(got) != len(first) {
			t.Fatalf("run %d: %d clusters, first had %d", i, len(got), len(first))
		}
		for j := range got {
			if got[j].ID != first[j].ID || len(got[j].Occurrences) != len(first[j].Occurrences) {
				t.Fatalf("run %d cluster %d differs", i, j)
			}
			for k := range got[j].Occurrences {
				if got[j].Occurrences[k] != first[j].Occurrences[k] {
					t.Fatalf("run %d cluster %d occurrence %d differs", i, j, k)
				}
			}
		}
	}
}

// Occurrences 5 seconds apart on a 10-second fragment overlap, so they are
// one airing found twice, not two airings.
func TestGroupOccurrencesDoNotOverlap(t *testing.T) {
	var pairs []Pair
	for i := 0; i < 8; i++ {
		pairs = append(pairs, Pair{A: i * 500, B: 200000 + i*500, Len: 1000})
	}
	got := Group(pairs, tol)
	for _, c := range got {
		for i := 1; i < len(c.Occurrences); i++ {
			prev, cur := c.Occurrences[i-1], c.Occurrences[i]
			if cur.Start < prev.End() {
				t.Errorf("occurrence %+v overlaps %+v", cur, prev)
			}
		}
	}
}

func TestDisjointKeepsSpacedOccurrences(t *testing.T) {
	in := []Occurrence{{0, 100}, {50, 100}, {200, 100}, {250, 100}, {400, 100}}
	got := disjoint(append([]Occurrence(nil), in...))
	want := []Occurrence{{0, 100}, {200, 100}, {400, 100}}
	if len(got) != len(want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("got %+v, want %+v", got, want)
		}
	}
}
