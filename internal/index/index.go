// Package index finds candidate repeat positions without comparing
// everything to everything.
//
// Real repeats are not bit-identical: measured on enerji_fm, two airings of
// the same audio agree on about 89% of bits, and only 29% of frames survive
// untouched. So a key cannot hash a whole window. It hashes the three most
// stable rows - the low cepstral coefficients, which err 4-6% of the time
// against 14% for the highest - sampled across a second.
//
// That key survives a repeat about 20% of the time. Entries are stored every
// Stride frames while lookups probe every frame, so a 10-second repeat offers
// ~33 chances and is found with ~99.9% probability.
package index

import (
	"slices"

	"miner/internal/fingerprint"
)

// Key shape. 30 bits over 91 frames, just under a second.
const (
	Samples = 10 // frames per key
	Step    = 10 // frames between them
	Rows    = 3  // stable rows used, counting from row 0
	Span    = (Samples-1)*Step + 1
	KeyBits = Samples * Rows
)

// Stride is the default gap between stored entries. Lookups probe every
// frame, so only one side of a repeat needs to land on a stride boundary.
const Stride = 32

// TargetChances is how many stored entries the shortest fragment should get.
// The key survives a repeat about a fifth of the time, so a fragment needs
// many chances for at least one to land; a 3-second fragment at the default
// stride gets only ten, and occasionally all of them fail.
const TargetChances = 32

// StrideFor spaces entries so a fragment of minLen frames still gets about
// TargetChances of them. It never exceeds the default, so longer minimums
// behave exactly as before.
func StrideFor(minLen int) int {
	s := minLen / TargetChances
	if s > Stride {
		return Stride
	}
	if s < 1 {
		return 1
	}
	return s
}

// MaxBucket caps how many candidates one key may return, so silence and dead
// air cannot dominate the run. A fuller bucket is sampled evenly rather than
// dropped: the keys with the most entries belong to the most repeated audio,
// and discarding them was losing the links that join an advert's hundredth
// airing to its first.
const MaxBucket = 64

// Index maps keys to timeline positions. Entries are key<<32|pos in one
// sorted slice: 8 bytes each, no map overhead, and equal keys end up
// adjacent.
type Index struct {
	entries []uint64
	n       int
	stride  int
}

// Stride reports the spacing this index was built with.
func (ix *Index) Stride() int { return ix.stride }

// Key computes the key at pos, reporting false if it is degenerate.
// An all-zero key means silence, which matches everything and means nothing.
func Key(frames []uint16, pos int) (uint32, bool) {
	if pos < 0 || pos+Span > len(frames) {
		return 0, false
	}
	var k uint32
	for s := 0; s < Samples; s++ {
		f := frames[pos+s*Step]
		for r := 0; r < Rows; r++ {
			k = k<<1 | uint32((f>>(fingerprint.Rows-1-r))&1)
		}
	}
	return k, k != 0
}

// Build indexes every stride-th position. A stride below 1 uses the default.
func Build(frames []uint16, stride int) *Index {
	if stride < 1 {
		stride = Stride
	}
	last := len(frames) - Span
	if last < 0 {
		return &Index{n: len(frames), stride: stride}
	}
	ix := &Index{
		entries: make([]uint64, 0, last/stride+1),
		n:       len(frames),
		stride:  stride,
	}
	for pos := 0; pos <= last; pos += stride {
		if k, ok := Key(frames, pos); ok {
			ix.entries = append(ix.entries, uint64(k)<<32|uint64(uint32(pos)))
		}
	}
	slices.Sort(ix.entries)
	return ix
}

// Len is the number of stored entries.
func (ix *Index) Len() int { return len(ix.entries) }

// Bytes is the index's memory footprint.
func (ix *Index) Bytes() int { return len(ix.entries) * 8 }

// Lookup appends the positions stored under the key at pos to dst and returns
// it. Buckets longer than MaxBucket are dropped rather than truncated: a key
// that common carries no information.
func (ix *Index) Lookup(frames []uint16, pos int, dst []int) []int {
	k, ok := Key(frames, pos)
	if !ok {
		return dst
	}
	return ix.lookupKey(k, dst)
}

func (ix *Index) lookupKey(k uint32, dst []int) []int {
	lo := uint64(k) << 32
	i, _ := slices.BinarySearch(ix.entries, lo)
	j := i
	for j < len(ix.entries) && uint32(ix.entries[j]>>32) == k {
		j++
	}
	n := j - i
	if n <= MaxBucket {
		for ; i < j; i++ {
			dst = append(dst, int(uint32(ix.entries[i])))
		}
		return dst
	}
	// Spread the sample across the whole bucket rather than taking a prefix,
	// so candidates are not all clustered at one end of the period.
	step := n / MaxBucket
	for c := 0; c < MaxBucket; c++ {
		dst = append(dst, int(uint32(ix.entries[i+c*step])))
	}
	return dst
}

// BucketSize reports how many entries share the key at pos, for diagnostics.
func (ix *Index) BucketSize(frames []uint16, pos int) int {
	k, ok := Key(frames, pos)
	if !ok {
		return 0
	}
	lo := uint64(k) << 32
	i, _ := slices.BinarySearch(ix.entries, lo)
	j := i
	for j < len(ix.entries) && uint32(ix.entries[j]>>32) == k {
		j++
	}
	return j - i
}
