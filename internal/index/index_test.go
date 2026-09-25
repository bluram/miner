package index

import (
	"math/rand"
	"path/filepath"
	"sort"
	"testing"

	"miner/internal/timeline"
)

func TestKeyIsStable(t *testing.T) {
	r := rand.New(rand.NewSource(11))
	fr := make([]uint16, Span+10)
	for i := range fr {
		fr[i] = uint16(r.Intn(4095) + 1)
	}
	a, ok := Key(fr, 0)
	if !ok {
		t.Fatal("key rejected")
	}
	if b, _ := Key(fr, 0); a != b {
		t.Error("key is not deterministic")
	}
	if c, _ := Key(fr, 1); a == c {
		t.Error("key ignores position")
	}
}

func TestKeyUsesOnlyStableRows(t *testing.T) {
	fr := make([]uint16, Span)
	for i := range fr {
		fr[i] = 0xFFF
	}
	want, _ := Key(fr, 0)
	// Flipping rows 3..11 must not change the key.
	for i := range fr {
		fr[i] = 0xE00 // rows 0,1,2 set, rest clear
	}
	got, _ := Key(fr, 0)
	if got != want {
		t.Errorf("key changed when unstable rows changed: %#x vs %#x", got, want)
	}
	// Flipping row 0 must change it.
	for i := range fr {
		fr[i] = 0x600
	}
	if other, _ := Key(fr, 0); other == want {
		t.Error("key ignored row 0")
	}
}

func TestKeyRejectsSilenceAndBounds(t *testing.T) {
	fr := make([]uint16, Span+1)
	if _, ok := Key(fr, 0); ok {
		t.Error("all-zero frames produced a usable key")
	}
	if _, ok := Key(fr, -1); ok {
		t.Error("negative position accepted")
	}
	if _, ok := Key(fr, len(fr)); ok {
		t.Error("out-of-range position accepted")
	}
}

func TestKeyBitsFitInWidth(t *testing.T) {
	fr := make([]uint16, Span)
	for i := range fr {
		fr[i] = 0xFFF
	}
	k, _ := Key(fr, 0)
	if k>>KeyBits != 0 {
		t.Errorf("key %#x is wider than %d bits", k, KeyBits)
	}
}

func TestBuildAndLookupFindsItself(t *testing.T) {
	r := rand.New(rand.NewSource(3))
	fr := make([]uint16, 20000)
	for i := range fr {
		fr[i] = uint16(r.Intn(4095) + 1)
	}
	ix := Build(fr, Stride)
	if ix.Len() == 0 {
		t.Fatal("index is empty")
	}
	// Every stored position must find itself.
	for pos := 0; pos+Span <= len(fr); pos += Stride {
		if _, ok := Key(fr, pos); !ok {
			continue
		}
		got := ix.Lookup(fr, pos, nil)
		if !contains(got, pos) {
			t.Fatalf("position %d not returned by its own key", pos)
		}
	}
}

// A copy planted elsewhere must be found from the original's key.
func TestLookupFindsPlantedCopy(t *testing.T) {
	r := rand.New(rand.NewSource(4))
	fr := make([]uint16, 30000)
	for i := range fr {
		fr[i] = uint16(r.Intn(4095) + 1)
	}
	const src, dst, n = 1000, 20000, 4000
	copy(fr[dst:dst+n], fr[src:src+n])

	ix := Build(fr, Stride)
	// dst is on a stride boundary, so probing the original should reach it.
	found := false
	for off := 0; off < Stride*2 && !found; off++ {
		for _, p := range ix.Lookup(fr, src+off, nil) {
			if p >= dst && p < dst+n {
				found = true
			}
		}
	}
	if !found {
		t.Error("planted copy was not found")
	}
}

func TestLookupMissReturnsNothing(t *testing.T) {
	r := rand.New(rand.NewSource(5))
	fr := make([]uint16, 5000)
	for i := range fr {
		fr[i] = uint16(r.Intn(4095) + 1)
	}
	ix := Build(fr, Stride)
	other := make([]uint16, Span)
	for i := range other {
		other[i] = uint16(r.Intn(4095) + 1)
	}
	if got := ix.Lookup(other, 0, nil); len(got) > 3 {
		t.Errorf("unrelated frames matched %d entries", len(got))
	}
}

func TestBuildTooShort(t *testing.T) {
	ix := Build(make([]uint16, Span-1), Stride)
	if ix.Len() != 0 {
		t.Errorf("short input produced %d entries", ix.Len())
	}
	if got := ix.Lookup(make([]uint16, Span), 0, nil); len(got) != 0 {
		t.Errorf("empty index returned %d candidates", len(got))
	}
}

// On a real day the index must be small and its buckets mostly short,
// otherwise the search degenerates into the comparison it exists to avoid.
func TestRealDayIndexIsSelective(t *testing.T) {
	paths, err := filepath.Glob("../../testdata/enerji_fm-2026-08/2026-08-01-*_enerji_fm_*.mp3.bin")
	if err != nil || len(paths) == 0 {
		t.Skip("testdata not present")
	}
	sort.Strings(paths)
	tl, err := timeline.Build(paths[:4], nil)
	if err != nil {
		t.Fatal(err)
	}
	ix := Build(tl.Frames, Stride)

	wantEntries := len(tl.Frames) / Stride
	if ix.Len() < wantEntries/2 {
		t.Errorf("index has %d entries for %d frames, want near %d", ix.Len(), len(tl.Frames), wantEntries)
	}
	if per := float64(ix.Bytes()) / float64(len(tl.Frames)); per > 0.3 {
		t.Errorf("index costs %.3f bytes per frame, want under 0.3", per)
	}

	var total, probes int
	buf := make([]int, 0, MaxBucket)
	for pos := 0; pos+Span <= len(tl.Frames); pos += 997 {
		buf = ix.Lookup(tl.Frames, pos, buf[:0])
		total += len(buf)
		probes++
	}
	if probes == 0 {
		t.Fatal("no probes")
	}
	if avg := float64(total) / float64(probes); avg > 4 {
		t.Errorf("average bucket returned %.2f candidates, want a handful", avg)
	}
}

func contains(xs []int, v int) bool {
	for _, x := range xs {
		if x == v {
			return true
		}
	}
	return false
}

// A key with more entries than MaxBucket belongs to the most repeated audio.
// Dropping it loses exactly the links that join a fragment's later airings to
// its first, so the bucket is sampled instead.
func TestLookupSamplesSaturatedBucket(t *testing.T) {
	block := make([]uint16, Span)
	for i := range block {
		block[i] = 0xE00 | uint16(i%8)
	}
	fr := make([]uint16, 0, Span*400)
	for i := 0; i < 400; i++ {
		fr = append(fr, block...)
	}
	ix := Build(fr, Stride)
	got := ix.Lookup(fr, 0, nil)
	if len(got) == 0 {
		t.Fatal("saturated key returned nothing; the repeats it links are lost")
	}
	if len(got) > MaxBucket {
		t.Errorf("returned %d candidates, above the %d cap", len(got), MaxBucket)
	}
	// The sample must span the bucket, not sit at one end.
	lo, hi := got[0], got[0]
	for _, p := range got {
		lo, hi = min(lo, p), max(hi, p)
	}
	if hi-lo < len(fr)/2 {
		t.Errorf("sample spans %d of %d frames; it is bunched at one end", hi-lo, len(fr))
	}
}

// A short fragment gets far fewer chances to be caught than a long one, so
// the spacing has to follow the shortest length asked for. Six-second adverts
// were being missed outright at the fixed stride.
func TestStrideForShortFragments(t *testing.T) {
	const fps = 108
	for _, tc := range []struct {
		sec        int
		wantAtMost int
	}{
		{30, Stride},
		{10, Stride},
		{5, 20},
		{3, 12},
	} {
		got := StrideFor(tc.sec * fps)
		if got > tc.wantAtMost {
			t.Errorf("StrideFor(%ds) = %d, want at most %d", tc.sec, got, tc.wantAtMost)
		}
		if got < 1 {
			t.Errorf("StrideFor(%ds) = %d, must be at least 1", tc.sec, got)
		}
		// Whatever the spacing, a fragment must get a useful number of tries.
		if chances := tc.sec * fps / got; chances < TargetChances-1 {
			t.Errorf("a %ds fragment gets only %d chances", tc.sec, chances)
		}
	}
	if StrideFor(0) < 1 || StrideFor(-5) < 1 {
		t.Error("degenerate lengths must still give a usable stride")
	}
}

func TestBuildDefaultsStride(t *testing.T) {
	fr := make([]uint16, 5000)
	for i := range fr {
		fr[i] = uint16(i%4095 + 1)
	}
	if got := Build(fr, 0).Stride(); got != Stride {
		t.Errorf("stride %d for a zero argument, want the default %d", got, Stride)
	}
	if got := Build(fr, 8).Stride(); got != 8 {
		t.Errorf("stride %d, want 8", got)
	}
	// A denser index holds more entries.
	if a, b := Build(fr, 32).Len(), Build(fr, 8).Len(); b <= a {
		t.Errorf("stride 8 gave %d entries, stride 32 gave %d", b, a)
	}
}
