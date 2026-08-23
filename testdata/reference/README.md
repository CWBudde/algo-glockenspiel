# Reference audio

## The files

| File                  | What it is                         | Fitted into                        |
| --------------------- | ---------------------------------- | ---------------------------------- |
| `glockenspiel_a4.wav` | A render, provenance unrecorded    | `assets/presets/default.json`      |
| `legacy_synth_a4.wav` | The same bytes under a second name | -                                  |
| `glockenspiel_c5.wav` | A real room recording              | `assets/presets/recorded-bar.json` |

## The A4 pair

`legacy_synth_a4.wav` and `glockenspiel_a4.wav` are **byte-identical**
(`md5 de81a10a1bf2f11a5125bb3b4d115d56`, 98364 bytes). They are two names for one
fixture, and the two names are load-bearing only in that different tests reach for
different ones: `internal/optimizer/legacy_validation_test.go` and
`internal/synth/legacy_compare_test.go` use the first, `testdata_smoke_test.go`
checks for the second.

Format and content:

|              |                                                           |
| ------------ | --------------------------------------------------------- |
| encoding     | `WAVE_FORMAT_IEEE_FLOAT` (fmt tag `0x0003`), 32-bit float |
| channels     | 1                                                         |
| sample rate  | 44100 Hz                                                  |
| length       | 24580 samples, 0.5574 s                                   |
| peak         | 1.0000                                                    |
| RMS          | 0.1932                                                    |
| crest factor | 5.18                                                      |

It is a struck bar: energy clustered near 1800 Hz — A4 = 440 Hz times four, the
first inharmonic mode of the shipped preset — decaying monotonically from an RMS
of 0.343 in the first 100 ms to 0.045 by 500 ms.

## Provenance

Unrecorded. Both files entered the repository in `8c3f312`, whose entire commit
message is "initial commit", and neither has been touched since. Nothing in the
tree says whether they were recorded, rendered by a predecessor implementation,
or exported from a DAW. Two things are known from the bytes: this project cannot
have written them, because `internal/wavio` encodes 16-bit PCM only
(`encodeBitDepth = 16`), and `internal/optimizer/legacy_validation_test.go` calls
the file "the pinned legacy render" — a render, not a recording.

Treat the content as authoritative and the label as a guess.

## `glockenspiel_c5.wav`

Added on 2026-08-23. Unlike the A4 pair it is unmistakably a **recording**: two
channels that correlate at 0.88 rather than duplicate, a room tail, and a noise
floor.

|             |                                |
| ----------- | ------------------------------ |
| encoding    | PCM (fmt tag `0x0001`), 16-bit |
| channels    | 2                              |
| sample rate | 44100 Hz                       |
| length      | 317816 frames, 7.207 s         |
| peak        | 0.0417 (-27.6 dBFS)            |
| onset       | frame 112                      |

Three things about it decide how it can be used.

**Its fundamental mode is 1053.6 Hz**, which is C6 plus 12 cents, not C5. The
naming follows the same convention as the A4 pair, whose first mode sits at
1756 Hz -- two octaves above the written note, which is how a glockenspiel is
notated. Take the file name as a label and the 1053.6 Hz as the measurement.

**Only the first second is a struck note.** Level falls monotonically to about
-62 dB by 1.75 s and then _rises_ again to -59 dB between 2.0 and 2.3 s. Whatever
that second event is, it is not the decay of the first strike, so a fit that
scores the whole file is scoring two things.

**Its level is arbitrary.** At -27.6 dBFS peak it carries someone's gain
staging, not the bar's loudness, so any fit against it has to normalise gain
(`--normalize-gain`) or scale the reference first.

Measured partials over the first second, with half-lives from the narrowband
envelope:

| Partial   | Level  | Half-life | T60    |
| --------- | ------ | --------- | ------ |
| 1053.6 Hz | 0 dB   | 677 ms    | 6.75 s |
| 3096.9 Hz | -16 dB | 117 ms    | 1.17 s |
| 8023.8 Hz | -20 dB | 204 ms    | 2.03 s |
| 5836.8 Hz | -30 dB | 55 ms     | 0.55 s |
| 4139.8 Hz | -37 dB | 626 ms    | 6.24 s |
| 3705.2 Hz | -39 dB | 71 ms     | 0.71 s |

The 677 ms half-life is the reason `model.DecayMsSearchMax` at 500 ms is a live
constraint on this file rather than a generous one: fits against it pin exactly
at the bound unless `--bounds` widens it.

The attack is not a separate thing from the partials. The first 10 ms holds 4.3%
of the energy and 72% of _that_ is between 4.5 and 7 kHz -- it is the 5836.8 Hz
mode, whose 55 ms half-life is over before the ear has finished calling it a
transient. A fit that closes the excitation lowpass to keep the low partials in
balance loses the attack entirely, which is what makes `filter_freq` the
load-bearing bound here.

## The decoder bug the A4 pair exposed

Until the fix in `internal/wavio.sampleConverter`, the A4 fixture decoded to a
square wave. `go-audio/wav` reads every sample format as an integer — its 32-bit
reader is `int(int32(binary.LittleEndian.Uint32(...)))`, and its float path
carries `// TODO: fix the float64 conversion (current int implementation)` — so
each float arrived as its own bit pattern, and `wavio` then divided it by
2^(bits-1). Every float32 in [0.06, 1.0] has an exponent that puts its bit
pattern near `0x3F00_0000`, which is +0.492 after that division; the sign bit put
the negatives near -0.508. The file came back as peak 0.5702, RMS 0.5004, crest
1.14.

That was not confined to the fixture. `internal/cli/fit.go` and
`internal/server/fit.go` read user references through the same function, so every
`glockenspiel fit --reference` against a 32-bit float WAV — what a DAW exports by
default — was fitting a square wave.

## Regenerating the A4 pair

There is no procedure, and there should not be one until the provenance is known:
re-rendering the fixture from the current model would make it a copy of the thing
it is supposed to be evidence about. If it is ever replaced, expect
`TestOptimizationImprovesFitAgainstLegacyReference` to need its tolerances
re-measured — they describe the cost surface this exact file produces.
