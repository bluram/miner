// Package timeline stitches a channel's fingerprint files into one run of frames.
//
// The recorder writes a 61-minute file every 60 minutes, so consecutive files
// share a minute. Left in, that minute makes anything aired in it look like a
// repeat of itself, so it is trimmed from the earlier file. Hours can also be
// missing, and those gaps stay real: frame positions must map back to true
// wall-clock times.
package timeline

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"miner/internal/fingerprint"
)

// Segment is a contiguous stretch of frames from one file.
type Segment struct {
	Path   string
	Start  time.Time // wall-clock time of the first frame
	Offset int       // its index within the source file
	N      int       // frames in this segment
	Global int       // its index within Timeline.Frames
}

// Timeline is every frame of one channel over a period, in order.
type Timeline struct {
	Channel string
	Frames  []uint16
	Segs    []Segment
	Skipped []Skip // unreadable files, reported not fatal
}

// Skip is one dropped file and why.
type Skip struct {
	Path string
	Err  error
}

// minFrames is the shortest fingerprint treated as a real recording. A
// half-written .bin can still be a valid whole number of frames, so size
// alone does not catch it.
var minFrames = fingerprint.DurationToFrames(time.Second)

func (t *Timeline) Len() int { return len(t.Frames) }

// At maps a global frame index back to wall-clock time and source file.
func (t *Timeline) At(global int) (time.Time, string, int, error) {
	if global < 0 || global >= len(t.Frames) {
		return time.Time{}, "", 0, fmt.Errorf("timeline: frame %d out of range [0,%d)", global, len(t.Frames))
	}
	s := t.Segs[t.segAt(global)]
	d := global - s.Global
	return s.Start.Add(fingerprint.FramesToDuration(d)), s.Path, s.Offset + d, nil
}

// Time is the wall-clock time of a global frame index.
func (t *Timeline) Time(global int) time.Time {
	ts, _, _, err := t.At(global)
	if err != nil {
		return time.Time{}
	}
	return ts
}

// Contiguous reports whether [from,to) lies inside one segment. A range
// straddling a recording gap is not one piece of audio.
func (t *Timeline) Contiguous(from, to int) bool {
	if from < 0 || to > len(t.Frames) || from >= to {
		return false
	}
	s := t.Segs[t.segAt(from)]
	return to <= s.Global+s.N
}

func (t *Timeline) segAt(global int) int {
	return sort.Search(len(t.Segs), func(i int) bool {
		return t.Segs[i].Global+t.Segs[i].N > global
	})
}

// Glob lists one channel's fingerprint files in a directory.
func Glob(dir string) ([]string, error) {
	paths, err := filepath.Glob(filepath.Join(dir, "*.mp3.bin"))
	if err != nil {
		return nil, err
	}
	if len(paths) == 0 {
		return nil, fmt.Errorf("timeline: no .mp3.bin files in %s", dir)
	}
	sort.Strings(paths)
	return paths, nil
}

// Build loads the given files and stitches them together. Files are read one
// at a time and released once copied, so peak memory stays near the size of
// the finished timeline.
func Build(paths []string) (*Timeline, error) {
	if len(paths) == 0 {
		return nil, fmt.Errorf("timeline: no files")
	}

	type meta struct {
		path    string
		channel string
		start   time.Time
		frames  int
	}

	var metas []meta
	tl := &Timeline{}

	for _, p := range paths {
		channel, _, start, err := fingerprint.ParseName(filepath.Base(p))
		if err != nil {
			tl.Skipped = append(tl.Skipped, Skip{p, err})
			continue
		}
		st, err := os.Stat(p)
		if err != nil {
			tl.Skipped = append(tl.Skipped, Skip{p, err})
			continue
		}
		n, err := fingerprint.FrameCount(st.Size())
		if err != nil {
			tl.Skipped = append(tl.Skipped, Skip{p, err})
			continue
		}
		if n < minFrames {
			tl.Skipped = append(tl.Skipped, Skip{p, fmt.Errorf("timeline: only %d frames", n)})
			continue
		}
		metas = append(metas, meta{p, channel, start, n})
	}
	if len(metas) == 0 {
		return nil, fmt.Errorf("timeline: no readable files")
	}

	sort.Slice(metas, func(i, j int) bool { return metas[i].start.Before(metas[j].start) })

	tl.Channel = metas[0].channel
	for _, m := range metas[1:] {
		if m.channel != tl.Channel {
			return nil, fmt.Errorf("timeline: mixed channels %q and %q", tl.Channel, m.channel)
		}
	}

	// Size the frame slice once: decide how much of each file survives first.
	keep := make([]int, len(metas))
	total := 0
	for i, m := range metas {
		keep[i] = m.frames
		if i+1 < len(metas) {
			if avail := fingerprint.DurationToFrames(metas[i+1].start.Sub(m.start)); avail < keep[i] {
				keep[i] = avail
			}
		}
		if keep[i] < 0 {
			keep[i] = 0
		}
		total += keep[i]
	}

	tl.Frames = make([]uint16, 0, total)
	for i, m := range metas {
		if keep[i] == 0 {
			continue
		}
		f, err := fingerprint.Load(m.path)
		if err != nil {
			tl.Skipped = append(tl.Skipped, Skip{m.path, err})
			continue
		}
		n := keep[i]
		if n > len(f.Frames) {
			n = len(f.Frames)
		}
		tl.Segs = append(tl.Segs, Segment{
			Path:   filepath.Base(m.path),
			Start:  m.start,
			N:      n,
			Global: len(tl.Frames),
		})
		tl.Frames = append(tl.Frames, f.Frames[:n]...)
		f.Frames = nil // release before the next read
	}
	if len(tl.Segs) == 0 {
		return nil, fmt.Errorf("timeline: every file failed to load")
	}
	return tl, nil
}
