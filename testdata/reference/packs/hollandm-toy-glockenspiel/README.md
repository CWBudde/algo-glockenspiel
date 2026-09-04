# hollandm — Toy Glockenspiel

Twenty single strikes, one per file, chromatic from C6 to G7.

## Provenance

|          |                                                                        |
| -------- | ---------------------------------------------------------------------- |
| Source   | https://freesound.org/people/hollandm/packs/38756/                     |
| Title    | `Toy Glockenspiel` (pack 38756)                                        |
| Author   | hollandm (https://freesound.org/people/hollandm/)                      |
| License  | Creative Commons 0 — http://creativecommons.org/publicdomain/zero/1.0/ |
| Original | `../../38756__hollandm__toy-glockenspiel.zip`                          |

CC0 places these recordings in the public domain: no attribution is required.
The pack ships no description; `freesound-pack-license.txt` is the manifest
Freesound generated, and it maps every file here to its own sound page.

## Format

Nothing was cut. Each note arrived as its own file in the pack and was copied
byte for byte under a new name.

|             |                                |
| ----------- | ------------------------------ |
| encoding    | PCM (fmt tag `0x0001`), 16-bit |
| channels    | 2                              |
| sample rate | 44100 Hz                       |

## The naming

The author's own file names are `glock-c3` through `glock-g4`, and **ten of them
are duplicates** — two files named `glock-c3`, two named `glock-d3`, and so on.
They are not duplicates. Freesound strips `#` from an upload's name, so
`glock-c3` and `glock-c#3` both arrive as `glock-c3`; measuring the two files
gives 1046.2 Hz and 1109.9 Hz, which is C6 and C#6.

Every file here is therefore named by the note it actually sounds, measured, and
the table below carries the author's name for it. Note that a glockenspiel part
is written two octaves below what it sounds, which is roughly what the author's
octave numbers are doing.

## The notes

Fundamentals are measured from the first second after the onset, the same way
`glockenspiel analyze` measures them; "off ET" is the distance from equal
temperament at A4 = 440 Hz. Half-lives are the fundamental's.

| File      | Author's name | Sound page                                | Fundamental | Off ET | Half-life | Length |  Peak |
| --------- | ------------- | ----------------------------------------- | ----------: | -----: | --------: | -----: | ----: |
| `c6.wav`  | `glock-c3`    | [693343](https://freesound.org/s/693343/) |   1046.2 Hz |   -0 c |    641 ms | 5.00 s | 0.834 |
| `cs6.wav` | `glock-c3`    | [693341](https://freesound.org/s/693341/) |   1109.9 Hz |   +2 c |    808 ms | 9.00 s | 0.972 |
| `d6.wav`  | `glock-d3`    | [693347](https://freesound.org/s/693347/) |   1174.5 Hz |   -0 c |    689 ms | 8.00 s | 0.951 |
| `ds6.wav` | `glock-d3`    | [693345](https://freesound.org/s/693345/) |   1246.0 Hz |   +2 c |    508 ms | 7.00 s | 0.967 |
| `e6.wav`  | `glock-e3`    | [693349](https://freesound.org/s/693349/) |   1319.8 Hz |   +2 c |    664 ms | 7.50 s | 1.000 |
| `f6.wav`  | `glock-f3`    | [693353](https://freesound.org/s/693353/) |   1396.3 Hz |   -1 c |    699 ms | 9.00 s | 0.781 |
| `fs6.wav` | `glock-f3`    | [693351](https://freesound.org/s/693351/) |   1482.9 Hz |   +3 c |    368 ms | 4.50 s | 0.918 |
| `g6.wav`  | `glock-g3`    | [693356](https://freesound.org/s/693356/) |   1568.3 Hz |   +0 c |    457 ms | 6.50 s | 0.813 |
| `gs6.wav` | `glock-g3`    | [693355](https://freesound.org/s/693355/) |   1662.5 Hz |   +1 c |    433 ms | 4.50 s | 0.758 |
| `a6.wav`  | `glock-a3`    | [693339](https://freesound.org/s/693339/) |   1763.4 Hz |   +3 c |    337 ms | 5.00 s | 0.983 |
| `as6.wav` | `glock-a3`    | [693338](https://freesound.org/s/693338/) |   1869.7 Hz |   +5 c |    292 ms | 4.50 s | 1.000 |
| `b6.wav`  | `glock-b3`    | [693340](https://freesound.org/s/693340/) |   1978.5 Hz |   +3 c |    164 ms | 2.50 s | 0.894 |
| `c7.wav`  | `glock-c4`    | [693344](https://freesound.org/s/693344/) |   2096.8 Hz |   +3 c |    344 ms | 4.50 s | 0.896 |
| `cs7.wav` | `glock-c4`    | [693342](https://freesound.org/s/693342/) |   2217.5 Hz |   +0 c |    318 ms | 5.00 s | 0.897 |
| `d7.wav`  | `glock-d4`    | [693348](https://freesound.org/s/693348/) |   2350.0 Hz |   +0 c |    209 ms | 3.00 s | 1.000 |
| `ds7.wav` | `glock-d4`    | [693346](https://freesound.org/s/693346/) |   2490.9 Hz |   +1 c |    290 ms | 4.00 s | 0.851 |
| `e7.wav`  | `glock-e4`    | [693350](https://freesound.org/s/693350/) |   2640.9 Hz |   +3 c |    298 ms | 5.00 s | 1.000 |
| `f7.wav`  | `glock-f4`    | [693354](https://freesound.org/s/693354/) |   2798.0 Hz |   +3 c |    269 ms | 4.50 s | 1.000 |
| `fs7.wav` | `glock-f4`    | [693352](https://freesound.org/s/693352/) |   2966.4 Hz |   +4 c |    212 ms | 4.00 s | 0.768 |
| `g7.wav`  | `glock-g4`    | [693357](https://freesound.org/s/693357/) |   3137.6 Hz |   +1 c |    177 ms | 3.00 s | 1.000 |

The run is complete: twenty semitones, C6 to G7, nothing missing.

Two things decide how these can be used.

**The tuning is good but the levels are not.** No note is more than 5 cents off
equal temperament. Peaks run from 0.758 to 1.000, and five files reach full
scale — close enough that clipping is worth checking before any of them is
fitted, and far enough apart that a fit across notes has to normalise gain
(`--normalize-gain`).

**The sharps are separate recordings, not pitch shifts.** Each shares a name
with its natural but not a length: `c6.wav` runs 5.00 s against 9.00 s for
`cs6.wav`. Contrast the jamieblam pack next door, where the sharps _are_ shifts
and say so.
