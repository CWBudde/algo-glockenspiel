# mooncubedesign — Toy Glockenspiel

Eight struck notes cut from one Freesound upload: a C major scale over one
octave.

## Provenance

|          |                                                                        |
| -------- | ---------------------------------------------------------------------- |
| Source   | https://freesound.org/s/420501/                                        |
| Title    | `Toy Glockenspiel`                                                     |
| Author   | mooncubedesign (https://freesound.org/people/mooncubedesign/)          |
| Uploaded | 2 March 2018                                                           |
| License  | Creative Commons 0 — http://creativecommons.org/publicdomain/zero/1.0/ |
| Original | `../../420501__mooncubedesign__toy-glockenspiel.wav`                   |
| Tags     | Glockenspiel, childs-toy, cmajor, metal, scale, toy, xylophone         |

CC0 places the recording in the public domain: no attribution is required.

The author's description, verbatim:

> Sampled a toy Glockenspiel. C Major scale. Each note runs for 2 bars set to
> 120bpm. Enjoy!

## Format

The cut keeps the source format byte for byte, including its broadcast-wave
chunks:

|             |                                |
| ----------- | ------------------------------ |
| encoding    | PCM (fmt tag `0x0001`), 16-bit |
| channels    | 2                              |
| sample rate | 44100 Hz                       |

## The notes

Two bars at 120 bpm is four seconds, and the strikes sit on that grid to within
a few milliseconds. Every file starts 10 ms before its own onset and runs to
10 ms before the next strike.

Fundamentals are measured from the first second after the onset, the same way
`glockenspiel analyze` measures them; "off ET" is the distance from equal
temperament at A4 = 440 Hz. Half-lives are the fundamental's.

| File     | Fundamental | Off ET | Half-life | Length |  Peak |
| -------- | ----------: | -----: | --------: | -----: | ----: |
| `c6.wav` |   1061.6 Hz |  +25 c |    174 ms | 3.99 s | 0.871 |
| `d6.wav` |   1198.4 Hz |  +35 c |    247 ms | 4.00 s | 0.852 |
| `e6.wav` |   1314.8 Hz |   -5 c |    412 ms | 4.00 s | 0.599 |
| `f6.wav` |   1407.9 Hz |  +14 c |    362 ms | 4.00 s | 0.797 |
| `g6.wav` |   1584.9 Hz |  +19 c |    248 ms | 4.00 s | 0.731 |
| `a6.wav` |   1810.2 Hz |  +49 c |    249 ms | 4.00 s | 0.723 |
| `b6.wav` |   2013.6 Hz |  +33 c |    252 ms | 4.00 s | 0.773 |
| `c7.wav` |   2129.9 Hz |  +30 c |    240 ms | 4.01 s | 0.667 |

**This instrument is not in tune.** The scale runs from 5 cents flat to 49 cents
sharp with no pattern to it: `a6.wav` is very nearly halfway between A6 and A#6.
The file names are the nearest equal-tempered note, which is a label; the
measured frequency in the table is the fact. Nothing here should be used as
evidence about tuning, and a fit that names a target note rather than a target
frequency will start half a semitone away on `a6.wav`.

Half-lives are short — 174 to 412 ms, against 320 to 910 ms for the
radiohummingbird reel — which is what a small toy bar does. That, and the
tuning, make this pack useful as the awkward case rather than the reference one.
