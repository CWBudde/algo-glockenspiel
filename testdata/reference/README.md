# Reference audio

## The files

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

## The decoder bug these files exposed

Until the fix in `internal/wavio.sampleConverter`, this fixture decoded to a
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

## Regenerating

There is no procedure, and there should not be one until the provenance is known:
re-rendering the fixture from the current model would make it a copy of the thing
it is supposed to be evidence about. If it is ever replaced, expect
`TestOptimizationImprovesFitAgainstLegacyReference` to need its tolerances
re-measured — they describe the cost surface this exact file produces.
