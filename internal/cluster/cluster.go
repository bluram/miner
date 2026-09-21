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

// Len is the representative length, taken as the shortest occurrence so the
// fragment is not padded with a neighbour.
func (c Cluster) Len() int {
	if len(c.Occurrences) == 0 {
		return 0
	}
	n := c.Occurrences[0].Len
	for _, o := range c.Occurrences[1:] {
		if o.Len < n {
			n = o.Len
		}
	}
	return n
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
// Each pair contributes two intervals. Intervals overlapping by at least
// minOverlap of the shorter one are the same airing and collapse into one
// occurrence; the pair then links the two occurrences it joined, and a
// cluster is a connected component of those links.
func Group(pairs []Pair, minOverlap float64) []Cluster {
	if len(pairs) == 0 {
		return nil
	}

	iv := make([]Occurrence, 0, len(pairs)*2)
	for _, p := range pairs {
		iv = append(iv, Occurrence{p.A, p.Len}, Occurrence{p.B, p.Len})
	}
	sort.Slice(iv, func(i, j int) bool {
		if iv[i].Start != iv[j].Start {
			return iv[i].Start < iv[j].Start
		}
		return iv[i].Len < iv[j].Len
	})

	merged := make([]Occurrence, 0, len(iv))
	for _, o := range iv {
		if n := len(merged); n > 0 && overlaps(merged[n-1], o, minOverlap) {
			merged[n-1] = union(merged[n-1], o)
			continue
		}
		merged = append(merged, o)
	}

	uf := newUF(len(merged))
	for _, p := range pairs {
		a := find(merged, p.A, p.Len)
		b := find(merged, p.B, p.Len)
		if a >= 0 && b >= 0 {
			uf.join(a, b)
		}
	}

	byRoot := map[int][]Occurrence{}
	for i, o := range merged {
		r := uf.root(i)
		byRoot[r] = append(byRoot[r], o)
	}

	out := make([]Cluster, 0, len(byRoot))
	for _, occ := range byRoot {
		if len(occ) < 2 {
			continue // a fragment seen once is not a repeat
		}
		sort.Slice(occ, func(i, j int) bool { return occ[i].Start < occ[j].Start })
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

// overlaps reports whether two intervals cover the same airing.
func overlaps(a, b Occurrence, minOverlap float64) bool {
	lo, hi := max(a.Start, b.Start), min(a.End(), b.End())
	if hi <= lo {
		return false
	}
	shorter := min(a.Len, b.Len)
	if shorter == 0 {
		return false
	}
	return float64(hi-lo)/float64(shorter) >= minOverlap
}

func union(a, b Occurrence) Occurrence {
	s := min(a.Start, b.Start)
	e := max(a.End(), b.End())
	return Occurrence{s, e - s}
}

// find locates the merged occurrence holding an interval.
func find(merged []Occurrence, start, length int) int {
	i := sort.Search(len(merged), func(i int) bool { return merged[i].End() > start })
	for ; i < len(merged) && merged[i].Start < start+length; i++ {
		if merged[i].Start <= start && merged[i].End() >= start+length {
			return i
		}
	}
	return -1
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
