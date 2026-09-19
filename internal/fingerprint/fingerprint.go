// Package fingerprint reads the binarised-MFCC .bin files the production
// recorder writes beside every mp3.
//
// A .bin is numpy packbits of a (12, T) array: all of row 0, then row 1, and
// so on, MSB first, padded to a byte at the end. No header, so T comes from
// the file size.
package fingerprint

import (
	"fmt"
	"os"
	"regexp"
	"time"
)

// Rows is the coefficient count surviving binarize_mfcc (numcep 13, row 0 dropped).
const Rows = 12

// Production records at 22050 Hz with a 205-sample hop. The stored
// frame_width is 205.00061; the difference is 11 ms per hour.
const (
	sampleRate = 22050
	hopSamples = 205
	FrameRate  = float64(sampleRate) / float64(hopSamples)
)

// File is one decoded fingerprint.
type File struct {
	Path    string
	Channel string // "enerji_fm"
	Server  string // "srv4"
	Start   time.Time
	// Frames holds the 12 bits of each frame in one uint16, row 0 at bit 11.
	// Frame-major costs 2 bytes instead of 1.5, but gives O(1) access and a
	// single XOR per comparison.
	Frames []uint16
}

func (f *File) Duration() time.Duration { return FramesToDuration(len(f.Frames)) }
func (f *File) End() time.Time          { return f.Start.Add(f.Duration()) }

func FramesToDuration(frames int) time.Duration {
	return time.Duration(float64(frames) / FrameRate * float64(time.Second))
}

func DurationToFrames(d time.Duration) int {
	return int(d.Seconds() * FrameRate)
}

// 2026-08-01-00-00-22_enerji_fm_srv4-61min.mp3.bin
var nameRe = regexp.MustCompile(
	`^(\d{4}-\d{2}-\d{2}-\d{2}-\d{2}-\d{2})_(.+)_(srv\d+)-\d+min\.mp3\.bin$`)

// ParseName reads channel, server and start time out of a .bin filename.
func ParseName(base string) (channel, server string, start time.Time, err error) {
	m := nameRe.FindStringSubmatch(base)
	if m == nil {
		return "", "", time.Time{}, fmt.Errorf("fingerprint: unrecognised name %q", base)
	}
	start, err = time.ParseInLocation("2006-01-02-15-04-05", m[1], time.UTC)
	if err != nil {
		return "", "", time.Time{}, fmt.Errorf("fingerprint: bad timestamp in %q: %w", base, err)
	}
	return m[2], m[3], start, nil
}

// FrameCount recovers T from a .bin size.
//
// packbits pads 12*T bits up to a byte, so size is ceil(12*T/8) and dividing
// back can land one frame high.
func FrameCount(size int64) (int, error) {
	if size <= 0 {
		return 0, fmt.Errorf("fingerprint: empty file")
	}
	t := int(size * 8 / Rows)
	for _, cand := range [...]int{t, t - 1} {
		if cand > 0 && (int64(Rows*cand)+7)/8 == size {
			return cand, nil
		}
	}
	return 0, fmt.Errorf("fingerprint: size %d is not a whole number of 12-bit frames", size)
}

// Load reads and decodes one .bin.
func Load(path string) (*File, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	base := path[lastSlash(path)+1:]
	channel, server, start, err := ParseName(base)
	if err != nil {
		return nil, err
	}
	n, err := FrameCount(int64(len(raw)))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", base, err)
	}
	return &File{
		Path:    path,
		Channel: channel,
		Server:  server,
		Start:   start,
		Frames:  transpose(raw, n),
	}, nil
}

// transpose turns the row-major bit stream into one uint16 per frame.
// Bit (row, frame) sits at flat index row*n+frame, MSB first in each byte.
func transpose(raw []byte, n int) []uint16 {
	frames := make([]uint16, n)
	for row := 0; row < Rows; row++ {
		base := row * n
		shift := uint(Rows - 1 - row)
		for t := 0; t < n; t++ {
			i := base + t
			if raw[i>>3]&(0x80>>uint(i&7)) != 0 {
				frames[t] |= 1 << shift
			}
		}
	}
	return frames
}

func lastSlash(s string) int {
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] == '/' {
			return i
		}
	}
	return -1
}
