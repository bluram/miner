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
	got := Group(pairs, tol, nil)
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
	got := Group(pairs, tol, nil)
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
	got := Group(pairs, tol, nil)
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
	got := Group(pairs, tol, nil)
	if len(got) != 2 {
		t.Fatalf("clusters = %d, want 2: %+v", len(got), got)
	}
}

func TestGroupDropsSingletons(t *testing.T) {
	// Both sides land in the same airing, so nothing actually repeats.
	pairs := []Pair{{A: 100, B: 110, Len: 300}}
	if got := Group(pairs, tol, nil); len(got) != 0 {
		t.Errorf("clusters = %+v, want none", got)
	}
}

func TestGroupEmpty(t *testing.T) {
	if got := Group(nil, tol, nil); got != nil {
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
	got := Group(pairs, tol, nil)
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
	got := Group(pairs, tol, nil)
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
	first := Group(pairs, tol, nil)
	for i := 0; i < 20; i++ {
		got := Group(pairs, tol, nil)
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
	got := Group(pairs, tol, nil)
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

// A truncated reading of an airing must not beat the full-length one. Sorting
// shortest-first let an 11-second interval win over the 40-second fragment at
// the same position, which then bridged unrelated clusters together.
func TestGroupPrefersLongerOccurrence(t *testing.T) {
	pairs := []Pair{
		{A: 1000, B: 100000, Len: 4000},
		{A: 1000, B: 200000, Len: 4000},
		{A: 1000, B: 300000, Len: 1100}, // truncated reading of the same airing
	}
	got := Group(pairs, tol, nil)
	if len(got) == 0 {
		t.Fatal("no clusters")
	}
	// The full-length airing must survive somewhere, and the cluster holding
	// it must not have been truncated to the short reading.
	var full *Cluster
	for i, c := range got {
		for _, o := range c.Occurrences {
			if o.Start == 1000 && o.Len == 4000 {
				full = &got[i]
			}
		}
	}
	if full == nil {
		t.Fatalf("the 4000-frame airing at 1000 was lost: %+v", got)
	}
	if n := len(full.Occurrences); n != 3 {
		t.Errorf("cluster has %d occurrences, want the 3 full-length ones", n)
	}
	if full.Len() != 4000 {
		t.Errorf("cluster length = %d, want 4000", full.Len())
	}
}

// A pair covering only a fraction of an airing found a nested fragment. With
// transitive closure one such link is permanent, so it must be refused.
func TestGroupRefusesPartialLink(t *testing.T) {
	pairs := []Pair{
		// A 40-frame-per-second-ish fragment, three airings.
		{A: 1000, B: 100000, Len: 4000},
		{A: 1000, B: 200000, Len: 4000},
		// A short jingle cluster elsewhere.
		{A: 500000, B: 600000, Len: 1200},
		{A: 500000, B: 700000, Len: 1200},
		// The bridge: a short match from inside the long fragment to the jingle.
		{A: 1000, B: 500000, Len: 1200},
	}
	got := Group(pairs, tol, nil)
	for _, c := range got {
		var hasLong, hasShort bool
		for _, o := range c.Occurrences {
			if o.Len >= 4000 {
				hasLong = true
			}
			if o.Len <= 1200 {
				hasShort = true
			}
		}
		if hasLong && hasShort {
			t.Errorf("cluster mixes a 4000-frame fragment with a 1200-frame one: %+v", c.Occurrences)
		}
	}
}

// Lengths drifting in small steps used to chain a 9-second fragment all the
// way to a 40-second one: every link passed, the closure was nonsense.
// Checking each candidate against the cluster's own representative stops it.
func TestGroupDoesNotChainThroughLengthDrift(t *testing.T) {
	var pairs []Pair
	// A ladder of airings whose lengths creep up 10% at a time.
	length := 1100
	start := 0
	for i := 0; i < 40; i++ {
		next := length * 11 / 10
		pairs = append(pairs, Pair{A: start, B: start + 100000, Len: min(length, next)})
		start += 200000
		length = next
	}
	// Representative test: only accept candidates within 20% on length.
	same := func(a, b Occurrence, shift int) bool {
		lo, hi := a.Len, b.Len
		if lo > hi {
			lo, hi = hi, lo
		}
		return float64(hi)/float64(lo) <= 1.2
	}
	got := Group(pairs, tol, same)
	for _, c := range got {
		lo, hi := c.Occurrences[0].Len, c.Occurrences[0].Len
		for _, o := range c.Occurrences {
			lo, hi = min(lo, o.Len), max(hi, o.Len)
		}
		if float64(hi)/float64(lo) > 1.5 {
			t.Errorf("cluster spans lengths %d..%d; chained through drift", lo, hi)
		}
	}
}

// The representative test must be able to reject a proposed member outright.
func TestGroupHonoursSameRejection(t *testing.T) {
	pairs := []Pair{
		{A: 1000, B: 100000, Len: 3000},
		{A: 1000, B: 200000, Len: 3000},
	}
	reject := func(a, b Occurrence, shift int) bool { return false }
	for _, c := range Group(pairs, tol, reject) {
		if len(c.Occurrences) > 1 {
			t.Errorf("cluster kept %d occurrences though every candidate was rejected", len(c.Occurrences))
		}
	}
}

// A 10-second piece of a 34-second advert repeats exactly as often as the
// advert, so it clusters just as well. Reporting both is fragmentation.
func TestSubsumeDropsPieceOfLongerFragment(t *testing.T) {
	long := Cluster{Occurrences: []Occurrence{
		{1000, 3400}, {50000, 3400}, {90000, 3400},
	}}
	// A piece sitting inside each airing of the long one.
	piece := Cluster{Occurrences: []Occurrence{
		{1200, 1000}, {50200, 1000}, {90200, 1000},
	}}
	got := Subsume([]Cluster{long, piece}, 0.8)
	if len(got) != 1 {
		t.Fatalf("kept %d clusters, want 1: %+v", len(got), got)
	}
	if got[0].Len() != 3400 {
		t.Errorf("kept the %d-frame cluster, want the 3400 one", got[0].Len())
	}
}

// Two fragments that merely sit near each other are both real.
func TestSubsumeKeepsIndependentClusters(t *testing.T) {
	a := Cluster{Occurrences: []Occurrence{{1000, 3400}, {50000, 3400}}}
	b := Cluster{Occurrences: []Occurrence{{20000, 1000}, {70000, 1000}}}
	if got := Subsume([]Cluster{a, b}, 0.8); len(got) != 2 {
		t.Errorf("kept %d clusters, want 2", len(got))
	}
}

// A shorter cluster only partly covered by a longer one survives.
func TestSubsumeKeepsPartlyCovered(t *testing.T) {
	long := Cluster{Occurrences: []Occurrence{{1000, 2000}, {50000, 2000}}}
	// Each piece hangs half outside the long airing.
	short := Cluster{Occurrences: []Occurrence{{2000, 2000}, {51000, 2000}}}
	if got := Subsume([]Cluster{long, short}, 0.8); len(got) != 2 {
		t.Errorf("kept %d clusters, want 2", len(got))
	}
}

func TestSubsumeTrivialInputs(t *testing.T) {
	if got := Subsume(nil, 0.8); len(got) != 0 {
		t.Errorf("got %+v, want none", got)
	}
	one := []Cluster{{Occurrences: []Occurrence{{0, 100}, {500, 100}}}}
	if got := Subsume(one, 0.8); len(got) != 1 {
		t.Errorf("kept %d, want 1", len(got))
	}
}
