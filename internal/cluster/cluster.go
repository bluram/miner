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

const (
	// minLinkRatio is how much of the longer airing a pair must cover before
	// it may join two airings into one cluster.
	minLinkRatio = 0.7

	// minCover is how much of a cluster a longer one must explain before the
	// shorter is dropped as a piece of it.
	minCover = 0.8
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
// Options configures Group.
type Options struct {
	// MinOverlap is how much of the longer interval two readings must share
	// to count as the same airing.
	MinOverlap float64

	// Same reports whether two airings are the same audio, given the shift to
	// apply to b: a.Start+i lines up with b.Start+Shift+i. Group uses it while
	// growing a cluster, where the shift comes free from the pair that
	// proposed the link. A nil Same accepts every proposal, which is only
	// safe on small inputs.
	Same func(a, b Occurrence, shift int) bool

	// Progress, if set, is called as airings are assigned to clusters.
	// Counting seeds instead would be useless: most are already absorbed by
	// an earlier cluster and skipped instantly.
	Progress func(done, total int)

	// Verify re-checks a finished cluster's members against its
	// representative, and must find the alignment itself. Shifts accumulate
	// along the path from the seed, so on a large cluster they drift too far
	// to trust. A nil Verify falls back to Same with the accumulated shift.
	Verify func(rep, o Occurrence) bool
}

// Group turns pairs into clusters of repeated audio.
func Group(pairs []Pair, opt Options) []Cluster {
	minOverlap, same := opt.MinOverlap, opt.Same
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
	// Longest first at a given start: the anchor should be the best-supported
	// reading of an airing. Anchoring on the shortest lets a truncated
	// interval win and pushes the full-length ones into airings of their own.
	sort.Slice(items, func(i, j int) bool {
		if items[i].start != items[j].start {
			return items[i].start < items[j].start
		}
		if items[i].length != items[j].length {
			return items[i].length > items[j].length
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

	repLen := make([]int, len(anchors))
	for a := range anchors {
		l := append([]int(nil), lens[a]...)
		sort.Ints(l)
		repLen[a] = l[len(l)/2]
	}
	occ := make([]Occurrence, len(anchors))
	for a := range anchors {
		occ[a] = Occurrence{anchors[a], repLen[a]}
	}

	// Pairs only propose which airings might belong together. Treating them
	// as proof and taking the transitive closure chains 9-second fragments to
	// 40-second ones a step at a time: each link passes, the closure is
	// nonsense. So every candidate is checked against the cluster's own
	// representative instead of against its neighbour.
	// An edge carries the alignment the pair proved, so a candidate never has
	// to be re-aligned by searching: the peak is about one frame wide and a
	// coarse scan misses it.
	adj := make([][]edge, len(anchors))
	for i, sa := range sideAiring {
		a, b := sa[0], sa[1]
		if a < 0 || b < 0 || a == b {
			continue
		}
		if longer := max(repLen[a], repLen[b]); longer > 0 &&
			float64(pairs[i].Len)/float64(longer) < minLinkRatio {
			continue
		}
		// pairs[i].A+t aligns with pairs[i].B+t, so relative to the anchors
		// the offset from a to b is this.
		off := (pairs[i].B - anchors[b]) - (pairs[i].A - anchors[a])
		adj[a] = append(adj[a], edge{b, off})
		adj[b] = append(adj[b], edge{a, -off})
	}
	for a := range adj {
		sort.Slice(adj[a], func(i, j int) bool {
			if adj[a][i].to != adj[a][j].to {
				return adj[a][i].to < adj[a][j].to
			}
			return adj[a][i].off < adj[a][j].off
		})
	}

	// Leaders first: the best-connected airing is the most reliable
	// representative of its fragment.
	order := make([]int, len(anchors))
	for i := range order {
		order[i] = i
	}
	sort.Slice(order, func(i, j int) bool {
		a, b := order[i], order[j]
		if len(adj[a]) != len(adj[b]) {
			return len(adj[a]) > len(adj[b])
		}
		return anchors[a] < anchors[b]
	})

	taken := make([]bool, len(anchors))
	var groups [][]Occurrence
	var queue []edge
	type member struct {
		occ Occurrence
		off int // alignment relative to the seed
	}
	var members []member
	assigned := 0
	for _, seed := range order {
		if taken[seed] || len(adj[seed]) == 0 {
			continue
		}
		taken[seed] = true
		assigned++
		if opt.Progress != nil {
			opt.Progress(assigned, len(order))
		}
		members = append(members[:0], member{occ[seed], 0})
		queue = append(queue[:0], adj[seed]...)
		for len(queue) > 0 {
			e := queue[0]
			queue = queue[1:]
			if taken[e.to] {
				continue
			}
			if same != nil && !same(occ[seed], occ[e.to], e.off) {
				continue
			}
			taken[e.to] = true
			assigned++
			members = append(members, member{occ[e.to], e.off})
			// Offsets compose along the path from the seed.
			for _, n := range adj[e.to] {
				queue = append(queue, edge{n.to, e.off + n.off})
			}
		}

		// Members were checked against the seed, but the cluster's advertised
		// length is its median occurrence. Those can differ, so re-check each
		// member against that representative and drop the ones that do not
		// match what the cluster claims to be. Offsets are known, so no
		// search is needed.
		keep := make([]Occurrence, 0, len(members))
		if (same == nil && opt.Verify == nil) || len(members) < 3 {
			for _, m := range members {
				keep = append(keep, m.occ)
			}
		} else {
			byLen := make([]int, len(members))
			for i := range members {
				byLen[i] = i
			}
			sort.Slice(byLen, func(i, j int) bool {
				if members[byLen[i]].occ.Len != members[byLen[j]].occ.Len {
					return members[byLen[i]].occ.Len < members[byLen[j]].occ.Len
				}
				return members[byLen[i]].occ.Start < members[byLen[j]].occ.Start
			})
			rep := members[byLen[len(byLen)/2]]
			for _, m := range members {
				switch {
				case m.occ == rep.occ:
				case opt.Verify != nil:
					if !opt.Verify(rep.occ, m.occ) {
						continue
					}
				case !same(rep.occ, m.occ, m.off-rep.off):
					continue
				}
				keep = append(keep, m.occ)
			}
		}
		groups = append(groups, keep)
	}

	out := make([]Cluster, 0, len(groups))
	for _, members := range groups {
		sort.Slice(members, func(i, j int) bool {
			if members[i].Start != members[j].Start {
				return members[i].Start < members[j].Start
			}
			return members[i].Len > members[j].Len
		})
		members = disjoint(members)
		if len(members) < 2 {
			continue // seen once is not a repeat
		}
		out = append(out, Cluster{Occurrences: members})
	}
	out = Subsume(out, minCover)
	sort.Slice(out, func(i, j int) bool {
		return out[i].Occurrences[0].Start < out[j].Occurrences[0].Start
	})
	for i := range out {
		out[i].ID = i + 1
	}
	return out
}

// Subsume drops clusters whose occurrences are already explained by a longer
// cluster.
//
// A 10-second piece of a 34-second advert repeats exactly as often as the
// advert does, so it forms a perfectly good cluster of its own. Reporting
// both is fragmentation: the longest reading of a stretch of airtime is the
// one worth keeping.
//
// A cluster goes when at least minCover of its occurrences are at least
// minCover covered by occurrences of longer clusters.
func Subsume(clusters []Cluster, minCover float64) []Cluster {
	if len(clusters) < 2 {
		return clusters
	}

	var spans []span
	for i, c := range clusters {
		l := c.Len()
		for _, o := range c.Occurrences {
			spans = append(spans, span{o.Start, o.End(), i, l})
		}
	}
	sort.Slice(spans, func(i, j int) bool {
		if spans[i].start != spans[j].start {
			return spans[i].start < spans[j].start
		}
		if spans[i].length != spans[j].length {
			return spans[i].length > spans[j].length
		}
		return spans[i].cluster < spans[j].cluster
	})

	total := make([]int, len(clusters))
	buried := make([]int, len(clusters))
	for i, sp := range spans {
		total[sp.cluster]++
		own := sp.end - sp.start
		if own <= 0 {
			continue
		}
		cover := 0
		// Overlapping spans are adjacent in start order, so a short walk
		// either way finds them all.
		for j := i - 1; j >= 0 && spans[j].end > sp.start; j-- {
			cover += overlapBy(spans[j], sp)
		}
		for j := i + 1; j < len(spans) && spans[j].start < sp.end; j++ {
			cover += overlapBy(spans[j], sp)
		}
		if float64(cover)/float64(own) >= minCover {
			buried[sp.cluster]++
		}
	}

	out := clusters[:0]
	for i, c := range clusters {
		if total[i] > 0 && float64(buried[i])/float64(total[i]) >= minCover {
			continue
		}
		out = append(out, c)
	}
	return out
}

// span is one occurrence, tagged with its cluster and that cluster's length.
type span struct {
	start, end, cluster, length int
}

// overlapBy is how much of sp the longer span other covers.
func overlapBy(other, sp span) int {
	if other.cluster == sp.cluster || other.length <= sp.length {
		return 0
	}
	lo, hi := max(other.start, sp.start), min(other.end, sp.end)
	if hi <= lo {
		return 0
	}
	return hi - lo
}

// edge is a candidate link and the alignment its pair proved.
type edge struct {
	to  int
	off int
}

// disjoint keeps the occurrences that do not overlap each other. An airing
// cannot overlap itself, so overlapping entries are the same airing seen
// again - or a self-similar stretch such as a looping music bed, where only
// one position per loop is a real occurrence.
//
// Input must be sorted by start, longest first, so a truncated reading of an
// airing loses to the full-length one it overlaps.
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
