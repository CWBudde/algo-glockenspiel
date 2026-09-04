# radiohummingbird — Morphagene Reel, Glockenspiel Rubber Mallet

Fifteen struck notes cut from one Freesound upload, a C major scale spanning two
octaves.

## Provenance

|          |                                                                                                                                                                         |
| -------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Source   | https://freesound.org/s/635381/                                                                                                                                         |
| Title    | `Morphagene Reel - Glockenspiel Rubber Mallet.wav`                                                                                                                      |
| Author   | radiohummingbird (https://freesound.org/people/radiohummingbird/)                                                                                                       |
| Uploaded | 27 May 2022                                                                                                                                                             |
| License  | Creative Commons Attribution 4.0 — https://creativecommons.org/licenses/by/4.0/                                                                                         |
| Original | `../../635381__radiohummingbird__morphagene-reel-glockenspiel-rubber-mallet.wav`                                                                                        |
| Tags     | chimes, electroacoustic, electronic, eurorack, glockenspiel, granular, makenoise, mgreel, modular, morphagene, music-production, sample, sound-design, synth, synthesis |

**Attribution is required.** Credit "radiohummingbird", link the sound and name
the licence anywhere these files are redistributed.

The author's description, verbatim:

> this is a morphagene reel with a sequence of recorded alto-tenor glockenspiel
> (chimes) sounds on it. it is played with a rubber mallet and contains 15 tones
> or two octaves in the c-major scale.
>
> the tones are placed chronologically in the reel and are each separated
> individually by a marker so that there are 15 splices in this one reel.
>
> i hope this reel is giving you much joy whilst using it!:)

## Format

The cut keeps the source format byte for byte, including its broadcast-wave
chunks:

|             |                                                     |
| ----------- | --------------------------------------------------- |
| encoding    | `WAVE_FORMAT_IEEE_FLOAT` (fmt tag `0x0003`), 32-bit |
| channels    | 2                                                   |
| sample rate | 48000 Hz                                            |

## The notes

Every file starts 10 ms before its own onset and runs to 10 ms before the next
strike, so each holds one whole strike and its tail. `c7.wav` is shorter only
because the reel ends.

Fundamentals are measured from the first second after the onset, the same way
`glockenspiel analyze` measures them; "off ET" is the distance from equal
temperament at A4 = 440 Hz. Half-lives are the fundamental's.

| File     | Fundamental | Off ET | Half-life | Length |  Peak |
| -------- | ----------: | -----: | --------: | -----: | ----: |
| `c5.wav` |    524.0 Hz |   +2 c |    736 ms | 6.00 s | 0.501 |
| `d5.wav` |    588.0 Hz |   +2 c |    776 ms | 6.00 s | 0.501 |
| `e5.wav` |    661.1 Hz |   +5 c |    669 ms | 6.00 s | 0.501 |
| `f5.wav` |    698.9 Hz |   +1 c |    910 ms | 6.00 s | 0.501 |
| `g5.wav` |    784.2 Hz |   +1 c |    624 ms | 6.00 s | 0.501 |
| `a5.wav` |    879.9 Hz |   -0 c |    714 ms | 6.00 s | 0.501 |
| `b5.wav` |    986.6 Hz |   -2 c |    542 ms | 6.00 s | 0.501 |
| `c6.wav` |   1047.3 Hz |   +1 c |    577 ms | 6.00 s | 0.501 |
| `d6.wav` |   1176.7 Hz |   +3 c |    700 ms | 6.00 s | 0.501 |
| `e6.wav` |   1319.0 Hz |   +1 c |    550 ms | 6.00 s | 0.501 |
| `f6.wav` |   1399.6 Hz |   +3 c |    602 ms | 6.00 s | 0.501 |
| `g6.wav` |   1570.5 Hz |   +3 c |    435 ms | 6.00 s | 0.501 |
| `a6.wav` |   1761.1 Hz |   +1 c |    570 ms | 6.00 s | 0.501 |
| `b6.wav` |   1975.6 Hz |   +0 c |    327 ms | 6.00 s | 0.501 |
| `c7.wav` |   2092.9 Hz |   -0 c |    320 ms | 5.56 s | 0.501 |

Three things decide how these can be used.

**The naming is by sounding pitch, not by glockenspiel notation.** A glockenspiel
part is written two octaves below what it sounds, so a player reading this scale
would call `c5.wav` "C3". Beware that `../../glockenspiel_c5.wav` in the parent
directory takes the other convention — it is named for a written C5 and sounds
C6 — so it is `c6.wav` here, not `c5.wav`, that sits beside it in pitch.

**The tuning is exact.** Nothing here is further than 5 cents from equal
temperament, which is what makes this pack the one to fit against when the
question is about a bar's modes rather than about a toy's intonation.

**The levels carry no information.** Every file peaks at 0.501, so the reel was
level-matched note by note before it was uploaded. Relative loudness across the
scale is the author's gain staging, and any fit that compares two of these files
has to normalise gain (`--normalize-gain`).
