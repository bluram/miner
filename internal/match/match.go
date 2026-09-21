// Package match scores how alike two runs of fingerprint frames are.
//
// Production correlates the +/-1 forms of two fingerprints and rescales with
// (c+dd)/(2*dd), dd being the cell count. With +/-1 cells that correlation is
// agreements minus disagreements, so the rescaled score is just the fraction
// of agreeing bits - here one XOR and a popcount per frame.
package match

import (
	"math"
	"math/bits"
	"time"

	"miner/internal/fingerprint"
)

// Bits per frame.
const Bits = fingerprint.Rows

// Threshold is the accept level for both the overall rate and the weakest
// second.
//
// Production uses 0.8, but it compares a clean uploaded advert against a
// recording. Every comparison here is recording against recording, so both
// sides carry encoding noise and genuine repeats land anywhere from 0.70 to
// 0.90. Unrelated stretches average 0.50 and stayed under 0.70 across 200
// sampled pairs, so the two populations still separate cleanly.
const Threshold = 0.70

// framesPerSecond for the per-second breakdown.
var framesPerSecond = fingerprint.DurationToFrames(time.Second)

// Hamming counts differing bits across two equal-length runs.
func Hamming(a, b []uint16) int {
	n := min(len(a), len(b))
	h := 0
	for i := 0; i < n; i++ {
		h += bits.OnesCount16(a[i] ^ b[i])
	}
	return h
}

// Rate is the fraction of agreeing bits: 1 for identical, ~0.5 for unrelated.
func Rate(a, b []uint16) float64 {
	n := min(len(a), len(b))
	if n == 0 {
		return 0
	}
	return 1 - float64(Hamming(a[:n], b[:n]))/float64(n*Bits)
}

// Stats is production's view of a candidate: the overall rate plus the
// spread across whole seconds. Min is what stops a loud partial overlap from
// passing on average alone.
type Stats struct {
	Rate    float64
	Min     float64
	Max     float64
	Mean    float64
	Seconds int
}

// OK applies production's rule: overall rate and weakest second both above
// the threshold.
func (s Stats) OK() bool { return s.Rate > Threshold && s.Min > Threshold }

// Compare scores two equal-length runs whole and second by second. Runs
// shorter than a second report Min and Mean equal to Rate.
func Compare(a, b []uint16) Stats {
	n := min(len(a), len(b))
	s := Stats{Rate: Rate(a[:n], b[:n])}
	secs := n / framesPerSecond
	if secs == 0 {
		s.Min, s.Max, s.Mean = s.Rate, s.Rate, s.Rate
		return s
	}
	s.Min, s.Max, s.Seconds = math.Inf(1), math.Inf(-1), secs
	total := 0.0
	for i := 0; i < secs; i++ {
		lo := i * framesPerSecond
		r := Rate(a[lo:lo+framesPerSecond], b[lo:lo+framesPerSecond])
		s.Min = math.Min(s.Min, r)
		s.Max = math.Max(s.Max, r)
		total += r
	}
	s.Mean = total / float64(secs)
	return s
}

// RateAt scores two n-frame windows of the same strip.
func RateAt(frames []uint16, i, j, n int) float64 {
	if i < 0 || j < 0 || n <= 0 || i+n > len(frames) || j+n > len(frames) {
		return 0
	}
	return Rate(frames[i:i+n], frames[j:j+n])
}

// Patience is how many consecutive failing windows Extend tolerates before
// it stops. A single window can dip below threshold inside a genuine repeat,
// and stopping there truncates the fragment.
const Patience = 3

// Extend grows a verified seed to the longest aligned region around it.
//
// Index hits are sparse, so the span of hits on a diagonal understates the
// real fragment. Rather than infer length from hit density, walk outwards in
// step-sized windows while they still match, and keep the furthest window
// that passed.
//
// a and b are the aligned starts, seed the verified length. Returns the start
// on the a side and the length, capped at maxLen.
func Extend(frames []uint16, a, b, seed, step, maxLen int, threshold float64) (int, int) {
	if a < 0 || b < 0 || seed <= 0 || step <= 0 || a+seed > len(frames) || b+seed > len(frames) {
		return a, 0
	}
	d := b - a
	lo, hi := a, a+seed
	bestLo, bestHi := lo, hi

	for miss := 0; miss < Patience; {
		if hi+step > len(frames) || hi+d+step > len(frames) || hi+step-bestLo > maxLen {
			break
		}
		if Rate(frames[hi:hi+step], frames[hi+d:hi+d+step]) > threshold {
			hi += step
			bestHi = hi
			miss = 0
			continue
		}
		hi += step
		miss++
	}
	for miss := 0; miss < Patience; {
		if lo-step < 0 || lo+d-step < 0 || bestHi-(lo-step) > maxLen {
			break
		}
		if Rate(frames[lo-step:lo], frames[lo+d-step:lo+d]) > threshold {
			lo -= step
			bestLo = lo
			miss = 0
			continue
		}
		lo -= step
		miss++
	}
	return bestLo, bestHi - bestLo
}
