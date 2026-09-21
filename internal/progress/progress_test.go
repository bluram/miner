package progress

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

func TestNilReporterIsSafe(t *testing.T) {
	var r *Reporter
	r.Update("search", 1, 10)
	r.Finish()
	if got := New(nil, false); got != nil {
		t.Error("New(nil) should disable the reporter")
	}
}

func TestUpdateWritesStageAndPercent(t *testing.T) {
	var buf bytes.Buffer
	r := New(&buf, false)
	r.Update("search", 25, 100)
	got := buf.String()
	if !strings.Contains(got, "[search]") {
		t.Errorf("missing stage: %q", got)
	}
	if !strings.Contains(got, "25%") {
		t.Errorf("missing percentage: %q", got)
	}
	if !strings.Contains(got, "RAM") {
		t.Errorf("missing memory: %q", got)
	}
}

// An unknown total must not print a percentage or an estimate.
func TestUnknownTotalOmitsPercent(t *testing.T) {
	var buf bytes.Buffer
	r := New(&buf, false)
	r.Update("group", 0, 0)
	got := buf.String()
	if strings.Contains(got, "%") && !strings.Contains(got, "CPU") {
		t.Errorf("percentage shown for unknown total: %q", got)
	}
	if strings.Contains(got, "left") {
		t.Errorf("estimate shown for unknown total: %q", got)
	}
}

// Piped output must not redraw thousands of times.
func TestPipedOutputIsRateLimited(t *testing.T) {
	var buf bytes.Buffer
	r := New(&buf, false)
	for i := 0; i < 1000; i++ {
		r.Update("search", i, 1000)
	}
	if n := strings.Count(buf.String(), "\n"); n != 1 {
		t.Errorf("wrote %d lines for one stage, want 1", n)
	}
}

// Each new stage is worth a line even when rate limited.
func TestEachStageIsReported(t *testing.T) {
	var buf bytes.Buffer
	r := New(&buf, false)
	for _, s := range []string{"load", "index", "search", "group"} {
		r.Update(s, 1, 10)
	}
	for _, s := range []string{"load", "index", "search", "group"} {
		if !strings.Contains(buf.String(), "["+s+"]") {
			t.Errorf("stage %s missing from %q", s, buf.String())
		}
	}
}

func TestFinishReportsPeak(t *testing.T) {
	var buf bytes.Buffer
	r := New(&buf, false)
	r.Update("search", 1, 10)
	r.Finish()
	if !strings.Contains(buf.String(), "peak RAM") {
		t.Errorf("Finish did not report the peak: %q", buf.String())
	}
}

func TestRSSIsPlausible(t *testing.T) {
	cur, peak := RSS()
	if cur == 0 {
		t.Error("current RSS is zero")
	}
	if peak < cur {
		t.Errorf("peak %d below current %d", peak, cur)
	}
}

func TestClock(t *testing.T) {
	for _, tc := range []struct {
		in   time.Duration
		want string
	}{
		{0, "0:00"},
		{-time.Second, "0:00"},
		{90 * time.Second, "1:30"},
		{time.Hour + 2*time.Minute + 3*time.Second, "1:02:03"},
	} {
		if got := clock(tc.in); got != tc.want {
			t.Errorf("clock(%v) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestGB(t *testing.T) {
	if got := gb(2 << 30); got != "2.0 GB" {
		t.Errorf("gb = %q, want 2.0 GB", got)
	}
	if got := gb(100 << 20); got != "100 MB" {
		t.Errorf("gb = %q, want 100 MB", got)
	}
}
