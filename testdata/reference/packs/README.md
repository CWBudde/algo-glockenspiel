# Reference packs

Four Freesound packs of struck-bar recordings, one note per file, named by the
note each file actually sounds.

| Pack                                                                                     | Instrument                  |           Notes | Range              | Format                       | Licence   |
| ---------------------------------------------------------------------------------------- | --------------------------- | --------------: | ------------------ | ---------------------------- | --------- |
| [`radiohummingbird-morphagene-glockenspiel/`](radiohummingbird-morphagene-glockenspiel/) | glockenspiel, rubber mallet |              15 | C5 – C7, C major   | 48 kHz, 32-bit float, stereo | CC BY 4.0 |
| [`hollandm-toy-glockenspiel/`](hollandm-toy-glockenspiel/)                               | toy glockenspiel            |              20 | C6 – G7, chromatic | 44.1 kHz, 16-bit, stereo     | CC0       |
| [`mooncubedesign-toy-glockenspiel/`](mooncubedesign-toy-glockenspiel/)                   | toy glockenspiel            |               8 | C6 – C7, C major   | 44.1 kHz, 16-bit, stereo     | CC0       |
| [`jamieblam-metallophone/`](jamieblam-metallophone/)                                     | metallophone, rubber mallet | 13 + 10 shifted | C4 – A5, chromatic | 44.1 kHz, 16-bit, mono       | CC BY 3.0 |

Each pack's `README.md` carries its provenance, the author's own description
verbatim, and a measured table of every file. Read that before using a pack —
the four differ in ways that decide what a fit against them can mean.

## Which pack to reach for

**radiohummingbird** is the one to fit against. It is the only 32-bit float
source here, its tuning is within 5 cents throughout, and its half-lives are the
longest, so the decay is measurable rather than gone in a fifth of a second. Its
one catch is that every note was level-matched to a peak of 0.501 before upload,
so loudness across the scale carries no information.

**hollandm** is the widest chromatic run and the highest, reaching G7 at 3138 Hz.
Five of its files touch full scale, so check for clipping before fitting one.

**mooncubedesign** is the awkward case on purpose: a toy up to 49 cents sharp
with half-lives from 174 ms. Use it to find out what a fit does when the note
name and the frequency disagree.

**jamieblam** is a metallophone rather than a glockenspiel — an octave and a half
lower, and the only mono source. Its ten sharps are pitch shifts of its own
recordings, not recordings, and are kept apart in `pitch-shifted/` for that
reason.

## Attribution

Two of the four licences require credit when the audio is redistributed:

- **radiohummingbird**, ["Morphagene Reel - Glockenspiel Rubber Mallet.wav"](https://freesound.org/s/635381/), [CC BY 4.0](https://creativecommons.org/licenses/by/4.0/)
- **JamieBlam**, ["Metallophone" pack](https://freesound.org/people/JamieBlam/packs/9008/), [CC BY 3.0](http://creativecommons.org/licenses/by/3.0/)

hollandm and mooncubedesign released theirs under CC0, which asks for nothing.
The two zipped packs keep the manifest Freesound generated as
`freesound-pack-license.txt`, which maps every file to its own sound page.

## How the files were made

The two packs that arrived as zips — hollandm and jamieblam — already held one
strike per file. Those were copied byte for byte and renamed.

The two that arrived as a single WAV — mooncubedesign and radiohummingbird —
were cut. Strikes were located by rising edges in block RMS, each edge refined
with `analysis.Onset`, and every note written from 10 ms before its own onset to
10 ms before the next strike, so no cut can clip an attack and every tail runs
until the next bar is struck. The cut copies the source's bytes and its format
chunks unchanged, which is why the radiohummingbird notes are still 32-bit float
and still carry the reel's `bext` chunk.

Every note name in every pack is the nearest equal-tempered note to the measured
fundamental at A4 = 440 Hz, measured over the first second after the onset with
`analysis.Measure` — the same code `glockenspiel analyze` runs. Freesound strips
`#` from an upload's name, so in both zipped packs the naturals and the sharps
arrived sharing one file name; measuring is what told them apart.

**The names are sounding pitch, and that differs from the parent directory.** A
glockenspiel part is written two octaves below what it sounds, and
`../glockenspiel_c5.wav` is named for the written note — its fundamental is
actually 1053.6 Hz, C6 plus 12 cents (see `../README.md`). These packs name the
note the file produces, so `radiohummingbird-morphagene-glockenspiel/c6.wav`
and `../glockenspiel_c5.wav` are about 10 cents apart despite the two names. The measured frequency in each table is the fact; the name is the
label. Note also that a metallophone does not transpose at all, so the jamieblam
pack has only one reading.
