package match

import (
	"math"
	"math/rand"
	"os"
	"path/filepath"
	"testing"

	"miner/internal/fingerprint"
)

const sampleFile = "../../testdata/enerji_fm-2026-08/2026-08-01-00-00-22_enerji_fm_srv4-61min.mp3.bin"

func TestRateIdentical(t *testing.T) {
	a := []uint16{0x5A5, 0x0F0, 0xFFF, 0x000}
	if got := Rate(a, a); got != 1 {
		t.Errorf("Rate(a,a) = %v, want 1", got)
	}
}

func TestRateOpposite(t *testing.T) {
	a := []uint16{0xFFF, 0x000}
	b := []uint16{0x000, 0xFFF}
	if got := Rate(a, b); got != 0 {
		t.Errorf("Rate = %v, want 0", got)
	}
}

func TestRateHalf(t *testing.T) {
	// Six of twelve bits differ in each frame.
	a := []uint16{0xFFF, 0xFFF}
	b := []uint16{0xFC0, 0xFC0}
	if got := Rate(a, b); got != 0.5 {
		t.Errorf("Rate = %v, want 0.5", got)
	}
}

func TestRateEmpty(t *testing.T) {
	if got := Rate(nil, nil); got != 0 {
		t.Errorf("Rate(nil,nil) = %v, want 0", got)
	}
}

func TestHamming(t *testing.T) {
	if got := Hamming([]uint16{0xFFF}, []uint16{0x000}); got != 12 {
		t.Errorf("Hamming = %d, want 12", got)
	}
	if got := Hamming([]uint16{0x001}, []uint16{0x003}); got != 1 {
		t.Errorf("Hamming = %d, want 1", got)
	}
}

// Averaging hides a bad patch; the weakest second must not.
func TestCompareMinCatchesLocalDamage(t *testing.T) {
	n := framesPerSecond * 10
	a := make([]uint16, n)
	for i := range a {
		a[i] = uint16(i * 7 % 4096)
	}
	b := append([]uint16(nil), a...)
	// Wreck one second out of ten.
	for i := framesPerSecond * 3; i < framesPerSecond*4; i++ {
		b[i] = ^a[i] & 0xFFF
	}
	s := Compare(a, b)
	if s.Seconds != 10 {
		t.Errorf("seconds = %d, want 10", s.Seconds)
	}
	if s.Rate < 0.85 {
		t.Errorf("overall rate = %.3f, expected it to stay high", s.Rate)
	}
	if s.Min != 0 {
		t.Errorf("weakest second = %.3f, want 0", s.Min)
	}
	if s.OK() {
		t.Error("OK() accepted a run with one destroyed second")
	}
}

func TestCompareIdenticalPasses(t *testing.T) {
	n := framesPerSecond * 5
	a := make([]uint16, n)
	for i := range a {
		a[i] = uint16(i * 13 % 4096)
	}
	s := Compare(a, a)
	if !s.OK() {
		t.Errorf("identical runs rejected: %+v", s)
	}
	if s.Min != 1 || s.Max != 1 || s.Mean != 1 {
		t.Errorf("stats = %+v, want all 1", s)
	}
}

func TestCompareShortRun(t *testing.T) {
	a := []uint16{0xFFF, 0xFFF}
	s := Compare(a, a)
	if s.Seconds != 0 {
		t.Errorf("seconds = %d, want 0", s.Seconds)
	}
	if s.Min != s.Rate || s.Mean != s.Rate {
		t.Errorf("stats = %+v, want Min and Mean to fall back to Rate", s)
	}
}

func TestRateAtBounds(t *testing.T) {
	f := make([]uint16, 100)
	for _, tc := range [][3]int{{-1, 0, 10}, {0, -1, 10}, {0, 0, 0}, {95, 0, 10}, {0, 95, 10}} {
		if got := RateAt(f, tc[0], tc[1], tc[2]); got != 0 {
			t.Errorf("RateAt(%v) = %v, want 0", tc, got)
		}
	}
}

func load(t *testing.T) []uint16 {
	t.Helper()
	if _, err := os.Stat(sampleFile); err != nil {
		t.Skip("testdata not present")
	}
	f, err := fingerprint.Load(filepath.Clean(sampleFile))
	if err != nil {
		t.Fatal(err)
	}
	return f.Frames
}

// The self-match at offset zero is the only score we know to be exactly 1
// without having found a repeat yet.
func TestRealSelfMatch(t *testing.T) {
	fr := load(t)
	n := framesPerSecond * 30
	if got := RateAt(fr, 1000, 1000, n); got != 1 {
		t.Errorf("self-match = %v, want 1", got)
	}
	s := Compare(fr[1000:1000+n], fr[1000:1000+n])
	if !s.OK() {
		t.Errorf("self-match rejected: %+v", s)
	}
}

// Unrelated stretches of real radio should sit near 0.5, well under the
// accept threshold. Anything much higher means the score is not
// discriminating and every pair would look like a repeat.
func TestRealUnrelatedIsNearHalf(t *testing.T) {
	fr := load(t)
	n := framesPerSecond * 30
	r := rand.New(rand.NewSource(1))
	worst, total, trials := 0.0, 0.0, 200
	for i := 0; i < trials; i++ {
		a := r.Intn(len(fr) - n)
		b := r.Intn(len(fr) - n)
		got := RateAt(fr, a, b, n)
		total += got
		worst = math.Max(worst, got)
	}
	if mean := total / float64(trials); mean < 0.45 || mean > 0.60 {
		t.Errorf("unrelated mean = %.3f, want ~0.5", mean)
	}
	if worst >= Threshold {
		t.Errorf("unrelated pair scored %.3f, at or above the %.2f threshold", worst, Threshold)
	}
}

// A shifted copy must not score as a match, or boundaries would be meaningless.
func TestRealShiftBreaksMatch(t *testing.T) {
	fr := load(t)
	n := framesPerSecond * 30
	if got := RateAt(fr, 1000, 1000+framesPerSecond, n); got >= Threshold {
		t.Errorf("one-second shift still scored %.3f", got)
	}
}

func TestExtendGrowsBothWays(t *testing.T) {
	step := framesPerSecond
	n := step * 60
	r := rand.New(rand.NewSource(9))
	fr := make([]uint16, n)
	for i := range fr {
		fr[i] = uint16(r.Intn(4095) + 1)
	}
	// Frames 10s-30s are duplicated at 35s-55s.
	const fromSec, toSec, lenSec = 10, 35, 20
	copy(fr[toSec*step:], fr[fromSec*step:(fromSec+lenSec)*step])

	// Seed in the middle of the copied region.
	a := (fromSec + 5) * step
	b := (toSec + 5) * step
	lo, length := Extend(fr, a, b, 10*step, step, 120*step, Threshold)

	if lo > fromSec*step+step || lo < fromSec*step-step {
		t.Errorf("start = %d, want near %d", lo, fromSec*step)
	}
	if want := lenSec * step; length < want-2*step || length > want+2*step {
		t.Errorf("length = %d, want near %d", length, want)
	}
}

func TestExtendRespectsMaxLen(t *testing.T) {
	step := framesPerSecond
	fr := make([]uint16, step*200)
	for i := range fr {
		fr[i] = 0xE0F // identical everywhere, so it would grow forever
	}
	maxLen := 30 * step
	_, length := Extend(fr, 0, step*100, 10*step, step, maxLen, Threshold)
	if length > maxLen {
		t.Errorf("length = %d, exceeds maxLen %d", length, maxLen)
	}
}

func TestExtendRejectsBadInput(t *testing.T) {
	fr := make([]uint16, 1000)
	for _, tc := range [][3]int{{-1, 0, 10}, {0, -1, 10}, {0, 0, 0}, {0, 990, 100}} {
		if _, n := Extend(fr, tc[0], tc[1], tc[2], 10, 1000, Threshold); n != 0 {
			t.Errorf("Extend(%v) returned length %d, want 0", tc, n)
		}
	}
}

func TestExtendStopsAtMismatch(t *testing.T) {
	step := framesPerSecond
	r := rand.New(rand.NewSource(12))
	fr := make([]uint16, step*100)
	for i := range fr {
		fr[i] = uint16(r.Intn(4095) + 1)
	}
	// A 15s copy and nothing beyond it.
	copy(fr[50*step:], fr[10*step:25*step])
	_, length := Extend(fr, 10*step, 50*step, 10*step, step, 120*step, Threshold)
	if length > 17*step {
		t.Errorf("length = %d frames (%ds), ran past the copied region", length, length/step)
	}
}

// One bad window inside a genuine repeat must not truncate it.
func TestExtendToleratesOneBadWindow(t *testing.T) {
	step := framesPerSecond
	r := rand.New(rand.NewSource(21))
	fr := make([]uint16, step*100)
	for i := range fr {
		fr[i] = uint16(r.Intn(4095) + 1)
	}
	const from, to, length = 10, 50, 25
	copy(fr[to*step:], fr[from*step:(from+length)*step])
	// Wreck one second at +18s into the copy.
	for i := (to + 18) * step; i < (to+19)*step; i++ {
		fr[i] = ^fr[i] & 0xFFF
	}
	_, got := Extend(fr, from*step, to*step, 10*step, step, 120*step, Threshold)
	if got < 22*step {
		t.Errorf("length = %ds, stopped at the damaged second instead of continuing", got/step)
	}
}
