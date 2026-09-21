// Command miner finds repeated audio in a radio channel's broadcast and
// writes the clusters as JSON.
//
// See docs/TZ.md for the specification.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"miner"
	"miner/internal/progress"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "miner:", err)
		os.Exit(1)
	}
}

func run() error {
	in := flag.String("in", "", "directory of .mp3.bin fingerprints, or a file listing them")
	out := flag.String("out", "", "write JSON here (default stdout)")
	minLen := flag.Duration("min", 10*time.Second, "shortest fragment to report")
	maxLen := flag.Duration("max", 120*time.Second, "longest fragment to report")
	minCount := flag.Int("min-count", 2, "airings needed to count as a repeat")
	rate := flag.Float64("rate", 0, "similarity threshold (0 uses the default)")
	cut := flag.String("cut", "", "also write one representative mp3 per cluster into this directory")
	quiet := flag.Bool("quiet", false, "no progress on stderr")
	flag.Parse()

	if *in == "" {
		flag.Usage()
		return fmt.Errorf("-in is required")
	}

	paths, err := inputs(*in)
	if err != nil {
		return err
	}

	var rep *progress.Reporter
	if !*quiet {
		rep = progress.Stderr()
	}
	res, err := miner.Find(paths, miner.Options{
		Min:      *minLen,
		Max:      *maxLen,
		MinCount: *minCount,
		Rate:     *rate,
		Progress: rep.Update,
	})
	if err != nil {
		return err
	}

	// Write the clusters before cutting: cutting is slow and optional, and
	// losing the run to a failure there would be absurd.
	if err := write(res, *out); err != nil {
		return err
	}
	if *cut != "" {
		if err := cutRepresentatives(res, *cut, rep); err != nil {
			return err
		}
	}
	rep.Finish()
	fmt.Fprintf(os.Stderr, "%d clusters\n", len(res.Clusters))
	return nil
}

// inputs accepts a directory or a file listing paths.
func inputs(in string) ([]string, error) {
	st, err := os.Stat(in)
	if err != nil {
		return nil, err
	}
	if st.IsDir() {
		return miner.Glob(in)
	}
	raw, err := os.ReadFile(in)
	if err != nil {
		return nil, err
	}
	var paths []string
	for _, line := range splitLines(string(raw)) {
		if line != "" {
			paths = append(paths, line)
		}
	}
	if len(paths) == 0 {
		return nil, fmt.Errorf("%s lists no files", in)
	}
	return paths, nil
}

func splitLines(s string) []string {
	var out []string
	start := 0
	for i := 0; i <= len(s); i++ {
		if i == len(s) || s[i] == '\n' {
			line := s[start:i]
			for len(line) > 0 && (line[len(line)-1] == '\r' || line[len(line)-1] == ' ') {
				line = line[:len(line)-1]
			}
			out = append(out, line)
			start = i + 1
		}
	}
	return out
}

func write(res *miner.Result, path string) error {
	enc := func(w *os.File) error {
		e := json.NewEncoder(w)
		e.SetIndent("", "  ")
		return e.Encode(res)
	}
	if path == "" {
		return enc(os.Stdout)
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return enc(f)
}

// cutRepresentatives writes one mp3 per cluster, so the sample can be heard.
// The .bin files sit beside the mp3 they were made from.
func cutRepresentatives(res *miner.Result, dir string, rep *progress.Reporter) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	for i := range res.Clusters {
		rep.Update("cut", i, len(res.Clusters))
		c := &res.Clusters[i]
		r := c.Representative()
		src := trimBin(r.File)
		if _, err := os.Stat(src); err != nil {
			return fmt.Errorf("cut cluster %d: %w", c.ID, err)
		}
		dst := filepath.Join(dir, fmt.Sprintf("cluster-%04d.mp3", c.ID))
		ctx, cancel := commandTimeout()
		cmd := exec.CommandContext(ctx, "ffmpeg", "-v", "error", "-y",
			"-ss", fmt.Sprintf("%.3f", r.OffsetSec),
			"-t", fmt.Sprintf("%.3f", c.DurationSec),
			"-i", src, dst)
		err := cmd.Run()
		cancel()
		if err != nil {
			return fmt.Errorf("cut cluster %d: %w", c.ID, err)
		}
	}
	return nil
}

func trimBin(name string) string {
	if filepath.Ext(name) == ".bin" {
		return name[:len(name)-len(".bin")]
	}
	return name
}
