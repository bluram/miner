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

// Threshold is production's accept level, used for both the overall rate and
// the weakest second.
const Threshold = 0.8

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
