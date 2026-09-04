# JamieBlam — Metallophone

Thirteen recorded strikes, C4 to A5, one per file. The ten sharps the pack also
ships are pitch shifts of those recordings and live in `pitch-shifted/`.

## Provenance

|          |                                                                                |
| -------- | ------------------------------------------------------------------------------ |
| Source   | https://freesound.org/people/JamieBlam/packs/9008/                             |
| Title    | `Metallophone` (pack 9008)                                                     |
| Author   | JamieBlam (https://freesound.org/people/JamieBlam/)                            |
| License  | Creative Commons Attribution 3.0 — http://creativecommons.org/licenses/by/3.0/ |
| Original | `../../9008__jamieblam__metallophone.zip`                                      |

**Attribution is required.** Credit "JamieBlam", link the pack and name the
licence anywhere these files are redistributed.

The author's pack description, verbatim:

> Metallophone hard strikes with rubber mallet.
>
> Recorded using a Rode NT-A Condenser Mic.
>
> Recorded from a C Major metallophone (no sharps or flats) so sharp and flat
> samples have been created by pitch shifting.

## Format

Nothing was cut. Each note arrived as its own file in the pack and was copied
byte for byte under a new name.

|             |                                |
| ----------- | ------------------------------ |
| encoding    | PCM (fmt tag `0x0001`), 16-bit |
| channels    | 1                              |
| sample rate | 44100 Hz                       |

## The naming

The author's names are `1c_hard` through `3a_hard`, and each appears twice
because Freesound strips `#` from an upload's name — `2c` is both C5 and C#5.
Every file here is named by the note it actually sounds, measured. The author's
octave number turns over at A rather than at C, so `2a` is A4 while `2c` is C5.

## The recorded notes

Fundamentals are measured from the first second after the onset, the same way
`glockenspiel analyze` measures them; "off ET" is the distance from equal
temperament at A4 = 440 Hz. Half-lives are the fundamental's.

| File     | Author's name | Sound page                                | Fundamental | Off ET | Half-life | Length |  Peak |
| -------- | ------------- | ----------------------------------------- | ----------: | -----: | --------: | -----: | ----: |
| `c4.wav` | `1c_hard`     | [146079](https://freesound.org/s/146079/) |    261.5 Hz |   -1 c |    599 ms | 6.97 s | 0.502 |
| `d4.wav` | `1d_hard`     | [146077](https://freesound.org/s/146077/) |    293.9 Hz |   +2 c |    375 ms | 4.13 s | 0.506 |
| `e4.wav` | `1e_hard`     | [146084](https://freesound.org/s/146084/) |    326.6 Hz |  -16 c |    186 ms | 3.20 s | 0.502 |
| `f4.wav` | `1f_hard`     | [146082](https://freesound.org/s/146082/) |    343.7 Hz |  -28 c |    228 ms | 3.61 s | 0.503 |
| `g4.wav` | `1g_hard`     | [146087](https://freesound.org/s/146087/) |    391.9 Hz |   -0 c |    236 ms | 4.43 s | 0.466 |
| `a4.wav` | `2a_hard`     | [146094](https://freesound.org/s/146094/) |    439.9 Hz |   -0 c |    186 ms | 4.03 s | 0.471 |
| `b4.wav` | `2b_hard`     | [146093](https://freesound.org/s/146093/) |    494.8 Hz |   +3 c |    312 ms | 4.85 s | 0.440 |
| `c5.wav` | `2c_hard`     | [146091](https://freesound.org/s/146091/) |    519.6 Hz |  -12 c |    200 ms | 3.50 s | 0.471 |
| `d5.wav` | `2d_hard`     | [146097](https://freesound.org/s/146097/) |    588.0 Hz |   +2 c |    436 ms | 6.93 s | 0.419 |
| `e5.wav` | `2e_hard`     | [146096](https://freesound.org/s/146096/) |    659.4 Hz |   +0 c |    527 ms | 7.76 s | 0.401 |
| `f5.wav` | `2f_hard`     | [146100](https://freesound.org/s/146100/) |    698.8 Hz |   +1 c |    439 ms | 6.73 s | 0.469 |
| `g5.wav` | `2g_hard`     | [146089](https://freesound.org/s/146089/) |    784.4 Hz |   +1 c |    432 ms | 6.85 s | 0.431 |
| `a5.wav` | `3a_hard`     | [146088](https://freesound.org/s/146088/) |    881.2 Hz |   +2 c |    414 ms | 6.09 s | 0.468 |

This is a metallophone, not a glockenspiel: an octave and a half below the
glockenspiel packs next door, with half-lives to match. `e4` and `f4` sit 16 and
28 cents flat; the rest is within 3 cents.

## `pitch-shifted/`

| File      | Shifted from | Fundamental | Off ET | Half-life | Length |  Peak |
| --------- | ------------ | ----------: | -----: | --------: | -----: | ----: |
| `cs4.wav` | `c4.wav`     |    277.0 Hz |   -1 c |    596 ms | 6.93 s | 0.487 |
| `ds4.wav` | `d4.wav`     |    311.3 Hz |   +1 c |    376 ms | 4.10 s | 0.493 |
| `fs4.wav` | `f4.wav`     |    364.2 Hz |  -27 c |    228 ms | 3.57 s | 0.487 |
| `gs4.wav` | `g4.wav`     |    415.3 Hz |   +0 c |    236 ms | 4.39 s | 0.451 |
| `as4.wav` | `a4.wav`     |    466.0 Hz |   -0 c |    185 ms | 3.98 s | 0.455 |
| `cs5.wav` | `c5.wav`     |    550.5 Hz |  -12 c |    199 ms | 3.47 s | 0.456 |
| `ds5.wav` | `d5.wav`     |    622.9 Hz |   +2 c |    439 ms | 6.88 s | 0.405 |
| `fs5.wav` | `f5.wav`     |    740.4 Hz |   +1 c |    442 ms | 6.69 s | 0.451 |
| `gs5.wav` | `g5.wav`     |    831.0 Hz |   +1 c |    430 ms | 6.81 s | 0.418 |
| `as5.wav` | `a5.wav`     |    933.5 Hz |   +2 c |    418 ms | 6.05 s | 0.452 |

**These are not evidence about a bar.** The author says the sharps were made by
pitch shifting, and the numbers agree: each carries its natural's tuning error
to the cent — `f4` is 28 cents flat and `fs4` is 27 — and its length to within
about one percent, where a resample up a semitone would have cut 5.6% off it.
The shift preserved time. Fitting a model against one of them measures the
shifter, not an instrument.

They are kept because they are still a useful negative: a mode set that a real
struck bar cannot produce, sitting right next to the recording it was made from.
Which of `1g_hard` and `2a_hard` is the natural was decided by measurement, not
by the pack's file order — the lower of each pair is the recording.
