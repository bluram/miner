// Package progress draws a one-line status on stderr.
//
// Status goes to stderr so it never corrupts the JSON on stdout, and it
// redraws in place only when stderr is a terminal; piped to a file it prints
// one line per stage instead of thousands of bars.
package progress

import (
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// Reporter renders stage, percentage, timing and resource use.
type Reporter struct {
	w   io.Writer
	tty bool

	mu         sync.Mutex
	stage      string
	stageStart time.Time
	start      time.Time
	lastDraw   time.Time
	drawn      bool
}

const (
	// redraw interval on a terminal, so the status never dominates the work.
	everyTTY = 200 * time.Millisecond
	// on a pipe each update is a new line, so they have to be rare enough to
	// stay readable in a log.
	everyPipe = 30 * time.Second
	// CPU share is meaningless before some wall time has passed.
	minForCPU = 300 * time.Millisecond
	// nor is an estimate on a stage that just started.
	minForETA = 2 * time.Second
)

// New returns a Reporter writing to w. A nil w disables it.
func New(w io.Writer, tty bool) *Reporter {
	if w == nil {
		return nil
	}
	now := time.Now()
	return &Reporter{w: w, tty: tty, start: now, stageStart: now}
}

// Stderr returns a Reporter on stderr, detecting whether it is a terminal.
func Stderr() *Reporter {
	tty := false
	if st, err := os.Stderr.Stat(); err == nil {
		tty = st.Mode()&os.ModeCharDevice != 0
	}
	return New(os.Stderr, tty)
}

// Update reports progress within a stage. total <= 0 means unknown, and the
// percentage and estimate are left out.
func (r *Reporter) Update(stage string, done, total int) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	now := time.Now()
	fresh := stage != r.stage
	if fresh {
		r.endLine()
		r.stage, r.stageStart = stage, now
	} else {
		gap := everyTTY
		if !r.tty {
			gap = everyPipe
		}
		if now.Sub(r.lastDraw) < gap {
			return
		}
	}
	r.lastDraw = now
	r.draw(now, done, total)
}

func (r *Reporter) draw(now time.Time, done, total int) {
	var b strings.Builder
	fmt.Fprintf(&b, "[%s]", r.stage)
	if total > 0 {
		pct := 100 * float64(done) / float64(total)
		fmt.Fprintf(&b, " %3.0f%%", pct)
	}
	elapsed := now.Sub(r.start)
	fmt.Fprintf(&b, "  %s elapsed", clock(elapsed))
	// An estimate needs enough of the stage behind it to mean anything.
	if spent := now.Sub(r.stageStart); total > 0 && done > 0 && spent >= minForETA {
		left := time.Duration(float64(spent) / float64(done) * float64(total-done))
		fmt.Fprintf(&b, "  ~%s left", clock(left))
	}
	cur, peak := RSS()
	fmt.Fprintf(&b, "   RAM %s (peak %s)", gb(cur), gb(peak))
	if elapsed >= minForCPU {
		fmt.Fprintf(&b, "  CPU %.0f%%", 100*cpuFraction(elapsed))
	}

	if r.tty {
		fmt.Fprintf(r.w, "\r\033[K%s", b.String())
		r.drawn = true
		return
	}
	fmt.Fprintln(r.w, b.String())
}

// Finish clears the line and reports the peak.
func (r *Reporter) Finish() {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.endLine()
	_, peak := RSS()
	fmt.Fprintf(r.w, "done in %s, peak RAM %s\n", clock(time.Since(r.start)), gb(peak))
}

func (r *Reporter) endLine() {
	if r.tty && r.drawn {
		fmt.Fprint(r.w, "\r\033[K")
		r.drawn = false
	}
}

// RSS returns current and peak resident set size in bytes. Peak is what the
// acceptance measures, and the kernel tracks it for us.
func RSS() (current, peak uint64) {
	current = fieldKB("VmRSS")
	peak = fieldKB("VmHWM")
	if peak == 0 {
		var ru syscall.Rusage
		if syscall.Getrusage(syscall.RUSAGE_SELF, &ru) == nil {
			peak = uint64(ru.Maxrss) * 1024
		}
	}
	if peak < current {
		peak = current
	}
	return current, peak
}

// fieldKB reads a kB-valued line out of /proc/self/status.
func fieldKB(name string) uint64 {
	raw, err := os.ReadFile("/proc/self/status")
	if err != nil {
		return 0
	}
	for _, line := range strings.Split(string(raw), "\n") {
		if !strings.HasPrefix(line, name+":") {
			continue
		}
		f := strings.Fields(line)
		if len(f) < 2 {
			return 0
		}
		v, err := strconv.ParseUint(f[1], 10, 64)
		if err != nil {
			return 0
		}
		return v * 1024
	}
	return 0
}

// cpuFraction is CPU time over wall time, so it exceeds 1 once the work runs
// on several cores.
func cpuFraction(elapsed time.Duration) float64 {
	if elapsed <= 0 {
		return 0
	}
	var ru syscall.Rusage
	if syscall.Getrusage(syscall.RUSAGE_SELF, &ru) != nil {
		return 0
	}
	cpu := time.Duration(ru.Utime.Nano()) + time.Duration(ru.Stime.Nano())
	return float64(cpu) / float64(elapsed)
}

func clock(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	d = d.Round(time.Second)
	h := int(d / time.Hour)
	m := int(d/time.Minute) % 60
	s := int(d/time.Second) % 60
	if h > 0 {
		return fmt.Sprintf("%d:%02d:%02d", h, m, s)
	}
	return fmt.Sprintf("%d:%02d", m, s)
}

func gb(b uint64) string {
	const g = 1 << 30
	if b >= g {
		return fmt.Sprintf("%.1f GB", float64(b)/g)
	}
	return fmt.Sprintf("%d MB", b>>20)
}
