package main

import (
	"fmt"
	"os"
	"runtime"
	"sort"
	"time"

	"miner/internal/cluster"
	"miner/internal/fingerprint"
	"miner/internal/index"
	"miner/internal/match"
	"miner/internal/timeline"
)

func mb(b uint64) float64 { return float64(b) / 1e6 }

func main() {
	hours := 8
	if len(os.Args) > 1 {
		if os.Args[1] == "month" {
			hours = 0
		} else {
			fmt.Sscanf(os.Args[1], "%d", &hours)
		}
	}
	paths, err := timeline.Glob("testdata/enerji_fm-2026-08")
	if err != nil {
		panic(err)
	}
	sort.Strings(paths)
	if hours > 0 {
		paths = paths[:hours]
	}

	t0 := time.Now()
	tl, err := timeline.Build(paths)
	if err != nil {
		panic(err)
	}
	fr := tl.Frames
	fmt.Printf("timeline  %d frames (%v)\n", len(fr), fingerprint.FramesToDuration(len(fr)).Round(time.Minute))
	ix := index.Build(fr)
	fmt.Printf("index     %d entries, %.0f MB\n", ix.Len(), float64(ix.Bytes())/1e6)

	fps := fingerprint.DurationToFrames(time.Second)
	minLen := fingerprint.DurationToFrames(10 * time.Second)
	maxLen := fingerprint.DurationToFrames(120 * time.Second)
	quick := 1 * fps

	t2 := time.Now()
	// One pair per fragment-pair: verify a seed, then grow it to its real
	// extent and skip later seeds already covered on that diagonal.
	covered := map[int]int{}
	var pairs []cluster.Pair
	buf := make([]int, 0, index.MaxBucket)
	step := fps / 2
	for pos := 0; pos+minLen <= len(fr); pos++ {
		buf = ix.Lookup(fr, pos, buf[:0])
		for _, q := range buf {
			if q <= pos || q-pos < minLen || q+minLen > len(fr) {
				continue
			}
			d := q - pos
			if end, ok := covered[d]; ok && pos < end {
				continue
			}
			if match.Rate(fr[pos:pos+quick], fr[q:q+quick]) < 0.75 {
				continue
			}
			if match.Rate(fr[pos:pos+minLen], fr[q:q+minLen]) < match.Threshold {
				continue
			}
			if !tl.Contiguous(pos, pos+minLen) || !tl.Contiguous(q, q+minLen) {
				continue
			}
			lo, length := match.Extend(fr, pos, q, minLen, step, maxLen, match.Threshold)
			if length < minLen {
				continue
			}
			covered[d] = lo + length
			pairs = append(pairs, cluster.Pair{A: lo, B: lo + d, Len: length})
		}
	}
	fmt.Printf("search    %v   pairs %d\n", time.Since(t2).Round(time.Second), len(pairs))

	t3 := time.Now()
	// A candidate joins only if it matches the cluster's representative
	// directly, over the representative's own length.
	var calls, rejBounds, rejRatio, rejRate, accepted int
	same := func(a, b cluster.Occurrence, shift int) bool {
		calls++
		n := min(a.Len, b.Len)
		bs := b.Start + shift
		if n < minLen || a.Start < 0 || bs < 0 || a.Start+n > len(fr) || bs+n > len(fr) {
			rejBounds++
			return false
		}
		if lo, hi := min(a.Len, b.Len), max(a.Len, b.Len); float64(hi)/float64(lo) > 1.5 {
			rejRatio++
			return false
		}
		if match.Rate(fr[a.Start:a.Start+n], fr[bs:bs+n]) < match.Threshold {
			rejRate++
			return false
		}
		accepted++
		return true
	}
	// Pruning finds its own alignment: the shift carried from the seed drifts
	// on a large cluster.
	var vCalls, vAccepted int
	verify := func(rep, o cluster.Occurrence) bool {
		vCalls++
		n := min(rep.Len, o.Len)
		if n < minLen {
			return false
		}
		if lo, hi := min(rep.Len, o.Len), max(rep.Len, o.Len); float64(hi)/float64(lo) > 1.5 {
			return false
		}
		win := min(n, 2*fps)
		if rep.Start+win > len(fr) {
			return false
		}
		bestShift, best := 0, -1.0
		maxShift := 3 * fps / 2
		for sh := -maxShift; sh <= maxShift; sh++ {
			st := o.Start + sh
			if st < 0 || st+win > len(fr) {
				continue
			}
			if r := match.Rate(fr[rep.Start:rep.Start+win], fr[st:st+win]); r > best {
				best, bestShift = r, sh
			}
		}
		st := o.Start + bestShift
		if st < 0 || st+n > len(fr) || rep.Start+n > len(fr) {
			return false
		}
		if match.Rate(fr[rep.Start:rep.Start+n], fr[st:st+n]) < match.Threshold {
			return false
		}
		vAccepted++
		return true
	}
	clusters := cluster.Group(pairs, cluster.Options{MinOverlap: 0.5, Same: same, Verify: verify})
	fmt.Printf("group     %v   clusters %d\n", time.Since(t3).Round(time.Second), len(clusters))
	fmt.Printf("  same(): %d calls  accepted %d  rej bounds %d  ratio %d  rate %d\n",
		calls, accepted, rejBounds, rejRatio, rejRate)
	fmt.Printf("  verify(): %d calls  accepted %d\n", vCalls, vAccepted)

	var kept []cluster.Cluster
	for _, c := range clusters {
		if n := c.Len(); n >= minLen && n <= maxLen {
			kept = append(kept, c)
		}
	}
	fmt.Printf("in 10-120s range: %d clusters\n", len(kept))

	sort.Slice(kept, func(i, j int) bool {
		return len(kept[i].Occurrences) > len(kept[j].Occurrences)
	})
	fmt.Printf("\nmost repeated:\n")
	for i, c := range kept {
		if i >= 12 {
			break
		}
		fmt.Printf("  %3dx  %5.1fs  first %s\n", len(c.Occurrences),
			fingerprint.FramesToDuration(c.Len()).Seconds(),
			tl.Time(c.Occurrences[0].Start).Format("01-02 15:04:05"))
	}

	// Detail on the biggest cluster, plus cuts to listen to.
	if len(kept) > 0 {
		c := kept[0]
		fmt.Printf("\nbiggest cluster: %dx %.1fs\n", len(c.Occurrences),
			fingerprint.FramesToDuration(c.Len()).Seconds())
		fmt.Printf("  first 8 airings and the gap to the previous one:\n")
		for i, o := range c.Occurrences {
			if i >= 8 {
				break
			}
			gap := ""
			if i > 0 {
				gap = fingerprint.FramesToDuration(o.Start - c.Occurrences[i-1].Start).Round(time.Second).String()
			}
			fmt.Printf("    %s  %5.1fs  %s\n", tl.Time(o.Start).Format("01-02 15:04:05"),
				fingerprint.FramesToDuration(o.Len).Seconds(), gap)
		}
		os.MkdirAll("out/check", 0o755)
		for i, o := range c.Occurrences {
			if i >= 3 {
				break
			}
			_, f, off, _ := tl.At(o.Start)
			fmt.Printf("  cut %d: %s +%.1fs len %.1fs\n", i, f,
				fingerprint.FramesToDuration(off).Seconds(),
				fingerprint.FramesToDuration(o.Len).Seconds())
		}
	}

	// Why does the biggest cluster mix lengths? Find the pairs that bridge
	// occurrences of very different length.
	if len(kept) > 0 {
		big := kept[0]
		hist := map[int]int{}
		for _, o := range big.Occurrences {
			hist[int(fingerprint.FramesToDuration(o.Len).Seconds())]++
		}
		var ks []int
		for k := range hist {
			ks = append(ks, k)
		}
		sort.Ints(ks)
		fmt.Printf("\nlength histogram of biggest cluster:\n")
		for _, k := range ks {
			fmt.Printf("  %3ds  x%d\n", k, hist[k])
		}

		find := func(pos int) *cluster.Occurrence {
			i := sort.Search(len(big.Occurrences), func(i int) bool {
				return big.Occurrences[i].End() > pos
			})
			if i < len(big.Occurrences) && big.Occurrences[i].Start <= pos {
				return &big.Occurrences[i]
			}
			return nil
		}
		fmt.Printf("\nbridge pairs (linking occurrences of very different length):\n")
		shown := 0
		for _, pr := range pairs {
			oa, ob := find(pr.A), find(pr.B)
			if oa == nil || ob == nil {
				continue
			}
			lo, hi := oa.Len, ob.Len
			if lo > hi {
				lo, hi = hi, lo
			}
			if float64(hi)/float64(lo) < 2 {
				continue
			}
			shown++
			if shown > 10 {
				continue
			}
			fmt.Printf("  pair len %5.1fs   A %s in occ %.1fs   B %s in occ %.1fs\n",
				fingerprint.FramesToDuration(pr.Len).Seconds(),
				tl.Time(pr.A).Format("15:04:05"), fingerprint.FramesToDuration(oa.Len).Seconds(),
				tl.Time(pr.B).Format("15:04:05"), fingerprint.FramesToDuration(ob.Len).Seconds())
		}
		fmt.Printf("  total bridge pairs: %d\n", shown)
	}

	// Ground truth: brute-force the biggest cluster's representative against
	// the whole timeline, so the cluster count can be checked against it.
	for ci := 0; ci < 3 && ci < len(kept); ci++ {
		c := kept[ci]
		// Use a full-length occurrence, not the first one, which may be a
		// truncated reading.
		rep := c.Occurrences[0]
		for _, o := range c.Occurrences {
			if o.Len == c.Len() {
				rep = o
				break
			}
		}
		n := rep.Len
		t4 := time.Now()
		var found []int
		nearMiss := 0
		last := -n
		for i := 0; i+n <= len(fr); i++ {
			if i-last < n {
				continue
			}
			if match.Rate(fr[rep.Start:rep.Start+quick], fr[i:i+quick]) < 0.60 {
				continue
			}
			r := match.Rate(fr[rep.Start:rep.Start+n], fr[i:i+n])
			if r < match.Threshold {
				if r >= 0.70 {
					nearMiss++
				}
				continue
			}
			found = append(found, i)
			last = i
		}
		lh := map[int]int{}
		for _, o := range c.Occurrences {
			lh[int(fingerprint.FramesToDuration(o.Len).Seconds())]++
		}
		var lks []int
		for k := range lh {
			lks = append(lks, k)
		}
		sort.Ints(lks)
		fmt.Printf("\ncluster %d occurrence lengths:", c.ID)
		for _, k := range lks {
			fmt.Printf(" %ds:%d", k, lh[k])
		}
		fmt.Println()
		fmt.Printf("ground truth for cluster %d (%.1fs rep at %s):\n", c.ID,
			fingerprint.FramesToDuration(n).Seconds(), tl.Time(rep.Start).Format("15:04:05"))
		fmt.Printf("  brute force found %d airings in %v, cluster reported %d  (near misses 0.70-0.80: %d)\n",
			len(found), time.Since(t4).Round(time.Second), len(c.Occurrences), nearMiss)
		have := map[int]bool{}
		for _, o := range c.Occurrences {
			have[o.Start] = true
		}
		missed := 0
		for _, f := range found {
			near := false
			for _, o := range c.Occurrences {
				if f < o.End() && f+n > o.Start {
					near = true
				}
			}
			if !near {
				if missed < 8 {
					fmt.Printf("  missed airing at %s\n", tl.Time(f).Format("15:04:05"))
				}
				missed++
			}
		}
		fmt.Printf("  airings the cluster missed: %d\n", missed)

		// Are the missed airings in another cluster, or lost entirely?
		inOther, lost := 0, 0
		otherIDs := map[int]int{}
		for _, f := range found {
			near := false
			for _, o := range c.Occurrences {
				if f < o.End() && f+n > o.Start {
					near = true
				}
			}
			if near {
				continue
			}
			where := -1
			for _, oc := range kept {
				if oc.ID == c.ID {
					continue
				}
				for _, o := range oc.Occurrences {
					if f < o.End() && f+n > o.Start {
						where = oc.ID
					}
				}
			}
			if where >= 0 {
				inOther++
				otherIDs[where]++
			} else {
				lost++
			}
		}
		fmt.Printf("  of those: in another cluster %d, not in any cluster %d\n", inOther, lost)

		// For the airings that reached no cluster at all, did the search even
		// produce a pair for them?
		var lostPos []int
		for _, f := range found {
			near := false
			for _, o := range c.Occurrences {
				if f < o.End() && f+n > o.Start {
					near = true
				}
			}
			if near {
				continue
			}
			inAny := false
			for _, oc := range kept {
				if oc.ID == c.ID {
					continue
				}
				for _, o := range oc.Occurrences {
					if f < o.End() && f+n > o.Start {
						inAny = true
					}
				}
			}
			if !inAny {
				lostPos = append(lostPos, f)
			}
		}
		// Distinguish the stages: a cluster removed by the 10-120s filter still
		// existed after grouping.
		inAllClusters, inKept := 0, 0
		for _, f := range found {
			near := false
			for _, o := range c.Occurrences {
				if f < o.End() && f+n > o.Start {
					near = true
				}
			}
			if near {
				inAllClusters++
				inKept++
				continue
			}
			hitAll, hitKept := false, false
			for _, oc := range clusters {
				for _, o := range oc.Occurrences {
					if f < o.End() && f+n > o.Start {
						hitAll = true
					}
				}
			}
			for _, oc := range kept {
				if oc.ID == c.ID {
					continue
				}
				for _, o := range oc.Occurrences {
					if f < o.End() && f+n > o.Start {
						hitKept = true
					}
				}
			}
			if hitAll {
				inAllClusters++
			}
			if hitKept {
				inKept++
			}
		}
		fmt.Printf("  of %d known airings: in some cluster %d, surviving the 10-120s filter %d\n",
			len(found), inAllClusters, inKept)

		fmt.Printf("\n  pairs involving each airing that reached no cluster:\n")
		for i, f := range lostPos {
			if i >= 10 {
				break
			}
			np := 0
			for _, pr := range pairs {
				if (pr.A > f-quick && pr.A < f+quick) || (pr.B > f-quick && pr.B < f+quick) {
					np++
				}
			}
			fmt.Printf("    %s  pairs %d\n", tl.Time(f).Format("15:04:05"), np)
		}
		if len(otherIDs) > 0 {
			fmt.Printf("  spread across %d other clusters:\n", len(otherIDs))
			type kv struct{ id, n int }
			var kvs []kv
			for id, cnt := range otherIDs {
				kvs = append(kvs, kv{id, cnt})
			}
			sort.Slice(kvs, func(i, j int) bool { return kvs[i].n > kvs[j].n })
			for i, e := range kvs {
				if i >= 5 {
					break
				}
				for _, oc := range kept {
					if oc.ID == e.id {
						fmt.Printf("    cluster %d: %d airings there, %dx %.1fs\n",
							e.id, e.n, len(oc.Occurrences), fingerprint.FramesToDuration(oc.Len()).Seconds())
					}
				}
			}
		}

		// Do pairs exist between the known airings at all?
		nearFound := func(pos int) int {
			for i, f := range found {
				if pos > f-quick && pos < f+quick {
					return i
				}
			}
			return -1
		}
		linked := map[int]bool{}
		var both, oneSide int
		for _, pr := range pairs {
			ia, ib := nearFound(pr.A), nearFound(pr.B)
			if ia >= 0 && ib >= 0 {
				both++
				linked[ia] = true
				linked[ib] = true
			} else if ia >= 0 || ib >= 0 {
				oneSide++
			}
		}
		fmt.Printf("  pairs joining two known airings: %d\n", both)
		fmt.Printf("  pairs touching exactly one:      %d\n", oneSide)
		fmt.Printf("  known airings appearing in any pair: %d of %d\n", len(linked), len(found))

		// Best match of each occurrence against the representative, allowing
		// for start jitter.
		band := map[string]int{}
		shiftMax := 3 * quick
		for _, o := range c.Occurrences {
			best := 0.0
			for sh := -shiftMax; sh <= shiftMax; sh++ {
				st := o.Start + sh
				if st < 0 || st+n > len(fr) {
					continue
				}
				if r := match.Rate(fr[rep.Start:rep.Start+n], fr[st:st+n]); r > best {
					best = r
				}
			}
			switch {
			case best >= 0.80:
				band[">=0.80"]++
			case best >= 0.70:
				band["0.70-0.80"]++
			case best >= 0.60:
				band["0.60-0.70"]++
			default:
				band["<0.60"]++
			}
		}
		fmt.Printf("  occurrences vs representative (best within +/-3s):")
		for _, k := range []string{">=0.80", "0.70-0.80", "0.60-0.70", "<0.60"} {
			if band[k] > 0 {
				fmt.Printf("  %s:%d", k, band[k])
			}
		}
		fmt.Println()
	}

	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	fmt.Printf("\ntotal %v   heap %.0f MB\n", time.Since(t0).Round(time.Second), mb(m.HeapAlloc))
}
