package main

import (
	"fmt"
	"os"
	"runtime"
	"sort"
	"time"

	"miner/internal/cluster"
	"miner/internal/fingerprint"
	"miner/internal/index"
	"miner/internal/match"
	"miner/internal/timeline"
)

func mb(b uint64) float64 { return float64(b) / 1e6 }

func main() {
	hours := 8
	if len(os.Args) > 1 && os.Args[1] == "month" {
		hours = 0
	}
	paths, err := timeline.Glob("testdata/enerji_fm-2026-08")
	if err != nil {
		panic(err)
	}
	sort.Strings(paths)
	if hours > 0 {
		paths = paths[:hours]
	}

	t0 := time.Now()
	tl, err := timeline.Build(paths)
	if err != nil {
		panic(err)
	}
	fr := tl.Frames
	fmt.Printf("timeline  %d frames (%v)\n", len(fr), fingerprint.FramesToDuration(len(fr)).Round(time.Minute))
	ix := index.Build(fr)
	fmt.Printf("index     %d entries, %.0f MB\n", ix.Len(), float64(ix.Bytes())/1e6)

	fps := fingerprint.DurationToFrames(time.Second)
	minLen := fingerprint.DurationToFrames(10 * time.Second)
	maxLen := fingerprint.DurationToFrames(120 * time.Second)
	quick := 1 * fps
	tol := fps / 4

	t2 := time.Now()
	col := cluster.NewCollector(minLen, tol)
	buf := make([]int, 0, index.MaxBucket)
	for pos := 0; pos+minLen <= len(fr); pos++ {
		buf = ix.Lookup(fr, pos, buf[:0])
		for _, q := range buf {
			if q <= pos || q-pos < minLen || q+minLen > len(fr) {
				continue
			}
			if match.Rate(fr[pos:pos+quick], fr[q:q+quick]) < 0.75 {
				continue
			}
			if match.Rate(fr[pos:pos+minLen], fr[q:q+minLen]) < match.Threshold {
				continue
			}
			if !tl.Contiguous(pos, pos+minLen) || !tl.Contiguous(q, q+minLen) {
				continue
			}
			col.Add(pos, q-pos)
		}
	}
	pairs := col.Pairs()
	fmt.Printf("search    %v   pairs %d\n", time.Since(t2).Round(time.Second), len(pairs))

	t3 := time.Now()
	clusters := cluster.Group(pairs, 0.5)
	fmt.Printf("group     %v   clusters %d\n", time.Since(t3).Round(time.Second), len(clusters))

	var kept []cluster.Cluster
	for _, c := range clusters {
		if n := c.Len(); n >= minLen && n <= maxLen {
			kept = append(kept, c)
		}
	}
	fmt.Printf("in 10-120s range: %d clusters\n", len(kept))

	sort.Slice(kept, func(i, j int) bool {
		return len(kept[i].Occurrences) > len(kept[j].Occurrences)
	})
	fmt.Printf("\nmost repeated:\n")
	for i, c := range kept {
		if i >= 12 {
			break
		}
		fmt.Printf("  %3dx  %5.1fs  first %s\n", len(c.Occurrences),
			fingerprint.FramesToDuration(c.Len()).Seconds(),
			tl.Time(c.Occurrences[0].Start).Format("01-02 15:04:05"))
	}

	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	fmt.Printf("\ntotal %v   heap %.0f MB\n", time.Since(t0).Round(time.Second), mb(m.HeapAlloc))
}
