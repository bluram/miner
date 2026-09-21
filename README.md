# miner

Finds every fragment of audio that repeats in a radio channel's broadcast,
groups the airings into clusters, and reports their timings.

![Airings of the seven most repeated fragments across one day of enerji_fm](/overview.svg)

It does **not** decide what a fragment is. Telling adverts from jingles,
stingers and music is a separate layer built on this output.

## Why

Someone has to listen through 24 hours of recordings by hand to find adverts
and cut them for the advert database. This finds the repeats so that work
starts from a short list instead of a day of audio.

## Input

The production recorder already writes a binarised-MFCC fingerprint beside
every mp3, as a `.bin`. miner reads those, so it never decodes audio and never
touches the database:

    2026-08-01-00-00-22_enerji_fm_srv4-61min.mp3        28 MB
    2026-08-01-00-00-22_enerji_fm_srv4-61min.mp3.bin   577 KB   <- this

One channel-month is 422 MB of fingerprints instead of 21 GB of audio.

## Build

    make build          # -> bin/miner
    make test
    make vet

Needs Go 1.26. `ffmpeg` is required only for `-cut`.

## Use

    bin/miner -in <dir|listfile> [-out clusters.json]

    bin/miner -in fingerprints/                  # JSON to stdout
    bin/miner -in fingerprints/ -out month.json
    bin/miner -in day.txt -out day.json -cut samples/

`-in` takes a directory of `.mp3.bin` files, or a text file listing paths one
per line.

| flag | default | meaning |
|---|---|---|
| `-in` | — | directory of fingerprints, or a file listing them |
| `-out` | stdout | where to write the JSON |
| `-min` | `10s` | shortest fragment to report |
| `-max` | `2m` | longest |
| `-min-count` | `2` | airings needed to count as a repeat |
| `-rate` | `0.70` | similarity threshold |
| `-workers` | one per CPU | parallel search workers |
| `-cut` | off | also write one representative mp3 per cluster here |
| `-quiet` | off | no progress on stderr |

Progress goes to stderr, so stdout stays pipeable:

    [search]  71%  4:21 elapsed  ~1:45 left   RAM 2.1 GB (peak 2.1 GB)  CPU 572%

CPU is CPU time over wall time, so it exceeds 100% once the search spreads
across cores. Peak RAM comes from the kernel (`VmHWM`).

## Output

    {
      "channel": "enerji_fm",
      "from": "2026-08-01T00:00:22Z",
      "to": "2026-08-31T23:59:59Z",
      "clusters": [
        {
          "id": 1,
          "duration_sec": 34.12,
          "count": 47,
          "occurrences": [
            {
              "start": "2026-08-01T00:31:16.455Z",
              "end": "2026-08-01T00:31:50.575Z",
              "file": "fingerprints/2026-08-01-00-00-22_enerji_fm_srv4-61min.mp3.bin",
              "offset_sec": 1854.455
            }
          ]
        }
      ],
      "skipped": ["fingerprints/broken.mp3.bin"]
    }

`offset_sec` is where the airing begins inside `file`. Unreadable files land
in `skipped` rather than aborting the run.

## As a package

    import "miner"

    res, err := miner.Find(paths, miner.Options{Min: 10 * time.Second})
    for _, c := range res.Clusters {
        for _, o := range c.Occurrences {
            // o.Start, o.End, o.File, o.OffsetSec
        }
    }

`miner.Find` is the whole API: `Options`, `Result`, `Cluster`, `Occurrence`,
`Glob`. Everything else is under `internal/` and cannot be imported, so the
internals stay free to change.

## How it works

    files -> fingerprint -> timeline -> index -> match -> cluster -> JSON

**fingerprint** reads a `.bin` into one `uint16` per frame. The file has no
header, so the frame count comes from its size. 22050 Hz with a 205-sample
hop gives 107.56 frames a second, 12 bits each.

**timeline** stitches a channel's files into one run of frames. Recordings are
61 minutes at 60-minute spacing, so consecutive files share a minute; left in,
everything aired in it looks like a repeat of itself. Missing hours stay real
gaps and frame positions still map back to true wall-clock times.

**index** is what avoids comparing everything to everything. Real repeats are
not bit-identical — two airings agree on about 89% of bits and only 29% of
frames survive untouched — so a key cannot hash a whole window. It hashes the
three most stable rows, the low cepstral coefficients, which err 4-6% against
14% for the highest. That key survives a repeat about 20% of the time; entries
are stored every 32 frames while lookups probe every frame, so a 10-second
repeat gets ~33 chances and is found with 99.9% probability.

**match** scores two stretches as the fraction of agreeing bits, which is what
production's correlation reduces to on ±1 data — one XOR and a popcount per
frame. The threshold is 0.70 rather than production's 0.8: production compares
a clean uploaded advert against a recording, while every comparison here is
recording against recording, so both sides carry encoding noise.

**cluster** grows each cluster from a seed, checking every candidate against
that seed, then re-checks every surviving member against the cluster's
median-length representative. Pairs only *propose* that two airings belong
together; taking their transitive closure chains a 9-second fragment to a
40-second one a step at a time, where every link passes and the result is
nonsense.

## Performance

One channel-month — 744 files, 288 million frames, 744 hours — on 8 cores:

| stage | time |
|---|---|
| load | 17 s |
| index | 2 s |
| search | 2 m 15 s |
| group | 54 s |
| **total** | **3 m 29 s** |

Peak RSS 3.7 GB, index 72 MB. One day runs in under 2 seconds.

## Tests

    make test

Covers the acceptance points that apply to this layer: the same input gives
identical clusters (including across repeated parallel runs), a corrupt file
is skipped rather than fatal, length and count bounds are honoured, and every
external command runs under a timeout.
