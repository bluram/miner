package fingerprint

import (
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"
)

const sampleDir = "../../testdata/enerji_fm-2026-08"

func TestParseName(t *testing.T) {
	channel, server, start, err := ParseName("2026-08-01-00-00-22_enerji_fm_srv4-61min.mp3.bin")
	if err != nil {
		t.Fatal(err)
	}
	if channel != "enerji_fm" {
		t.Errorf("channel = %q, want enerji_fm", channel)
	}
	if server != "srv4" {
		t.Errorf("server = %q, want srv4", server)
	}
	want := time.Date(2026, 8, 1, 0, 0, 22, 0, time.UTC)
	if !start.Equal(want) {
		t.Errorf("start = %v, want %v", start, want)
	}
}

// Channel names contain underscores and dots, so the server suffix is what
// anchors the split.
func TestParseNameAwkwardChannels(t *testing.T) {
	for _, tc := range []struct{ name, channel string }{
		{"2026-07-01-00-00-08_106.3fm_srv4-61min.mp3.bin", "106.3fm"},
		{"2026-07-01-00-00-08_radio_space_fm_srv6-61min.mp3.bin", "radio_space_fm"},
		{"2026-07-23-12-00-00_saglam_fm_srv99-60min.mp3.bin", "saglam_fm"},
	} {
		got, _, _, err := ParseName(tc.name)
		if err != nil {
			t.Errorf("%s: %v", tc.name, err)
			continue
		}
		if got != tc.channel {
			t.Errorf("%s: channel = %q, want %q", tc.name, got, tc.channel)
		}
	}
}

func TestParseNameRejectsJunk(t *testing.T) {
	for _, name := range []string{
		"notes.txt",
		"2026-08-01_enerji_fm_srv4-61min.mp3.bin", // truncated timestamp
		"2026-08-01-00-00-22_enerji_fm-61min.mp3.bin",
	} {
		if _, _, _, err := ParseName(name); err == nil {
			t.Errorf("%s: want error, got none", name)
		}
	}
}

// The recorder stores the shape in Postgres; these pairs were read back from
// it so the size arithmetic can be checked without a database.
func TestFrameCount(t *testing.T) {
	for _, tc := range []struct {
		size int64
		want int
	}{
		{590508, 393672}, // shape {12,393672} in records
		{590501, 393667}, // 12*T is not byte-aligned here
		{590504, 393669},
		{3, 2},
	} {
		got, err := FrameCount(tc.size)
		if err != nil {
			t.Errorf("FrameCount(%d): %v", tc.size, err)
			continue
		}
		if got != tc.want {
			t.Errorf("FrameCount(%d) = %d, want %d", tc.size, got, tc.want)
		}
		if packed := (int64(Rows*got) + 7) / 8; packed != tc.size {
			t.Errorf("FrameCount(%d) = %d packs back to %d", tc.size, got, packed)
		}
	}
}

func TestFrameCountRejectsEmpty(t *testing.T) {
	if _, err := FrameCount(0); err == nil {
		t.Error("want error for empty file")
	}
}

// Rows are laid out one after another across the whole file, not interleaved
// per frame, so a transposition bug is invisible unless the bits are checked
// individually.
func TestTranspose(t *testing.T) {
	const n = 8
	// Row r is set only at frame r, giving a diagonal.
	raw := make([]byte, (Rows*n+7)/8)
	for r := 0; r < Rows; r++ {
		i := r*n + r%n
		raw[i>>3] |= 0x80 >> uint(i&7)
	}
	frames := transpose(raw, n)
	for r := 0; r < Rows; r++ {
		bit := uint16(1) << uint(Rows-1-r)
		if frames[r%n]&bit == 0 {
			t.Errorf("row %d: bit missing from frame %d", r, r%n)
		}
	}
}

func TestLoad(t *testing.T) {
	path := filepath.Join(sampleDir, "2026-08-01-00-00-22_enerji_fm_srv4-61min.mp3.bin")
	if _, err := os.Stat(path); err != nil {
		t.Skip("testdata not present")
	}
	f, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if f.Channel != "enerji_fm" {
		t.Errorf("channel = %q", f.Channel)
	}
	if len(f.Frames) != 393672 {
		t.Errorf("frames = %d, want 393672", len(f.Frames))
	}
	// A 61-minute recording, within a second.
	if got := f.Duration().Seconds(); math.Abs(got-3660) > 1 {
		t.Errorf("duration = %.1fs, want ~3660s", got)
	}
	if !f.End().After(f.Start) {
		t.Error("End is not after Start")
	}
	// Only the low 12 bits may be set.
	for i, fr := range f.Frames {
		if fr>>Rows != 0 {
			t.Fatalf("frame %d = %#x has bits above row 12", i, fr)
		}
	}
}

// Silence and dead air make some frames all-zero, but a real hour of radio
// must not be uniform.
func TestLoadHasVariety(t *testing.T) {
	path := filepath.Join(sampleDir, "2026-08-01-00-00-22_enerji_fm_srv4-61min.mp3.bin")
	if _, err := os.Stat(path); err != nil {
		t.Skip("testdata not present")
	}
	f, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[uint16]int{}
	for _, fr := range f.Frames {
		seen[fr]++
	}
	if len(seen) < 100 {
		t.Errorf("only %d distinct frame values; transposition is probably wrong", len(seen))
	}
}

func TestRoundTripFrameCount(t *testing.T) {
	paths, err := filepath.Glob(filepath.Join(sampleDir, "*.bin"))
	if err != nil || len(paths) == 0 {
		t.Skip("testdata not present")
	}
	for _, p := range paths {
		st, err := os.Stat(p)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := FrameCount(st.Size()); err != nil {
			t.Errorf("%s: %v", filepath.Base(p), err)
		}
	}
}
