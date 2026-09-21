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
	ix := Build(fr)
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

	ix := Build(fr)
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

func TestLookupDropsHugeBuckets(t *testing.T) {
	// The same 91 frames repeated: one key, thousands of positions.
	block := make([]uint16, Span)
	for i := range block {
		block[i] = 0xE00 | uint16(i%8)
	}
	fr := make([]uint16, 0, Span*400)
	for i := 0; i < 400; i++ {
		fr = append(fr, block...)
	}
	ix := Build(fr)
	if got := ix.Lookup(fr, 0, nil); len(got) != 0 {
		t.Errorf("returned %d candidates from a saturated key, want 0", len(got))
	}
}

func TestLookupMissReturnsNothing(t *testing.T) {
	r := rand.New(rand.NewSource(5))
	fr := make([]uint16, 5000)
	for i := range fr {
		fr[i] = uint16(r.Intn(4095) + 1)
	}
	ix := Build(fr)
	other := make([]uint16, Span)
	for i := range other {
		other[i] = uint16(r.Intn(4095) + 1)
	}
	if got := ix.Lookup(other, 0, nil); len(got) > 3 {
		t.Errorf("unrelated frames matched %d entries", len(got))
	}
}

func TestBuildTooShort(t *testing.T) {
	ix := Build(make([]uint16, Span-1))
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
	ix := Build(tl.Frames)

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
