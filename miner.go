// Package miner finds repeated audio in a radio channel's broadcast.
//
// It reads the binarised-MFCC fingerprints the production recorder writes
// beside every mp3, stitches them into one timeline, and reports every
// fragment that airs more than once, grouped into clusters.
//
// It does not decide what a fragment is. Telling adverts from jingles is a
// separate layer built on this output.
package miner

import (
	"fmt"
	"sort"
	"time"

	"miner/internal/cluster"
	"miner/internal/fingerprint"
	"miner/internal/index"
	"miner/internal/match"
	"miner/internal/timeline"
)

// Options controls a run. The zero value is filled in by Defaults.
type Options struct {
	Min      time.Duration // shortest fragment to report
	Max      time.Duration // longest
	MinCount int           // occurrences needed to count as a repeat
	Rate     float64       // similarity threshold

	// Progress, if set, is called as each stage advances. total is 0 when the
	// amount of work is not known up front.
	Progress func(stage string, done, total int)
}

// Defaults fills in anything left at zero.
func (o Options) Defaults() Options {
	if o.Min <= 0 {
		o.Min = 10 * time.Second
	}
	if o.Max <= 0 {
		o.Max = 120 * time.Second
	}
	if o.MinCount < 2 {
		o.MinCount = 2
	}
	if o.Rate <= 0 {
		o.Rate = match.Threshold
	}
	return o
}

// Occurrence is one airing of a fragment.
type Occurrence struct {
	Start  time.Time     `json:"start"`
	End    time.Time     `json:"end"`
	File   string        `json:"file"`
	Offset time.Duration `json:"-"`
	// OffsetSec is where the airing begins inside File.
	OffsetSec float64 `json:"offset_sec"`
}

// Cluster is every airing of one fragment.
type Cluster struct {
	ID          int          `json:"id"`
	DurationSec float64      `json:"duration_sec"`
	Count       int          `json:"count"`
	Occurrences []Occurrence `json:"occurrences"`
}

// Representative is the airing to sample the cluster's sound from.
func (c Cluster) Representative() Occurrence {
	return c.Occurrences[len(c.Occurrences)/2]
}

// Result is one run's output.
type Result struct {
	Channel  string    `json:"channel"`
	From     time.Time `json:"from"`
	To       time.Time `json:"to"`
	Clusters []Cluster `json:"clusters"`
	Skipped  []string  `json:"skipped,omitempty"`
}

// Find reports every fragment of the given fingerprint files that airs more
// than once.
func Find(paths []string, opt Options) (*Result, error) {
	opt = opt.Defaults()
	if opt.Min >= opt.Max {
		return nil, fmt.Errorf("miner: min %v is not shorter than max %v", opt.Min, opt.Max)
	}

	report := opt.Progress
	if report == nil {
		report = func(string, int, int) {}
	}

	tl, err := timeline.Build(paths, func(done, total int) {
		report("load", done, total)
	})
	if err != nil {
		return nil, err
	}
	fr := tl.Frames

	minLen := fingerprint.DurationToFrames(opt.Min)
	maxLen := fingerprint.DurationToFrames(opt.Max)
	quick := fingerprint.DurationToFrames(time.Second)
	step := quick / 2
	shift := 3 * quick / 2

	pairs := searchPairs(tl, minLen, maxLen, quick, step, opt.Rate, report)
	clusters := cluster.Group(pairs, cluster.Options{
		MinOverlap: 0.5,
		Same:       sameFunc(fr, minLen, opt.Rate),
		Verify:     verifyFunc(fr, minLen, quick, shift, opt.Rate),
		Progress: func(done, total int) {
			report("group", done, total)
		},
	})
	report("output", 0, 0)

	res := &Result{Channel: tl.Channel}
	if len(tl.Segs) > 0 {
		res.From = tl.Time(0)
		res.To = tl.Time(tl.Len() - 1)
	}
	for _, s := range tl.Skipped {
		res.Skipped = append(res.Skipped, s.Path)
	}

	for _, c := range clusters {
		n := c.Len()
		if n < minLen || n > maxLen || len(c.Occurrences) < opt.MinCount {
			continue
		}
		out := Cluster{
			DurationSec: fingerprint.FramesToDuration(n).Seconds(),
			Count:       len(c.Occurrences),
		}
		for _, o := range c.Occurrences {
			ts, file, off, err := tl.At(o.Start)
			if err != nil {
				continue
			}
			d := fingerprint.FramesToDuration(o.Len)
			out.Occurrences = append(out.Occurrences, Occurrence{
				Start:     ts,
				End:       ts.Add(d),
				File:      file,
				Offset:    fingerprint.FramesToDuration(off),
				OffsetSec: fingerprint.FramesToDuration(off).Seconds(),
			})
		}
		if len(out.Occurrences) < opt.MinCount {
			continue
		}
		res.Clusters = append(res.Clusters, out)
	}

	sort.Slice(res.Clusters, func(i, j int) bool {
		return res.Clusters[i].Occurrences[0].Start.Before(res.Clusters[j].Occurrences[0].Start)
	})
	for i := range res.Clusters {
		res.Clusters[i].ID = i + 1
	}
	return res, nil
}

// searchPairs finds every fragment-pair: verify a seed from the index, grow
// it to its real extent, then skip later seeds already covered on that
// diagonal.
func searchPairs(tl *timeline.Timeline, minLen, maxLen, quick, step int, rate float64,
	report func(stage string, done, total int)) []cluster.Pair {
	fr := tl.Frames
	report("index", 0, 0)
	ix := index.Build(fr)
	covered := map[int]int{}
	var pairs []cluster.Pair
	buf := make([]int, 0, index.MaxBucket)

	last := len(fr) - minLen
	for pos := 0; pos <= last; pos++ {
		if pos&0xFFFF == 0 {
			report("search", pos, last)
		}
		buf = ix.Lookup(fr, pos, buf[:0])
		for _, q := range buf {
			if q <= pos || q-pos < minLen || q+minLen > len(fr) {
				continue
			}
			d := q - pos
			if end, ok := covered[d]; ok && pos < end {
				continue
			}
			if match.Rate(fr[pos:pos+quick], fr[q:q+quick]) < rate {
				continue
			}
			if match.Rate(fr[pos:pos+minLen], fr[q:q+minLen]) < rate {
				continue
			}
			if !tl.Contiguous(pos, pos+minLen) || !tl.Contiguous(q, q+minLen) {
				continue
			}
			lo, length := match.Extend(fr, pos, q, minLen, step, maxLen, rate)
			if length < minLen {
				continue
			}
			covered[d] = lo + length
			pairs = append(pairs, cluster.Pair{A: lo, B: lo + d, Len: length})
		}
	}
	return pairs
}

// sameFunc checks a candidate against a cluster's seed, using the alignment
// the linking pair proved.
func sameFunc(fr []uint16, minLen int, rate float64) func(a, b cluster.Occurrence, shift int) bool {
	return func(a, b cluster.Occurrence, shift int) bool {
		n := min(a.Len, b.Len)
		bs := b.Start + shift
		if n < minLen || a.Start < 0 || bs < 0 || a.Start+n > len(fr) || bs+n > len(fr) {
			return false
		}
		if lo, hi := min(a.Len, b.Len), max(a.Len, b.Len); float64(hi)/float64(lo) > maxLenRatio {
			return false
		}
		return match.Rate(fr[a.Start:a.Start+n], fr[bs:bs+n]) >= rate
	}
}

// verifyFunc re-checks a member against its cluster's representative, finding
// the alignment itself: shifts carried from the seed drift on a big cluster.
func verifyFunc(fr []uint16, minLen, quick, maxShift int, rate float64) func(rep, o cluster.Occurrence) bool {
	return func(rep, o cluster.Occurrence) bool {
		n := min(rep.Len, o.Len)
		if n < minLen {
			return false
		}
		if lo, hi := min(rep.Len, o.Len), max(rep.Len, o.Len); float64(hi)/float64(lo) > maxLenRatio {
			return false
		}
		win := min(n, 2*quick)
		if rep.Start < 0 || rep.Start+win > len(fr) {
			return false
		}
		best, bestShift := -1.0, 0
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
		return match.Rate(fr[rep.Start:rep.Start+n], fr[st:st+n]) >= rate
	}
}

// maxLenRatio is how far two readings of one fragment may differ in length.
const maxLenRatio = 1.5

// Glob lists one channel's fingerprint files in a directory.
func Glob(dir string) ([]string, error) { return timeline.Glob(dir) }
