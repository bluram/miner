// Package cluster turns verified hits into repeat groups.
//
// A hit says frames[pos:pos+minLen] matches frames[pos+d:pos+d+minLen]. A
// repeat longer than minLen therefore produces a hit at every position it
// covers, all on the same diagonal d. So a repeat is a contiguous run of
// hits on one diagonal, and its length is that run plus minLen.
//
// Taking a diagonal's first and last hit over a whole month instead gives
// garbage: unrelated matches from different days share offsets.
package cluster

import (
	"sort"
)

// Pair is one repeat: [A,A+Len) matches [B,B+Len), A < B.
type Pair struct {
	A, B, Len int
}

// Occurrence is one airing of a fragment.
type Occurrence struct {
	Start, Len int
}

// End is the frame just past the occurrence.
func (o Occurrence) End() int { return o.Start + o.Len }

// Cluster is every occurrence of one fragment.
type Cluster struct {
	ID          int
	Occurrences []Occurrence
}

// Len is the representative length: the median occurrence. The shortest
// would collapse to the verification floor, since in a large cluster some
// occurrence is always barely over it.
func (c Cluster) Len() int {
	if len(c.Occurrences) == 0 {
		return 0
	}
	lens := make([]int, len(c.Occurrences))
	for i, o := range c.Occurrences {
		lens[i] = o.Len
	}
	sort.Ints(lens)
	return lens[len(lens)/2]
}

// Collector streams hits into pairs. Hits must arrive with non-decreasing pos.
type Collector struct {
	minLen int
	tol    int
	open   map[int]*run
	pairs  []Pair
	nextGC int
}

type run struct{ lo, hi int }

// NewCollector takes the verification window and how many missed positions
// may interrupt a run. Verification fails at the odd position inside a real
// repeat, so a run must survive small holes.
func NewCollector(minLen, tol int) *Collector {
	return &Collector{minLen: minLen, tol: tol, open: map[int]*run{}}
}

// Add records a verified hit.
func (c *Collector) Add(pos, d int) {
	if d <= 0 {
		return
	}
	if r, ok := c.open[d]; ok {
		if pos-r.hi <= c.tol {
			if pos > r.hi {
				r.hi = pos
			}
			return
		}
		c.emit(d, r)
	}
	c.open[d] = &run{pos, pos}

	// Close runs nothing has extended, so the map stays small.
	if pos >= c.nextGC {
		c.nextGC = pos + c.tol
		for dd, rr := range c.open {
			if dd != d && pos-rr.hi > c.tol {
				c.emit(dd, rr)
				delete(c.open, dd)
			}
		}
	}
}

func (c *Collector) emit(d int, r *run) {
	c.pairs = append(c.pairs, Pair{A: r.lo, B: r.lo + d, Len: r.hi - r.lo + c.minLen})
}

// Pairs closes every open run and returns the result, sorted for determinism.
func (c *Collector) Pairs() []Pair {
	for d, r := range c.open {
		c.emit(d, r)
		delete(c.open, d)
	}
	sort.Slice(c.pairs, func(i, j int) bool {
		if c.pairs[i].A != c.pairs[j].A {
			return c.pairs[i].A < c.pairs[j].A
		}
		if c.pairs[i].B != c.pairs[j].B {
			return c.pairs[i].B < c.pairs[j].B
		}
		return c.pairs[i].Len < c.pairs[j].Len
	})
	return c.pairs
}

// Group merges pairs into clusters.
//
// Each pair contributes two intervals. Different pairs find the same airing
// at slightly different starts, so intervals are grouped by overlap rather
// than by start: an interval joins an airing when it covers at least
// minOverlap of the longer of the two.
//
// The comparison is always against the airing's first interval and never
// against a growing extent. Growing it lets a chain of neighbours creep
// across the timeline and weld unrelated fragments together.
func Group(pairs []Pair, minOverlap float64) []Cluster {
	if len(pairs) == 0 {
		return nil
	}

	type item struct {
		start, length, pair int
		side                int // 0 = A, 1 = B
	}
	items := make([]item, 0, len(pairs)*2)
	for i, p := range pairs {
		items = append(items,
			item{p.A, p.Len, i, 0},
			item{p.B, p.Len, i, 1})
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].start != items[j].start {
			return items[i].start < items[j].start
		}
		if items[i].length != items[j].length {
			return items[i].length < items[j].length
		}
		if items[i].pair != items[j].pair {
			return items[i].pair < items[j].pair
		}
		return items[i].side < items[j].side
	})

	// Sweep into airings, anchored on each airing's first interval.
	airing := make([]int, len(items))
	var anchors []int
	var lens [][]int
	cur := -1
	var anchor Occurrence
	for i, it := range items {
		o := Occurrence{it.start, it.length}
		if cur < 0 || !sameAiring(anchor, o, minOverlap) {
			cur++
			anchor = o
			anchors = append(anchors, o.Start)
			lens = append(lens, nil)
		}
		airing[i] = cur
		lens[cur] = append(lens[cur], it.length)
	}

	// Which airing each side of each pair landed in.
	sideAiring := make([][2]int, len(pairs))
	for i := range sideAiring {
		sideAiring[i] = [2]int{-1, -1}
	}
	for i, it := range items {
		sideAiring[it.pair][it.side] = airing[i]
	}

	uf := newUF(len(anchors))
	for _, sa := range sideAiring {
		if sa[0] >= 0 && sa[1] >= 0 && sa[0] != sa[1] {
			uf.join(sa[0], sa[1])
		}
	}

	occOf := func(a int) Occurrence {
		l := append([]int(nil), lens[a]...)
		sort.Ints(l)
		return Occurrence{anchors[a], l[len(l)/2]}
	}

	byRoot := map[int][]Occurrence{}
	for a := range anchors {
		r := uf.root(a)
		byRoot[r] = append(byRoot[r], occOf(a))
	}

	out := make([]Cluster, 0, len(byRoot))
	for _, occ := range byRoot {
		sort.Slice(occ, func(i, j int) bool {
			if occ[i].Start != occ[j].Start {
				return occ[i].Start < occ[j].Start
			}
			return occ[i].Len < occ[j].Len
		})
		occ = disjoint(occ)
		if len(occ) < 2 {
			continue // seen once is not a repeat
		}
		out = append(out, Cluster{Occurrences: occ})
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Occurrences[0].Start < out[j].Occurrences[0].Start
	})
	for i := range out {
		out[i].ID = i + 1
	}
	return out
}

// disjoint keeps the occurrences that do not overlap each other. An airing
// cannot overlap itself, so overlapping entries are the same airing seen
// again - or a self-similar stretch such as a looping music bed, where only
// one position per loop is a real occurrence. Input must be sorted by start.
func disjoint(occ []Occurrence) []Occurrence {
	out := occ[:0]
	end := 0
	for i, o := range occ {
		if i > 0 && o.Start < end {
			continue
		}
		out = append(out, o)
		end = o.End()
	}
	return out
}

// sameAiring reports whether b is the same airing as anchor. Items arrive
// sorted by start, so anchor starts no later than b.
func sameAiring(anchor, b Occurrence, minOverlap float64) bool {
	ov := min(anchor.End(), b.End()) - b.Start
	if ov <= 0 {
		return false
	}
	longer := max(anchor.Len, b.Len)
	if longer == 0 {
		return false
	}
	return float64(ov)/float64(longer) >= minOverlap
}

type uf struct{ p []int }

func newUF(n int) *uf {
	p := make([]int, n)
	for i := range p {
		p[i] = i
	}
	return &uf{p}
}

func (u *uf) root(i int) int {
	for u.p[i] != i {
		u.p[i] = u.p[u.p[i]]
		i = u.p[i]
	}
	return i
}

func (u *uf) join(a, b int) {
	ra, rb := u.root(a), u.root(b)
	if ra != rb {
		u.p[rb] = ra
	}
}
