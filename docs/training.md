# Training

This note is the evidence side of Phase 8 in [PLAN.md](../PLAN.md): what the shipped presets score
today, measured through the fit objective's own code, so that every later change to the
objective, the search space or the engine has a number it must not regress. Nothing here was
tuned; it is a reading taken before anything moved.

## How the numbers are taken

`glockenspiel distance` encodes a written preset through the fit's parameter codec, renders it
once per policy, and scores it with the same alignment, gain and metric functions that
`Evaluate` uses for a candidate during a fit. The RMS, log and spectral terms are all reported
from each render, under three policies:

| Policy         | Alignment           | Gain                             | What a fit calls it |
| -------------- | ------------------- | -------------------------------- | ------------------- |
| `raw`          | none                | none                             | `--align=false`     |
| `aligned`      | onset + correlation | none                             | the default         |
| `aligned+gain` | onset + correlation | least-squares scalar divided out | `--normalize-gain`  |

The least-squares gain is measured under every policy and applied only under the third, so the
`gain` column says how much of the candidate the waveform correlation could actually see. The
report also lists the dimensions of the written preset that sit on an edge of the search box,
and any edge the box had to move to contain the preset at all. `just baseline` prints the whole
table below; `glockenspiel distance --json` is the same report for a script.

Two references exist, described in [testdata/reference/README.md](../testdata/reference/README.md):
`legacy_synth_a4.wav`, a 0.56 s render of unrecorded provenance, and `glockenspiel_c5.wav`, a
7.2 s stereo room recording of which only the first second is the struck note. The recording's
first channel is what the objective sees. The rows marked "first second" score a copy of the
recording cut at 1.000 s, which is what `recorded-bar.json` was fitted to according to its commit;
the cut is not a shipped file, because Phase 8.1 makes it a property of the reference loader.

## Baseline, 2026-09-02

Taken at commit `625dc20` plus the `distance` command, Go 1.26.0, mayfly v0.6.0, velocity 100,
44.1 kHz. `rms` is the aligned time-domain error, `rms+gain` the same with the optimal gain
divided out, and `residual` is `rms+gain` relative to the reference RMS over the compared span.
`spectral` is the aligned STFT error in dB.

| Preset                      | Reference             | Note | `rms`   | `rms+gain` | gain     | residual | `spectral` |
| --------------------------- | --------------------- | ---- | ------- | ---------- | -------- | -------- | ---------- |
| `default.json`              | `legacy_synth_a4.wav` | 69   | 0.02561 | 0.02561    | −0.00 dB | −17.5 dB | 8.37       |
| `recorded-bar.json`         | `legacy_synth_a4.wav` | 69   | 0.1259  | 0.1219     | +2.05 dB | −4.0 dB  | 10.14      |
| `default.json`              | `glockenspiel_c5.wav` | 69   | 0.05381 | 0.002078   | −92.6 dB | 0.0 dB   | 4.08       |
| `recorded-bar.json`         | `glockenspiel_c5.wav` | 69   | 0.03449 | 0.002077   | −60.8 dB | 0.0 dB   | 4.68       |
| `recorded-bar.json`         | `glockenspiel_c5.wav` | 60   | 0.04659 | 0.002074   | −52.5 dB | 0.0 dB   | 3.80       |
| `default.json`              | C5, first second      | 69   | 0.1448  | 0.005382   | −92.6 dB | 0.0 dB   | 9.71       |
| `recorded-bar.json`         | C5, first second      | 69   | 0.09211 | 0.005381   | −60.7 dB | 0.0 dB   | 10.37      |
| `recorded-bar.json`         | C5, first second      | 60   | 0.1209  | 0.005371   | −52.3 dB | 0.0 dB   | 8.23       |
| recorded-bar, retune undone | C5, first second      | 69   | 0.09013 | 0.003084   | −26.6 dB | −4.8 dB  | 5.49       |
| recorded-bar, retune undone | `glockenspiel_c5.wav` | 69   | 0.03373 | 0.001250   | −26.6 dB | −4.4 dB  | 2.18       |

"Retune undone" is `recorded-bar.json` with every mode frequency divided by 1.66720, the factor
that puts its second mode back on the recording's 1053.6 Hz fundamental. It is a reconstruction,
not the fitted preset: the fit was retuned by hand, two modes were deleted, and the pre-retune
file was not kept.

Where the presets sit in the box:

- `default.json` has eight of nineteen dimensions on an edge: `input_mix` at 2, all four
  amplitudes at ±2, two Chebyshev gains at 0, and mode 1's frequency multiplier at 10.264 — the
  default box stops at 10, and the codec widened it to exactly contain the preset. Seven of those
  are edges of the box the fit was asked for; the eighth is an edge the widening created.
- `recorded-bar.json` has three of forty-three on an edge: `input_mix` at 0, one Chebyshev gain
  at 2, and mode 11's frequency multiplier at 22.25, again on a widened edge. Its box was moved
  from 10 to 22.25 to contain it.

## What the baseline says

**Against the recording, the time-domain objective sees nothing.** For every shipped preset at
every note the least-squares gain is between −52 and −93 dB, and in two rows it is negative.
That is the correlation between a rendered waveform and the recorded one over the alignment
window, and it is zero: the frequencies differ by enough that the phases drift through many
cycles within the compared span. The two RMS columns then measure two different trivial things.
Without gain normalisation the residual is the render's own energy (compare `rms` with the render
RMS, −25 to −29 dBFS). With it the optimal gain zeroes the candidate and the residual is the
reference's own energy, which is why every `residual` reads 0.0 dB. A fit under either policy is
told nothing about the recording by its objective except through the spectral term.

**Fifteen cents is enough to lose the waveform entirely.** Note 60 is the closest key to undoing
the 1.667 retune and lands 15 cents flat; the gain is still −52 dB. The reconstruction that
undoes the retune exactly reaches −26.6 dB, which is the level difference between a −27.6 dBFS
recording and a −2.6 dBFS render, i.e. real correlation. This is finding 2 of the Phase 8 review
with a number on it: the time-domain terms have a capture range of a few cents per partial.

**The one reproducible fit is 4.8 dB, not 11.1 dB.** The commit that shipped `recorded-bar.json`
records a residual 11.1 dB below the reference RMS. The reconstruction reaches 4.8 dB on the first
second and 4.4 dB on the whole file. The difference is what the hand retune, the two deleted
modes and the unrecorded fit command cost, and it cannot be narrowed further because the fitted
preset itself does not exist in the repository.

**The spectral term is the only one that orders the rows, and it orders them weakly.** It puts
the retune-undone preset (5.49) ahead of the shipped one at either note (10.37, 8.23) and the
default (9.71) on the first second. Its absolute values are dominated by bins neither signal
carries — the whole-file rows are lower than the first-second rows because six seconds of room
tail at the floor agree with six seconds of silence — which is finding 4 of the review.

**The legacy A4 fit is real.** `default.json` against the render it was fitted to sits 17.5 dB
below the reference RMS with the gain at 0.00 dB, at a lag of 51 samples. That is the one number
here that a change to the objective must keep: an objective that scores this preset worse against
this file, relative to alternatives, has lost something the current one measures.

## What the references hold, 2026-09-02

Taken with `glockenspiel analyze` at its defaults: channel zero, automatic cut, peak normalised,
16384-point spectrum, partials within 40 dB of the strongest. Level is against the strongest
partial; attack is the decay line's value at the strike in dB against full scale of the cut,
which is the level a model mode has to reach.

`glockenspiel_c5.wav` cuts to 1.650 s, from frame 303 to 73068, because the tail stops falling
there; the event at 2 s that `testdata/reference/README.md` describes climbs 5.9 dB, just
under the 6 dB that would have named it a second onset. The gain applied is +27.6 dB.

| Partial   | Level    | Attack   | Half-life | T60    |
| --------- | -------- | -------- | --------- | ------ |
| 1053.7 Hz | 0 dB     | −12.4 dB | 620 ms    | 6.18 s |
| 3096.7 Hz | −12.3 dB | −12.6 dB | 118 ms    | 1.18 s |
| 8023.7 Hz | −18.5 dB | −22.5 dB | 173 ms    | 1.72 s |
| 5837.2 Hz | −20.9 dB | −10.8 dB | 54 ms     | 0.54 s |
| 3704.9 Hz | −31.1 dB | −24.2 dB | 68 ms     | 0.68 s |
| 9756.6 Hz | −34.6 dB | −19.9 dB | 48 ms     | 0.48 s |
| 9776.9 Hz | −36.1 dB | −15.5 dB | 38 ms     | 0.38 s |
| 4139.8 Hz | −37.1 dB | −49.0 dB | 577 ms    | 5.75 s |

Three readings. The frequencies agree with the hand table to 0.02%, and the half-lives to within
15%; the fundamental's 620 ms against the hand table's 677 ms is the fit range, since the
envelope steepens from 8.9 to 17 dB/s after the first second as the room takes over from the
bar, and a line over the first second after the envelope's peak sits partly on the steeper
part. The attack column orders the partials differently from the level column: the 5837 Hz
partial is 21 dB down on average but 1.6 dB below the fundamental at the strike, which is the
attack that `testdata/reference/README.md` describes and that a mode amplitude fitted to the
average level cannot reproduce. And the 9.8 kHz pair is the mode the shipping commit for
`recorded-bar.json` says it dropped to survive the retune; below 40 dB the recording shows only
the broadband residue of the attack, dozens of components a few hertz apart that all die within
sixty milliseconds.

`legacy_synth_a4.wav` is one partial: 1756.5 Hz, attack −4.6 dB, half-life 166 ms, and nothing
else within 40 dB of it. Whatever rendered it used one mode of any weight.

## The composite objective on the shipped presets, 2026-09-02

Taken with `glockenspiel distance` after Phase 8.2, which is also where the reference loader
entered the path: every row below scores against the reference as `fit` now reads it — one
channel, cut to the first strike, peak-normalised — so the C5 rows are the 1.650 s cut at +27.6
dB, not the raw file. `just baseline` prints these rows. Each term is a raw number in its own
unit; the score is the `balanced` profile's, in `[0, 1]`, where each term is scaled by its norm
through `x/(1+x)` and the terms are averaged by weight. The row at note 60 is the key that comes
closest to undoing the hand retune, and the last row is the six-mode preset
`PresetFromAnalysis` writes from the recording's own measurement, at that note.

| Preset                      | Reference             | Note | cents | level  | decay    | missing | extra | fine    | coarse  | envelope | slope     | waveform | gain    | wf gain  | score |
| --------------------------- | --------------------- | ---- | ----- | ------ | -------- | ------- | ----- | ------- | ------- | -------- | --------- | -------- | ------- | -------- | ----- |
| `default.json`              | `legacy_synth_a4.wav` | 69   | 0.01  | 0.0 dB | 0.03 oct | 0.00    | 0.00  | 4.7 dB  | 6.1 dB  | 0.28 dB  | 1.0 dB/s  | 0.13     | +0.1 dB | +0.0 dB  | 0.135 |
| `recorded-bar.json`         | `legacy_synth_a4.wav` | 69   | 0.02  | 0.0 dB | 1.08 oct | 0.00    | 4.71  | 15.0 dB | 15.4 dB | 2.08 dB  | 14.5 dB/s | 0.63     | +4.3 dB | +2.1 dB  | 0.431 |
| `default.json`              | `glockenspiel_c5.wav` | 69   | n/a   | n/a    | n/a      | 1.00    | 0.17  | 16.9 dB | 20.7 dB | 3.51 dB  | 23.0 dB/s | 1.00     | −0.8 dB | −64.8 dB | 0.595 |
| `recorded-bar.json`         | `glockenspiel_c5.wav` | 69   | 60.5  | 8.3 dB | 0.28 oct | 0.56    | 0.58  | 17.6 dB | 21.4 dB | 1.61 dB  | 6.4 dB/s  | 1.00     | +3.1 dB | −33.2 dB | 0.555 |
| `recorded-bar.json`         | `glockenspiel_c5.wav` | 60   | 15.1  | 1.8 dB | 0.46 oct | 0.40    | 0.31  | 14.8 dB | 13.7 dB | 0.64 dB  | 0.7 dB/s  | 1.00     | +0.5 dB | −24.8 dB | 0.417 |
| six modes from the analysis | `glockenspiel_c5.wav` | 60   | 1.5   | 3.5 dB | 0.14 oct | 0.14    | 0.00  | 9.0 dB  | 10.1 dB | 1.08 dB  | 1.4 dB/s  | 0.73     | −6.4 dB | −9.7 dB  | 0.24  |

**The `score` column of this table is superseded**, and its last row is an error; every other
column still holds. See "The composite objective re-taken, 2026-09-05" below, which reproduces
every term and reconstructs five of the six scores exactly from the weights of the day.

The norms in `optimizer.DefaultNorms` — 10 cents, 6 dB of level, half an octave of decay, a
half of the partial weight missing, once the weight extra, 10 dB for either spectral term, 3 dB
of envelope, 10 dB/s of slope, a residual of one half — were set against these rows: on every
row a shipped preset scores against the reference it was fitted to or at its nearest note, each
term sits between a tenth of its norm and about twice it, where the score still moves when the
term does. The one term that saturates anywhere is `extra` for the twelve-mode preset against a
single-partial render, which is the pairing that should.

**What the table says.**

- **The one real fit reads as one.** `default.json` against the A4 render matches its single
  partial to a hundredth of a cent, has no partial missing or extra, and its waveform residual is
  the −17.5 dB of the legacy baseline. Its spectral terms are the largest thing left, at 5–6 dB,
  and they are the render's own residue above the 60 dB floor.
- **The shipped recording fit is ordered where the listener would put it.** At note 60 the
  recorded bar matches four of the recording's eight partials at 15 cents and 1.8 dB; at note
  69 it matches three at 60 cents; the default preset matches none. The legacy objective scored
  these three rows within 4% of each other with the gain at −25 to −65 dB.
- **The partial term is what the shipped preset fails.** Its 40% missing weight is the 8 kHz,
  9.8 kHz and 4.1 kHz partials it has no mode for; its 31% extra is the two modes near 5.16 kHz
  that the visibility rule keeps and the 3705 Hz partial's match does not use. The seed written
  from the analysis has nothing extra, 14% missing (the two partials past its six modes), and
  its six matches within 1.5 cents; on the partial terms alone it scores 0.19 against 0.43.
- **The seed also correlates.** Its waveform gain is −9.7 dB against the shipped preset's
  −24.8 dB, and its residual 0.73 against 1.00: six modes placed by measurement recover a
  quarter of the waveform's energy on a recording where a hand-fitted preset recovers none.
  That is the first number in this repository that says the model can reach the recording.

The legacy terms, re-taken through the loader for the same rows, for comparison with the
baseline above: the aligned `rms` of every C5 row moves to 0.12–0.15 because the reference now
peaks at full scale, and `aligned+gain`'s spectral term climbs to 32–73 dB because a −25 to
−65 dB gain shifts every candidate bin under the floor. Nothing there changes a reading of the
baseline.

Two sentences of that paragraph have since been overtaken, and both are corrected in
"The legacy metrics re-taken, 2026-09-05" below. The gain columns are **not** unchanged — the
loader's +27.6 dB normalisation moves every C5 row's gain by exactly that much — and
`aligned+gain`'s spectral term no longer climbs, because Phase 8.10 moved the floor to sit under
the gained candidate rather than the raw one.

## The legacy metrics re-taken, 2026-09-05

Taken at revision `9704ed4`, `modified false`, Go 1.26.0, mayfly v0.7.1, go-cma-es v0.1.0,
velocity 100, 44.1 kHz, through `glockenspiel distance --json`. The "first second" rows use
`--window 1s`, which cuts one second after the onset where the 2026-09-02 rows used a copy of the
file cut at 1.000 s; the onset sits at frame 303, so the two spans differ by seven milliseconds.
"Retune undone" is rebuilt the same way, every mode frequency divided by 1.66720, which puts mode
two on 1053.60 Hz.

| Preset                      | Reference             | Note | `rms`   | `rms+gain` | gain      | residual | `spectral` | `spectral+gain` |
| --------------------------- | --------------------- | ---- | ------- | ---------- | --------- | -------- | ---------- | --------------- |
| `default.json`              | `legacy_synth_a4.wav` | 69   | 0.02557 | 0.02557    | +0.00 dB  | −17.6 dB | 8.37       | 8.37            |
| `recorded-bar.json`         | `legacy_synth_a4.wav` | 69   | 0.1259  | 0.1219     | +2.05 dB  | −4.0 dB  | 10.13      | 10.24           |
| `default.json`              | `glockenspiel_c5.wav` | 69   | 0.1518  | 0.1024     | −64.83 dB | 0.0 dB   | 15.40      | 14.50           |
| `recorded-bar.json`         | `glockenspiel_c5.wav` | 69   | 0.1242  | 0.1024     | −33.20 dB | 0.0 dB   | 15.30      | 14.55           |
| `recorded-bar.json`         | `glockenspiel_c5.wav` | 60   | 0.1369  | 0.1022     | −24.76 dB | 0.0 dB   | 12.01      | 12.74           |
| `default.json`              | C5, first second      | 69   | 0.1929  | 0.1286     | −64.80 dB | 0.0 dB   | 18.92      | 17.90           |
| `recorded-bar.json`         | C5, first second      | 69   | 0.1568  | 0.1286     | −33.09 dB | 0.0 dB   | 18.55      | 17.91           |
| `recorded-bar.json`         | C5, first second      | 60   | 0.1715  | 0.1284     | −24.63 dB | 0.0 dB   | 15.05      | 15.99           |
| recorded-bar, retune undone | C5, first second      | 69   | 0.07475 | 0.07393    | +0.96 dB  | −4.8 dB  | 15.30      | 15.25           |
| recorded-bar, retune undone | `glockenspiel_c5.wav` | 69   | 0.06059 | 0.05987    | +1.03 dB  | −4.7 dB  | 12.16      | 12.12           |

Three readings, and only one of them is about Phase 8.10.

**The residual is unchanged wherever the compared span is, and that is the column to trust.**
−17.6 against the baseline's −17.5, −4.0 against −4.0, 0.0 against 0.0, −4.8 against −4.8. The
residual is the one legacy column that divides out both the reference's level and the candidate's,
so the loader's normalisation cannot move it, and it did not.

The single exception proves the rule rather than breaking it: the last row reads −4.7 against the
baseline's −4.4, and the reason is the span, not the arithmetic. That row is the only one where
"whole file" meant different things on the two days — the baseline scored the raw 7.2 s recording,
and the loader now cuts to the 1.650 s strike, so six seconds of room tail have left the
comparison. The other whole-file C5 rows hide the same change behind a residual of 0.0 dB, which
is saturated either way.

Anything the baseline concluded from the residual still stands, including that the one reproducible
fit is 4.8 dB rather than the shipping commit's 11.1 dB.

**The `rms` and `gain` columns moved by the loader, not by the objective.** Every C5 row's gain is
exactly +27.6 dB above its 2026-09-02 value — −92.6 to −64.83, −60.8 to −33.20, −52.5 to −24.76,
−26.6 to +0.96 — which is the peak normalisation the reference loader applies, entering the path in
Phase 8.2 after the baseline was taken. The A4 rows, where the loader applies +0.0 dB, are
unchanged to four figures. So these two columns are stale for an 8.2 reason and were already known
to be: the paragraph above this section said so, and got the gain half of it backwards.

**`spectral+gain` is where Phase 8.10 shows, and it is the whole point of the fix.** Under the old
arithmetic that column climbed to 32–73 dB on the C5 rows, because a −25 to −65 dB gain was applied
_after_ the floor and shifted every candidate bin under it, leaving a flat plateau. It now reads
14.50 at a gain of −64.83 dB, against 15.40 for the same row without gain normalisation — a
difference of nine tenths of a decibel where there used to be a difference of fifty. The floor now
sits under the gained candidate, so normalising the gain no longer flattens the spectrum, and the
term measures the spectrum at every level instead of measuring the floor.

## The composite objective re-taken, 2026-09-05

All six rows through the composite, against the 2026-09-02 table above. The seed row is rebuilt
with `PresetFromAnalysis` from `recorded-bar.json` as the template, the C5 measurement, note 60 and
six modes, which is what that row was.

**Every term is unchanged to the precision that table prints.** `cents`, `level`, `decay`,
`missing`, `extra`, `fine`, `coarse`, `envelope`, `slope`, `waveform`, `gain` and `wf gain` all
reproduce on all six rows, the largest discrepancy being 17.6 against 17.5 dB of `fine` on one
row. That is Phase 8.10's own scope claim holding exactly: both shipped presets render within a
few decibels of their references, the floor never bit for them, and the fix could not move them.

**The `score` column moved on every row, and 8.10 is not why.**

| Row                                          | 2026-09-02 | reconstructed | 2026-09-05 |
| -------------------------------------------- | ---------: | ------------: | ---------: |
| `default.json` vs `legacy_synth_a4.wav`, 69  |      0.135 |        0.1352 |     0.1789 |
| `recorded-bar.json` vs `legacy_synth_a4`, 69 |      0.431 |        0.4313 |     0.4402 |
| `default.json` vs `glockenspiel_c5.wav`, 69  |      0.595 |        0.5953 |     0.6113 |
| `recorded-bar.json` vs `glockenspiel_c5`, 69 |      0.555 |        0.5545 |     0.5669 |
| `recorded-bar.json` vs `glockenspiel_c5`, 60 |      0.417 |        0.4167 |     0.4404 |
| six modes from the analysis, 60              |       0.24 |        0.3085 |     0.3331 |

The objective gained an eleventh term after the table was taken. `onset_db` is not in that table's
header, and `balanced` was reweighted to make room for it, in commit `c7f0ecf`: cents 0.12 to 0.11,
level and decay 0.08 to 0.07, extra 0.06 to 0.05, each spectral term 0.125 to 0.11, envelope 0.15
to 0.13, decay slope 0.10 to 0.09, onset in at 0.10, missing and waveform unchanged.

The "reconstructed" column is the 2026-09-05 terms scored under those **old** weights, and it is
what makes this a demonstration rather than an assertion: five of the six rows come back to the
digit the 2026-09-02 table printed. Two rules had to be respected to get there. The old weights,
obviously; and the rule that a term which could not be measured is dropped from the weighted mean
rather than counted as zero — the `default.json` C5 row matches no partial at all, so its cents,
level and decay are excluded and the remaining weights renormalise over 0.72, giving 0.5953 against
the printed 0.595. The current objective still excludes them the same way: 0.4585 over 0.75 is the
0.6113 it reports today.

So five rows moved for exactly one reason, and it is not Phase 8.10. Every term underneath the
score reproduced; the score moved because the objective's own definition of `balanced` changed
underneath it. A score that moves while every term holds is a change in the scoring rule, not a
change in the model.

**The sixth row was wrong when it was written.** The analysis seed's 0.24 does not reconstruct: the
same terms under the same old weights give 0.3085, and no exclusion rule closes a gap that large —
the row matches six of eight partials, so nothing is excluded. The numbers around it in the prose
are sound and reproduce exactly: the seed scores 0.1875 on the partial terms alone against the
shipped preset's 0.4246, which is the "0.19 against 0.43" that paragraph quotes. It is the
`balanced` cell alone that is wrong, and both places it appears hedge it — 0.24 to two decimals
where every other row carries three, and "≈0.24" in the 8.3 table below.

The correction does not overturn what the row was cited for. The seed still beats the shipped
preset it was compared against, 0.3085 against 0.4167 under the arithmetic of the day and 0.3331
against 0.4404 today. It overstated the margin by about a third. It is the one number in this file
that re-measurement found to be an error rather than a casualty of a changed definition.

## What the references hold, re-measured 2026-09-05

`glockenspiel analyze` reproduces the 2026-09-02 measurement of both references **exactly**: the
same cut (frame 303 to 73068, 1.650 s, +27.6 dB), the same fundamental at 1053.7 Hz, and all eight
partials at the same level, attack, half-life and T60 to the digit; `legacy_synth_a4.wav` likewise
at 1756.5 Hz, −4.6 dB, 166 ms. This is the expected result and is recorded because it is the
control: the reference side of every measurement in this file is unaffected by anything Phase 8.9
or 8.10 changed, so a table that moved, moved on the candidate side. The output now also carries an
`amplitude` column that the 2026-09-02 table does not.

## The search space after 8.3, 2026-09-02

Phase 8.3 reshaped what a fit searches, and two measurements say what that bought.

**The acceptance test.** `TestAColdFitFromTheRealBoxRecoversASixModeTarget` renders a
six-mode target at glockenspiel-like ratios (1000 to 14500 Hz, half-lives 400 down to 50 ms),
measures it, seeds the modes from the measurement, and runs Mayfly (DESMA, population 12, 30
iterations, about 1300 evaluations) from a template that is wrong in its dry mix and its
lowpass, in the default box narrowed by the fundamental and the note — no hand-set bounds.
Twelve seeds, twelve recoveries: every mode within 0.02 cents and 0.1% of its half-life. The
seed itself scores 0.0115 on `balanced` and the twelve fits end at 0.0110 to 0.0113, so the
search neither loses the incumbent nor moves far from it; what it refines is the mix and the
lowpass. The test takes twenty seconds and is skipped under `-short`.

**The first fit through the new space on the recording.** One run, not a campaign — the
campaign harness is 8.5 — recorded here because it is the first fit from a real recording that
was not hand-tuned afterwards:

```
glockenspiel fit --reference testdata/reference/glockenspiel_c5.wav --note 72 \
  --optimizer mayfly --mayfly-pop 20 --seed 1 --time-budget 90s --max-iter 100000
```

| Row                                | `balanced` | missing | extra | fine dB | coarse dB | env dB | waveform | matched |
| ---------------------------------- | ---------: | ------: | ----: | ------: | --------: | -----: | -------: | ------: |
| `recorded-bar.json`, note 60 (8.2) |      0.417 |    0.40 |  0.31 |     6.6 |       8.1 |    1.8 |     1.00 |   4 / 8 |
| seed from the analysis, note 60    |      ≈0.24 |    0.14 |  0.00 |         |           |        |     0.73 |   6 / 8 |
| fit from the seed, note 72, 90 s   |      0.180 |    0.44 |  0.00 |     2.0 |       2.9 |    1.0 |     0.67 |   5 / 8 |

**Two of this table's three `balanced` figures are superseded.** The seed's ≈0.24 is an error, and
should read 0.3085 under the weights of the day; the shipped preset's 0.417 is right for its day
and reads 0.4404 today. Both are re-taken in "The composite objective re-taken, 2026-09-05" above.
The 90 s fit's 0.180 is a third case again: it was budgeted in wall clock under mayfly v0.6.0, so
it cannot be reproduced at all — see the note on budgets below.

The fit halves every spectral and envelope term against the shipped preset and matches the
partials it does match to 0.2 cents and 0.4 dB. It also says, through the pinned report, where
the model stops it:

- **Seven of thirty dimensions finished on a bound**: the dry mix at its maximum, the four
  highest modes' amplitudes at 2, and two shaper gains at zero. The fit pulled the excitation
  lowpass down to 3.5 kHz, where its 12 dB per octave costs the 4.9 to 8.2 kHz partials 6 to
  15 dB, and the amplitude ceiling cannot pay that back — so three of the recording's eight
  partials count as missing and the search stacked two modes four hertz apart at 8.2 kHz to
  make one loud enough. That is the fake-beat cluster of the review, reappearing for a reason
  the objective can now name: the amplitude range and the lowpass together bound the spectral
  tilt the model can produce. It is model work, not search work, and the review's item 15
  gains a second half.
- **The fundamental's half-life sits at 738 ms against a 743 ms ceiling.** The recording's
  677 ms at C5 is 805 ms authored at note 69, above what a preset at that note may carry, so the
  seed is clamped and the fit stays there. A preset that is to hold this bar has to be authored
  lower, or the five-second policy behind `DecayMsValidationMax` has to move.

Nothing here is a campaign result. It is one seed at one budget, and 8.5 exists because one
run is not evidence; but it is the first run whose report says what to do next.

## Engines after 8.4, 2026-09-02

Phase 8.4 changed which engines exist and which one runs by default. Nothing here chooses a
default by measurement; that is 8.5's campaign and 8.6's decision.

**Dragonfly is out.** MayFlyCircleFit measured it against the same paired-block design its other
optimizer reports use and it lost decisively: zero of twelve blocks in every arm, t = −16.8, and
the best of 576 restarts worse than every baseline block
(`docs/dragonfly-poc-report.md` in MayFlyCircleFit). That is not a close call a different
objective could reverse, so Dragonfly is not a candidate in 8.5's designs and no wrapper for it
exists here.

**Every Mayfly number in this file's earlier sections is incomparable to a run taken now.**
Mayfly moved from v0.6.0 to v0.7.1 on 2026-09-02. v0.7.0 was a correctness release that changed
the update rules themselves: standard attraction uses the paper's scalar Cartesian distances,
crossover retains offspring sex, mutation draws from the matching incumbent population, females
take part in global-best and termination decisions, and non-finite objective values stop being
rewarded. v0.7.1 changed no behaviour. Every Mayfly figure recorded above, and the benchmark
rows in [user-guide.md](user-guide.md), was measured under v0.6.0: a seeded trajectory from that
version does not reproduce, and the costs it reached are not a baseline for v0.7.1. The 90 s fit
in "The search space after 8.3" is one of those numbers.

**go-cma-es is pinned at v0.1.0, deliberately.** That version has a measured defect above a
population of 256 in separable mode and 1024 in block mode, where `ActiveCMA` goes inert and
covariance memory is dropped. This fit runs a population of twelve to fourteen by default and
`--cmaes-lambda` is not expected past 64, so the ceiling is far above anything a fit here
reaches. v0.2.0 fixes it and changes the sampling trajectory with it, which would split the 8.5
campaign's numbers into a before and an after, so the bump waits until 8.6's tables exist. It is
recorded under "Deferred" in [PLAN.md](../PLAN.md).

### A smoke run of the three engines

One 60 s run of each engine on the C5 recording at note 72 under `balanced`, seed 1, on twelve
hardware threads. **This is a smoke test, not a comparison.** One run per engine at one budget
on one seed says the wiring works and the reports are readable; it does not say which engine
finds a better fit, and no error bar exists here to say whether any gap between two rows is
larger than the spread of one engine over seeds. 8.5's campaign is the instrument that answers
that question, over paired seed blocks at a matched evaluation budget.

```
glockenspiel fit --reference testdata/reference/glockenspiel_c5.wav --note 72 \
  --optimizer cmaes --seed 1 --time-budget 60s --max-iter 100000 \
  --work-dir out/smoke/cmaes-sep --output out/smoke/cmaes-sep.json

glockenspiel fit --reference testdata/reference/glockenspiel_c5.wav --note 72 \
  --optimizer cmaes --cmaes-covariance block --seed 1 --time-budget 60s --max-iter 100000 \
  --work-dir out/smoke/cmaes-block --output out/smoke/cmaes-block.json

glockenspiel fit --reference testdata/reference/glockenspiel_c5.wav --note 72 \
  --optimizer mayfly --mayfly-pop 20 --seed 1 --time-budget 60s --max-iter 100000 \
  --work-dir out/smoke/mayfly --output out/smoke/mayfly.json
```

| Engine                           | `balanced` | Restarts or rounds | Evaluations | Pinned  |
| -------------------------------- | ---------: | ------------------ | ----------: | ------- |
| `cmaes`, separable (the default) |   0.273463 | `restarts=2`       |      24,583 | 0 of 30 |
| `cmaes`, block covariance        |   0.261791 | `restarts=1`       |      22,903 | 0 of 30 |
| `mayfly`, DESMA, population 20   |   0.224395 | one round          |      27,290 | 5 of 30 |

**These three rows are budgeted in wall clock, and that makes them unreproducible in principle.**
`--time-budget 60s` buys however many evaluations the machine can afford in a minute, so the score
a row reaches is a reading of the hardware and of what else was running on it as much as of the
engine. Re-taking them on a different day gives a different number with nothing wrong with either.
The same applies to the 90 s fit above. This is why 8.5's campaign matches arms on **evaluations**
instead, and why the campaign tables further down survive re-measurement while these do not: an
evaluation-budgeted score is a property of the search, a time-budgeted one is a property of the
afternoon. Re-take these under an evaluation budget or read them as qualitative; they are left as
they were taken, because refreshing them would only produce a differently unreproducible number.

All three stopped on `time_budget` rather than a convergence criterion, and all three seeded
eight modes from the recording's partials. The reference was read the same way in every run:
`channel first of 2, cut 303..73068 (1.650 s, tail reached floor), gain +27.59 dB`. Both CMA-ES
runs took Hansen's λ of 14 at the thirty dimensions eight modes encode, σ 0.3.

Two things the rows do say, because they are structural rather than statistical. The CMA-ES runs
finished with nothing on a bound and one of the eight reference partials matched; the Mayfly run
finished with five dimensions pinned and six of eight partials matched, which is the seeded
population doing what 8.3 built it for. And the Mayfly run reads 0.224 against the 0.180 the
90 s fit above reads, but that fit ran under v0.6.0 for half again the budget, so the pair is an
illustration of the incomparability warning rather than a measurement of either.

## The campaign harness, 2026-09-02

Phase 8.5 built the instrument the smoke run above says is missing. `cmd/glockenspiel-campaign`
plans, runs, collects and analyses a designed comparison: every arm of a design runs on every one
of a set of paired seed blocks, at a matched evaluation budget, and every job leaves a run
directory that records what it was given and what it found.
[docs/campaign.md](campaign.md) is the harness; what follows is only what it means for the
numbers in this file.

Two designs are registered. `engine-shape` puts `mayfly-single`, `mayfly-r16`, `sep-cmaes-r`,
`blk-cmaes-r` and `sep-cmaes-ipop` on the C5 recording at note 72 under `balanced`, twelve blocks
of five arms at 24,000 evaluations each, with `blk-cmaes-r` against `mayfly-r16` as the
registered primary contrast and two secondary ones. `seed-hunt` then takes whichever CMA-ES arm
won and compares it at Hansen's default population against twice it over forty eight blocks,
descriptively.

Arms are matched on evaluations rather than on time, and scored at the best cost their trace
recorded at or below the cap, because a generation is atomic and a run may overrun the budget by
one of them. The score a job finished with is kept in its own column, so the tables below say
what was matched and what was spent.

`smoke`, four jobs of 1,200 evaluations on the synthetic A4 reference, is the wiring check that
proves plan, run, collect and analyze agree about the files between them; `just campaign-smoke`
runs it in seconds. Two blocks of two arms cannot separate anything whatever the budget, so its
output is not reproduced here.

The report is rebuilt from `results.csv` plus the registered design, which supplies the block
count, budget, profile and contrast family; the manifest is the frozen record of what ran and is
not consulted by `analyze`.

## Engine shape, 2026-09-03

**This campaign has been re-run and its ranking did not survive.** It was taken under a ten-term
objective — its `results.csv` has no `onset_db` column at all — and before Phase 8.10 corrected
the floor. "Engine shape re-taken, 2026-09-05" below has the current numbers and the decision they
force; what follows is kept because the mechanisms it identifies are still sound and because it is
the evidence the shipped default was chosen on.

The registered `engine-shape` campaign, run from commit `4389279` on twelve hardware threads:
twelve paired seed blocks of five arms at 24,000 evaluations each, on the C5 recording at note 72
under `balanced`. Sixty jobs, about an hour. Every row stopped on `max_evaluations` having spent
its budget, no two blocks of any arm share a score or a parameter vector, and the build records
`modified false` — the four things that have to hold before the table is read at all.

```
design engine-shape: 12 blocks, budget 24000 evaluations, balanced on testdata/reference/glockenspiel_c5.wav, revision 43892797d4d0b821a33ad6527595e9ffc5a885e9

Backend and restart shape on the C5 recording, twelve blocks of five arms at 24,000 evaluations.

### Table 1: arms against mayfly-r16

| arm | mean | sd | median | best | gain vs mayfly-r16 | t (df=11) | p | Holm | blocks won |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| mayfly-single | 0.220173 | 0.047645 | 0.189710 | 0.180888 | n/a | n/a | n/a | n/a | n/a |
| mayfly-r16 | 0.213820 | 0.013269 | 0.207915 | 0.198439 | control | control | control | control | control |
| sep-cmaes-r | 0.253491 | 0.027624 | 0.258856 | 0.201767 | -0.0397 | -4.00 | 0.00208 | reject | 2/12 |
| blk-cmaes-r | 0.275572 | 0.024958 | 0.284889 | 0.225272 | -0.0618 | -10.68 | 0.00000 | reject | 0/12 |
| sep-cmaes-ipop | 0.279348 | 0.031483 | 0.281398 | 0.201767 | n/a | n/a | n/a | n/a | n/a |

### Table 1: arms against mayfly-single

| arm | mean | sd | median | best | gain vs mayfly-single | t (df=11) | p | Holm | blocks won |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| mayfly-single | 0.220173 | 0.047645 | 0.189710 | 0.180888 | control | control | control | control | control |
| mayfly-r16 | 0.213820 | 0.013269 | 0.207915 | 0.198439 | +0.0064 | +0.49 | 0.63681 | retain | 5/12 |
| sep-cmaes-r | 0.253491 | 0.027624 | 0.258856 | 0.201767 | n/a | n/a | n/a | n/a | n/a |
| blk-cmaes-r | 0.275572 | 0.024958 | 0.284889 | 0.225272 | n/a | n/a | n/a | n/a | n/a |
| sep-cmaes-ipop | 0.279348 | 0.031483 | 0.281398 | 0.201767 | n/a | n/a | n/a | n/a | n/a |

### Table 2: score by block

| block | seed | mayfly-single | mayfly-r16 | sep-cmaes-r | blk-cmaes-r | sep-cmaes-ipop |
| --- | --- | --- | --- | --- | --- | --- |
| 0 | 121000 | 0.251968 | **0.202610** | 0.284690 | 0.266343 | 0.290383 |
| 1 | 121001 | 0.311349 | **0.230466** | 0.273061 | 0.282795 | 0.273236 |
| 2 | 121002 | **0.187488** | 0.218110 | 0.244139 | 0.284029 | 0.270397 |
| 3 | 121003 | 0.214114 | **0.202674** | 0.259230 | 0.237646 | 0.300835 |
| 4 | 121004 | **0.189455** | 0.203439 | 0.283847 | 0.288879 | 0.300291 |
| 5 | 121005 | **0.180888** | 0.227432 | 0.213447 | 0.295641 | 0.288340 |
| 6 | 121006 | **0.189965** | 0.203048 | 0.248837 | 0.298293 | 0.274456 |
| 7 | 121007 | **0.185446** | 0.209866 | 0.258483 | 0.285748 | 0.258483 |
| 8 | 121008 | 0.281547 | **0.205963** | 0.268501 | 0.251116 | 0.268501 |
| 9 | 121009 | **0.183175** | 0.198439 | 0.226073 | 0.225272 | 0.333439 |
| 10 | 121010 | 0.280530 | 0.234612 | **0.201767** | 0.289275 | **0.201767** |
| 11 | 121011 | **0.186156** | 0.229184 | 0.279818 | 0.301822 | 0.292047 |

### Table 3: best of each arm

| arm | best | block | seed | within 5% of best | median | q25 | q75 | mean evaluations | spent at best |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| mayfly-single | 0.180888 | 5 | 121005 | 6/12 | 0.189710 | 0.185979 | 0.259108 | 24003 | 98.8% |
| mayfly-r16 | 0.198439 | 9 | 121009 | 6/12 | 0.207915 | 0.202955 | 0.227870 | 24009 | 53.7% |
| sep-cmaes-r | 0.201767 | 10 | 121010 | 1/12 | 0.258856 | 0.239623 | 0.274750 | 24000 | 57.7% |
| blk-cmaes-r | 0.225272 | 9 | 121009 | 1/12 | 0.284889 | 0.262536 | 0.290867 | 24000 | 48.4% |
| sep-cmaes-ipop | 0.201767 | 10 | 121010 | 1/12 | 0.281398 | 0.269923 | 0.294108 | 24000 | 61.1% |

Holm step-down over 3 paired contrasts at a family-wise alpha of 0.05.
the registered primary contrast is blk-cmaes-r against mayfly-r16.
```

The rows are committed as
[data/engine-shape-2026-09-03-results.csv](data/engine-shape-2026-09-03-results.csv), so the table
above rebuilds without rerunning the campaign:
`glockenspiel-campaign analyze --csv docs/data/engine-shape-2026-09-03-results.csv`. The
unqualified `data/engine-shape-results.csv` is always the current run, which is now the
2026-09-05 one.

**The registered primary contrast fails, and so does the secondary one.** Block-covariance CMA-ES
is not better than the mayfly arm the project ships; it is worse by 0.062 of score in twelve
blocks of twelve, t = −10.68, p < 0.0001 after Holm. Separable CMA-ES — which is what the CLI
defaulted to — loses too, by 0.040, t = −4.00, p = 0.002, winning two blocks of twelve. This is
the contrast the phase was built to run and it returns the opposite of its hypothesis. **No arm
in this design beats `mayfly-r16`.**

That block covariance loses is worth recording against the expectation that put it in the design.
MayFlyCircleFit measured block covariance with one block per entity winning eleven of twelve
blocks, and the argument for it here was that a bar's per-mode `(amplitude, frequency, decay)`
triples are the same shape. They are not: a mode's frequency interacts with every other mode's
through the partial matching and both spectral terms, so grouping the covariance by mode
withholds exactly the correlations this objective has.

**Restarts buy consistency more than they buy a better mean.** `mayfly-r16` against
`mayfly-single` is +0.0064, t = +0.49, p = 0.64 — nothing on the mean. But the standard
deviations are 0.0133 against 0.0476, and the medians go the other way, 0.2079 against 0.1897.
The single long run wins seven blocks of twelve and holds the best result in the campaign,
0.180888 at seed 121005, while also producing the three worst blocks the two arms have between
them. Sixteen rounds trade that tail away. Which arm is preferable depends on how a run is used,
and the table says so rather than resolving it: for one blind run take the consistent arm, and
for a preset that will be shipped take the best of several runs.

**Splitting the budget works; the population ladder does not.** Both restarting CMA-ES arms took
their best from a restart other than the first in nine blocks of twelve. `sep-cmaes-ipop` took
its best from restart 0 in **twelve blocks of twelve**: the doubled population never once
improved on the first run, and the arm is the worst of the five. That is MayFlyCircleFit's IPOP
finding, only stronger — it recorded restart 0 holding the best in six blocks of twelve. The
ladder spends the budget and returns nothing, so `--cmaes-lambda-growth` is not a flag a recipe
here should set.

`mayfly-r16` tells the same story from the other side. Its best came from round 0 — the warm round
seeded from the reference's own measured partials — in one block of twelve; the other eleven came
from a cold round, spread from round 2 to round 13. The analysis seed is a good starting point
and is not the basin the answer is in. The round index is inferred from the schedule rather than
read: the trace's `restart` key is a CMA-ES quantity and is zero on every mayfly row, which is a
gap in the instrument and not in the result.

**The budget, not the engine, binds the single long run.** `mayfly-single` reached its best at
98.8% of its budget on average — it was still improving when the cap cut it. The restarting arms
plateau far earlier: 57.7% for `sep-cmaes-r`, 53.7% for `mayfly-r16`, 48.4% for `blk-cmaes-r`. So
the one arm holding the campaign's best result is the one arm this budget did not let finish, and
its figure understates it by an amount this design cannot measure. Settling that needs a design
running `mayfly-single` alone at two or three budgets; it is not this design.

**Two defects the campaign found before it could be read.** Both are recorded here because every
restarting figure taken before them is void, and because neither would have shown up in a table.

Round and restart random streams were derived from the run's seed arithmetically — `seed − k` for
a mayfly round, `seed + k` and `seed − k − 1` for a CMA-ES run and its cold mean. That keeps one
run's streams apart from each other, which is all it was written to do, and it does not keep two
runs apart: a campaign block's seed is `SeedBase + block`, so block _b_'s round _k_ was block
_b+1_'s round _k∓1_, and a sixteen-round arm's twelve blocks shared fourteen of their fifteen
restarts. It surfaced as two blocks of `mayfly-r16` writing a bit-identical preset. The first
table produced under it had `mayfly-r16` at an sd of 0.0084 rather than 0.0133 and put separable
CMA-ES at p = 0.055 "retain" rather than p = 0.002 "reject" — a coupled design understates its
own spread and flatters the arm being tested against it. `internal/optimizer/randomstream.go`
now mixes the seed with a family label. The arms whose result comes from index zero of the
primary family — `mayfly-single` and `sep-cmaes-ipop` — are bit-identical across the fix, which
is the check that it changed only what it should.

Separately, `--time-budget` had to be positive, so no CLI fit could ever be bounded by
`--max-evals` and no hand-run fit could reproduce a campaign arm however its cap was set. Zero
now means "no clock"; `--max-iter` still bounds the run.

`seed-hunt` was **not** run. It refines a winning CMA-ES arm by construction — `SeedHunt` refuses
a non-cmaes arm — and no CMA-ES arm won, so its precondition is unmet; Part B of the review
already lists λ as a null not to re-derive. The design stays registered and `--winner` still
takes an arm, so it runs the day a CMA-ES arm wins something.

## The default shape, and what the refits found, 2026-09-03

**Both halves of this section have been overtaken.** The promotion it records was decided on the
2026-09-03 campaign, whose ranking did not survive re-measurement, and the reason the refit did not
ship — that the schema had no output gain — was removed by Phase 8.9. Both are re-decided in
"Engine shape re-taken, 2026-09-05" and "The second reference re-taken, 2026-09-05" below. What
stands here unchanged is the materiality threshold, which is a rule rather than a measurement.

### The second reference

The campaign covers the C5 recording. The promotion rule asks about both references, so the same
two shapes were run on `legacy_synth_a4.wav` over eight paired seeds at the campaign's budget,
all sixteen fits stopping on `max_evaluations`:

| seed | `sep-cmaes-r` | `mayfly-r16` | better |
| ---- | ------------- | ------------ | ------ |
| 1    | 0.064798      | 0.065294     | cmaes  |
| 2    | 0.064585      | 0.065120     | cmaes  |
| 3    | 0.083878      | 0.065215     | mayfly |
| 4    | 0.065020      | 0.065737     | cmaes  |
| 5    | 0.064964      | 0.067654     | cmaes  |
| 6    | 0.064819      | 0.064667     | mayfly |
| 7    | 0.065809      | 0.064921     | mayfly |
| 8    | 0.064770      | 0.065452     | cmaes  |
| mean | 0.067330      | 0.065507     |        |
| sd   | 0.006696      | 0.000925     |        |

No difference worth calling one: paired t = 0.75, mayfly ahead on the mean and behind on five
seeds of eight. The A4 render is a single partial and both shapes solve it. What the column does
show is the same variance story as the recording — an sd of 0.0009 against 0.0067, and the one
bad CMA-ES seed (0.0839, seed 3) is the whole of its mean's disadvantage.

### The promotion rule needed a threshold

The rule as written — "a default changes only when it wins the registered contrast and regresses
no term of `balanced` on either reference" — cannot be applied. Two fits always differ on some
term, so every candidate regresses something and no default could ever change. Against A4,
`mayfly-r16` is worse on `decay_slope_dbps` in eight paired seeds of eight, 0.0012 against
0.0174 dB/s. It is unanimous, so it is not noise; it is also 0.17% of that term's 10 dB/s norm,
worth about 0.0002 of score.

The rule now carries a materiality threshold: a term counts as regressed when the paired
difference is both real and larger than one percent of the term's norm in
`optimizer.DefaultNorms`. That is the judgement the rule always required, written down so the
next decision is reproducible rather than argued.

**`mayfly-r16` is promoted to the CLI default.** `glockenspiel fit` now runs mayfly in one warm
round plus fifteen cold restarts, with `--max-iter` defaulting to 640 so the sixteen rounds have
room to anneal. Every other backend stays selectable, and the server and browser paths keep their
own defaults until 8.7 collapses the three.

### The refits, and why neither shipped

Both recipes ran the promoted shape at 120,000 evaluations with the clock off, so a rerun at a
fixed seed reproduces the fit rather than approximating it. Both stopped on `max_evaluations` at
120,021 evaluations; both polish stages were rejected, having lowered the polish profile while
raising the primary score, which is the accept-only-if-better rule doing its job.

| preset                      | `balanced` at note 69 | at note 72 | modes | render peak    |
| --------------------------- | --------------------- | ---------- | ----- | -------------- |
| shipped `recorded-bar.json` | 0.5545                | 0.5365     | 12    | −3.0 dBFS      |
| refit `recorded-bar.json`   | 0.4892                | **0.2043** | 8     | **−27.5 dBFS** |

The refit is better by 62% at its own note, and it needs no hand retune at all: it was fitted at
note 72, the recording's own pitch, so the ×1.667 multiplication that produced the shipped file
has nothing left to do. **It is still not shipped**, because it renders 24.5 dB quieter and no
post-step in this repository can fix that.

That is the finding, and it is about the model rather than the fit. The reference loader
peak-normalises the recording by +27.6 dB, so a candidate has to reach full scale to match it.
It cannot: mode amplitudes are bounded at ±2 and this fit already has `modes[2].amplitude` pinned
at −2, meaning the search asked for more amplitude and the box refused; `input_mix` is bounded at
2 and sits at 1.135, worth at most another 4.9 dB; and the preset schema has no output gain at
all. The objective never notices, because it divides out a least-squares gain before scoring —
this fit's own provenance records `gain +26.95 dB`. So the score is a statement about shape, the
level is unconstrained, and every fit against this reference lands about 25 dB quiet.

Review finding A1.6 said gain is searched rather than solved. This is that finding with a number
on it and a shipped artifact behind it: **the recorded-bar refit is blocked on the model gaining
an output gain parameter**, not on more search.

`default.json` was not refitted either, for a different reason. `legacy_synth_a4.wav` contains
exactly one partial, so a fit against it seeds one mode and writes a one-mode preset — 0.0649
against the shipped preset's 0.135, and correct, and useless as the instrument's general-purpose
sound. Choosing a default sound is not a fit against a single-partial synthetic render. Refitting
it needs a multi-partial reference at A4, which this repository does not have.

## Engine shape re-taken, 2026-09-05

The same registered `engine-shape` design, re-run from revision `9704ed4` with `modified false`:
twelve paired seed blocks of five arms at 24,000 evaluations each, on the C5 recording at note 72
under `balanced`. The same four preconditions hold — every row stopped on `max_evaluations`, no two
blocks of any arm share a score or a parameter vector, and the build is stamped. The design hash is
unchanged, so the arms, the seeds and the budget are the same ones; what changed underneath is the
objective.

The full report is [data/engine-shape-report.md](data/engine-shape-report.md) and the rows are
[data/engine-shape-results.csv](data/engine-shape-results.csv). The 2026-09-03 run is kept beside
them under its own date.

**The elapsed-time column of this run is not comparable to the 2026-09-03 one.** The machine was
shared while it ran, at a load average around 30 on twelve threads, so a job took 1m26s against the
earlier run's 63s. Nothing else is affected: the budget is 24,000 evaluations, not a stopwatch,
which is exactly the property the smoke run further up this file does not have.

### The ranking inverted

| arm              | 2026-09-03 mean | rank | 2026-09-05 mean | rank |
| ---------------- | --------------: | ---: | --------------: | ---: |
| `mayfly-single`  |        0.220173 |    2 |        0.287040 |    1 |
| `sep-cmaes-r`    |        0.253491 |    3 |        0.309507 |    2 |
| `sep-cmaes-ipop` |        0.279348 |    5 |        0.310222 |    3 |
| `mayfly-r16`     |    **0.213820** |    1 |        0.314557 |    4 |
| `blk-cmaes-r`    |        0.275572 |    4 |        0.326868 |    5 |

`mayfly-r16` — the arm 8.6 promoted, and what a bare `glockenspiel fit` runs today — goes from
first of five to fourth of five. This is the reordering Phase 8.10 warned was possible, arriving on
the one table a shipped decision rests on.

Every score rose, by 0.05 to 0.10. Two things changed between these runs, though, and only one of
them is Phase 8.10: the objective also gained `onset_db` and `balanced` was reweighted for it, which
is exactly what "The composite objective re-taken" above shows moving scores on fixed presets. The
rise and the reordering cannot be attributed to the floor by inspection.

**So the reweighting is ablated rather than argued away.** Every job of this run is rescored from
its own term columns under the ten-term weights the 2026-09-03 run was scored with — `balanced` at
`c7f0ecf^`, partials 0.4 / spectrum 0.25 / envelope and slope 0.25 / waveform 0.1 — dropping
`onset_db` entirely. The reconstruction is checked first against the run it can be checked on: the
2026-09-03 published `score` column reproduces from its own terms under those weights to 8e-06, and
this run's published column reproduces under the eleven-term weights to 6e-06, so the arithmetic
below is the shipped arithmetic and not a re-derivation of it.

| arm              | 2026-09-03 (10 terms) | 2026-09-05 rescored (10 terms) | 2026-09-05 as published (11 terms) |
| ---------------- | --------------------: | -----------------------------: | ---------------------------------: |
| `mayfly-single`  |              0.220173 |                   **0.263272** |                       **0.287040** |
| `sep-cmaes-r`    |              0.253491 |                       0.283831 |                           0.309507 |
| `sep-cmaes-ipop` |              0.279348 |                       0.287276 |                           0.310222 |
| `mayfly-r16`     |          **0.213820** |                       0.290354 |                           0.314557 |
| `blk-cmaes-r`    |              0.275572 |                       0.290258 |                           0.326868 |

**Under identical weights, on identical terms, the inversion is still there.** `mayfly-single` is
first and `mayfly-r16` fourth of five with the eleventh term removed; the decisive contrast reads
+0.0271 at t = +5.69 winning twelve blocks of twelve, against +0.0275, t = +5.18 and eleven of
twelve as published. The scores still rise by 0.04 to 0.08 with the weights held fixed. Whatever
the reweighting did to the composite table on fixed presets, it is not what moved this campaign.

Two limits on that, both real. Rescoring cannot undo the fact that the 2026-09-05 arms _searched_
the eleven-term objective, so their trajectories differ for a reason no rescore can remove — the
ablation shows the ranking is not an artefact of how the results were scored, not that the two runs
optimised the same thing. And it isolates the weights, not the floor: what remains after the
weights are held fixed is 8.10 together with that trajectory difference. What the floor is known
independently to have done is the plateau — Phase 8.10 records `spectral_fine_db` reporting the
height of a constant for candidates far from level, and the sorted column below shows that plateau
present in the old run and absent from the new one.

The arms were not being flattered equally, which is why the order moved rather than merely the
level: fits against this peak-normalised recording landed far enough from its level for the old
floor to bite, and how far was arm-dependent — the gain table below spreads from −33.4 to +60.0 dB
across the arms of this very run, so "far from level" is a tendency with exceptions, not a property
of every fit.

### The contrasts, all three

| contrast                                | 2026-09-03                                  | 2026-09-05                                |
| --------------------------------------- | ------------------------------------------- | ----------------------------------------- |
| `blk-cmaes-r` vs `mayfly-r16` (primary) | −0.0618, t −10.68, p < 0.0001, 0/12, reject | −0.0123, t −2.66, p 0.0224, 4/12, reject  |
| `sep-cmaes-r` vs `mayfly-r16`           | −0.0397, t −4.00, p 0.0021, 2/12, reject    | +0.0050, t +1.70, p 0.1167, 8/12, retain  |
| `mayfly-single` vs `mayfly-r16`         | +0.0064, t +0.49, p 0.6368, 5/12, retain    | +0.0275, t +5.18, p 0.0003, 11/12, reject |

**The registered primary contrast fails again, in the same direction.** Block-covariance CMA-ES is
still worse than the mayfly arm, so the conclusion 8.6 drew from it stands and the reasoning under
"That block covariance loses is worth recording" above is unaffected. It fails by a fifth of the
margin, though: t = −2.66 where it was −10.68, and it now wins four blocks of twelve where it won
none. Most of what looked like a decisive loss was the floor.

**One secondary contrast reversed outright.** Separable CMA-ES was worse than `mayfly-r16` by
0.0397 at p = 0.002, winning two blocks of twelve. It is now ahead on the mean, winning eight of
twelve, at p = 0.117 — not significant, so the reading is "no difference worth calling one" where
it used to be "clearly worse". An arm the project had written off is level with the default.

**The other secondary contrast reversed and became significant, and it is the one that decides
something.** `mayfly-single` and `mayfly-r16` were indistinguishable — p = 0.64, five blocks of
twelve, and 8.6 chose `mayfly-r16` on its lower spread rather than on its mean. The single long run
now beats sixteen rounds by 0.0275 of score in **eleven blocks of twelve**, t = 5.18, p = 0.0003,
surviving Holm at a family-wise 0.05.

The spread argument that decided it in 8.6 has also gone. The standard deviations were 0.0133 for
`mayfly-r16` against 0.0476 for `mayfly-single` — a factor of 3.6, and the whole case for the
restarting arm. They are now 0.0103 against 0.0144, a factor of 1.4. `mayfly-single` is no longer
the erratic arm; most of its apparent volatility was the floor rewarding whichever of its runs
happened to drift quietest.

There is a mechanism for this rather than just a number. Phase 8.10 records that the old
`spectral_fine_db` was reporting the height of a floor plateau — a constant — for candidates far
from level, so a twelfth of the balanced weight was spent on something that could not tell two
candidates apart. That is a flatter, less informative landscape, and restarts are worth most
exactly there. Sharpen the term and a single long anneal has something to follow. The sorted
`spectral_fine_db` column says the same thing directly: the 2026-09-03 run is bimodal, with 41 of
its 60 jobs compressed into the band 1.93 to 4.66 and a gap before the rest, while the 2026-09-05
run spreads over 8.67 to 18.03 with no such cluster.

### What this does to the default

Under 8.6's promotion rule, with the materiality threshold it gained, `mayfly-single` wins a
registered contrast against the shipped default decisively. The rule's second clause — that the
challenger regress no term of `balanced` on **either** reference — is settled in
"The second reference re-taken" below, and the default is decided there rather than here.

`seed-hunt` remains unrunnable. It refines a winning CMA-ES arm and there is still no CMA-ES arm
that wins: separable CMA-ES reached parity, not victory.

### Level is now a two-sided drift, and 8.9's clamp earns its warning

Every job in this run wrote an `output_gain_db`, which Phase 8.9 solves at write time from the
render's peak. Across the sixty presets the mean absolute gain is **19.6 dB**, so level is still
entirely unconstrained by the search — as designed, since the objective is gain-invariant and the
gain is solved rather than searched.

What changed is the direction. The old objective paid for quiet, and 8.9 measured every Morphagene
seed drifting about 37 dB down. With the floor corrected the drift is two-sided and arm-dependent:

| arm              | mean `output_gain_db` | range          | negative |
| ---------------- | --------------------: | -------------- | -------- |
| `mayfly-single`  |                  +8.9 | −15.4 to +29.2 | 1 of 12  |
| `mayfly-r16`     |                  −9.0 | −30.4 to +18.0 | 8 of 12  |
| `sep-cmaes-ipop` |                  −1.7 | −27.0 to +60.0 | 9 of 12  |
| `blk-cmaes-r`    |                 −23.1 | −33.3 to +0.2  | 11 of 12 |
| `sep-cmaes-r`    |                 −26.1 | −33.4 to −11.3 | 12 of 12 |

A negative gain means the render came out louder than the −3 dBFS target, so both CMA-ES restart
arms now overshoot by about 25 dB as consistently as the old fits undershot. The bias 8.10 removed
was real and one-directional; what is left is the free ridge 8.9 described, and 8.9's solved gain
is what makes any of these presets usable.

One job of sixty bound the clamp — `b07-sep-cmaes-ipop`, whose log reads
`output gain: +60.00 dB, clamped at the bound; the fit renders far enough from the target that the
preset stays off it`. Its score, 0.3164, is unremarkable, so this is an ordinary fit that landed
more than 63 dB below full scale rather than a degenerate one. That is the warning 8.9 added doing
the job it was added for, at a rate of one in sixty.

## The second reference re-taken, 2026-09-05

The promotion rule asks about both references, and the C5 campaign above puts a challenger in
front of it, so the same three shapes were run on `legacy_synth_a4.wav` over eight paired seeds at
the campaign's 24,000-evaluation budget. Twenty-four fits, all stopping on `max_evaluations`. The
arm settings are copied from the campaign's own job configs, so these are the same three shapes the
campaign ran and not an approximation of them.

| seed | `mayfly-single` | `mayfly-r16` | `sep-cmaes-r` |
| ---- | --------------- | ------------ | ------------- |
| 1    | 0.172141        | 0.170350     | 0.172168      |
| 2    | 0.172141        | 0.170933     | 0.172144      |
| 3    | 0.170593        | 0.170324     | 0.170531      |
| 4    | 0.172147        | 0.169934     | 0.172177      |
| 5    | 0.172139        | 0.170848     | 0.172528      |
| 6    | 0.172524        | 0.170470     | 0.170961      |
| 7    | 0.172151        | 0.170442     | 0.170491      |
| 8    | 0.172140        | 0.169936     | 0.172142      |
| mean | 0.171997        | **0.170405** | 0.171643      |
| sd   | 0.000583        | 0.000365     | 0.000834      |

**`mayfly-r16` wins here, and it wins every seed.** Against `mayfly-single` the paired difference is
0.001592 with t = 6.86, eight blocks of eight; against `sep-cmaes-r` it is 0.001238 with t = 3.95,
again eight of eight. The arm that lost eleven of twelve blocks on the recording wins all eight on
the synthetic render.

That is not a contradiction, it is what two references are for. The A4 file holds a single partial
and every shape solves it to within 0.002 of score; the recording holds eight partials in a room
and the shapes separate by fifty times as much. The second reference is not a tie-breaker of equal
weight, it is a check that a change good for the hard case is not paid for on the easy one.

**The old outlier was an artifact too.** The 2026-09-03 table recorded `sep-cmaes-r` with an sd of
0.006696 against `mayfly-r16`'s 0.000925, and named seed 3 — which scored 0.083878 where its
siblings scored about 0.0648 — as "the whole of its mean's disadvantage". Seed 3 now scores
0.170531, in line with every other seed, and the sd is 0.000834 against 0.000365. The bad seed was
the floor, not the engine. The variance argument that section makes does not survive, though its
conclusion — no difference worth calling one between the two shapes on this reference — does.

### The decision: the default does not change

Phase 8.6's rule is that a default changes only when it wins a registered contrast and regresses no
term of `balanced` on either reference, where a term counts as regressed when the paired difference
is both real and larger than one percent of the term's norm in `optimizer.DefaultNorms`.

`mayfly-single` passes the first clause on the recording, decisively. It fails the second on A4,
paired over the eight seeds, `mayfly-single` minus `mayfly-r16`:

| term                    | difference | % of norm | t     | worse in |
| ----------------------- | ---------- | --------- | ----- | -------- |
| `onset_db`              | +0.85989   | +5.73%    | +5.40 | 8/8      |
| `envelope_db`           | +0.05612   | +1.87%    | +6.37 | 8/8      |
| `partial_decay_octaves` | +0.00101   | +0.20%    | +3.90 | 8/8      |
| `spectral_coarse_db`    | +0.00229   | +0.02%    | +0.73 | 1/8      |
| `partial_cents`         | −0.00044   | −0.00%    | −1.24 | 3/8      |
| `decay_slope_dbps`      | −0.00599   | −0.06%    | −1.62 | 2/8      |
| `spectral_fine_db`      | −0.00052   | −0.01%    | −4.39 | 0/8      |
| `waveform`              | −0.01353   | −2.71%    | −6.68 | 0/8      |

`onset_db` and `envelope_db` are unanimous, significant, and above the one percent threshold. Two
material regressions on the second reference, so the rule refuses the promotion.

**`mayfly-r16` remains the CLI default, now on evidence rather than on inheritance.** It is worth
being exact about what that sentence is worth: the arm is retained having been beaten on the
primary reference, by a rule written before the result was known. Exactly two terms save it,
`onset_db` and `envelope_db`, because the rule vetoes on material _regressions_ only — `waveform`
is the challenger's largest term movement on this reference, −2.71% of its norm in eight seeds of
eight, and it counts for nothing here. And `onset_db` did not exist when this default was chosen,
which makes the term that decided the rematch one the original campaign could not have measured.

Two things follow. `mayfly-single` is now the better arm on the only real recording this project
has, by a margin no seed disputes, so the case for it is open rather than closed and wants a design
that measures both shapes at two or three budgets — the campaign already showed `mayfly-single`
reaching its best at 98.8% of its budget, still improving when the cap cut it. That design is the
`rounds-*` ladder, run the same day; "The round schedule at three budgets" below is its result, and
it does not promote the arm either. And `sep-cmaes-r` reached parity on the recording, which is not
a win, so `seed-hunt` stays unrunnable for the second campaign running.

## The refit re-taken, 2026-09-05

The `recorded-bar` refit is re-run with the current binary at the same recipe the 2026-09-03 one
used — the promoted mayfly shape, 120,000 evaluations with the clock off, `--polish cmaes`,
`--seed 1`, note 72 — so this is a rerun rather than a new experiment. It stopped on
`max_evaluations` at 120,021 evaluations, the same count as before, and its polish stage was
rejected again for the same reason, having lowered the polish profile while raising the primary
score.

| preset                      | `balanced` at 69 | at 72      | modes | render peak   | waveform | wf gain     |
| --------------------------- | ---------------- | ---------- | ----- | ------------- | -------- | ----------- |
| shipped `recorded-bar.json` | 0.5669           | 0.5536     | 12    | −3.0 dBFS     | 1.000    | −47.3 dB    |
| refit `recorded-bar.json`   | 0.4916           | **0.2937** | 8     | **−2.7 dBFS** | 0.714    | **+4.4 dB** |

**The level problem is gone.** The 2026-09-03 refit rendered at −27.5 dBFS and could not be
shipped, because nothing in the schema could correct it. This one renders at −2.7 dBFS against the
shipped preset's −3.0. Its solved `output_gain_db` is **−0.93 dB**: the fit landed within a
decibel of the target on its own, and the gain Phase 8.9 added barely has to do anything. What was
a 24.5 dB blocker is a rounding correction.

That is worth separating into its two causes, because only one of them is 8.9. Phase 8.9 made the
level **representable** — a schema field the fit writes, so a preset can carry its own level at
all. Phase 8.10 removed the reason the level was wrong in the first place: the old floor paid a
candidate for being quiet, and this fit is the same recipe run without that incentive. The
campaign above shows the drift is now two-sided and still large in general, mean absolute 19.6 dB,
so landing at −0.93 dB is this run's luck rather than a new guarantee. The guarantee is 8.9's:
whatever the fit drifts to, the written preset carries the correction.

**The refit is better by 47%, not 62%.** At its own note it scores 0.2937 against the shipped
preset's 0.5536. The 2026-09-03 figures were 0.2043 against 0.5365, a 62% improvement, and most of
the difference between the two margins is the old floor flattering a −27.5 dBFS candidate. The
refit is still far better, and it still needs no hand retune, having been fitted at note 72 — the
recording's own pitch — so the ×1.667 hand multiplication that produced the shipped file has
nothing left to do.

**A fit against this recording now correlates in the time domain, which has not happened before.**
This file's baseline recorded that "against the recording, the time-domain objective sees nothing":
every shipped preset's waveform gain sat tens of decibels down, meaning zero correlation, and only
the spectral term ordered candidates at all. The shipped preset still reads −47.3 dB with a
waveform residual of 1.000, recovering none of the reference's energy. The refit reads **+4.4 dB**
with a residual of 0.714 — real correlation, and 29% of the recording's energy recovered by a
model that had recovered none of it. The analysis seed's 0.73 was called "the first number in this
repository that says the model can reach the recording"; this is that claim from a fit rather than
from a seed.

Every other term moves the same way: `spectral_fine_db` 17.5 to 13.1, `spectral_coarse_db` 21.6 to
16.0, `envelope_db` 2.22 to 0.60, `onset_db` 29.1 to 9.6, `partial_extra` 0.50 to 0.15. The refit
finishes with **one of thirty dimensions on a bound**, against the 2026-09-03 refit's pinned
amplitude, so the search is no longer fighting the box.

**`default.json` is still not refitted**, and that reason is unchanged by any of this:
`legacy_synth_a4.wav` holds exactly one partial, so a fit against it writes a one-mode preset.
Choosing a default sound is not a fit against a single-partial synthetic render, and it needs a
multi-partial reference at A4 that this repository does not have.

### Shipping it is a separate decision

The blocker 8.6 recorded is discharged: the refit is better on the recording by every term, and it
now carries a correct level. Promotion is still a judgement about the instrument's sound rather
than about a score, and the preset is left in `out/` for that decision to be taken deliberately.
Two things the decision should weigh. It matches one of the recording's eight partials where the
shipped preset also matches one, so the improvement is in shape and level rather than in partial
placement. And `calibrateNoteTrims` normalises the whole instrument to the preset's own note, so
shipping a preset changes the level of every key, which is exactly why the level had to be right
before this could be considered at all.

## The round schedule at three budgets, 2026-09-05

The section above left the `mayfly-single` case open rather than closed, and named what would
settle it: the arm reached its best at 98.8% of its budget, still improving when the cap cut it, so
the comparison might have been budget-limited rather than decided. `rounds-12k`, `rounds-24k` and
`rounds-48k` are that measurement — the same two shapes, the same twelve-block paired design, the
same C5 reference, at 12,000, 24,000 and 48,000 evaluations.

They are three registered designs rather than one, because `Design.Budget` is the evaluation cap
for every job in a design, and matching arms on evaluations is the single property that makes two
arms comparable at all. A design holding two budgets would give that up. They also take **disjoint
seed bases** (123,000 / 124,000 / 125,000), and here that is substantive rather than conventional:
a 12k run is a _prefix_ of a 48k run at the same seed and arm, so a shared base would make the
rungs nearly perfectly correlated and any cross-budget reading would understate its own spread.
The consequence is the reverse of the usual one — each rung is a paired test carrying its own
p-value, and reading _across_ the rungs is descriptive, not inferential.

Revision `bfc780b`, go1.26.0, mayfly v0.7.1, go-cma-es v0.1.0, binary SHA-256 `50877d53…`,
reference `testdata/reference/glockenspiel_c5.wav` SHA-256 `635f898e…`, 12 workers, manifests
recording `modified: false`. Design hashes `d391edd5…`, `f3996cfb…`, `405e377f…`. 72 jobs, 1 h 36 m
of compute. The per-job data is
[`docs/data/rounds-12k-results.csv`](data/rounds-12k-results.csv),
[`-24k`](data/rounds-24k-results.csv) and [`-48k`](data/rounds-48k-results.csv), with the reports
beside them.

| budget | `mayfly-single` mean (sd) | `mayfly-r16` mean (sd) | gain    | t (df=11) | p       | blocks won | single spent at best | r16 spent at best |
| ------ | ------------------------- | ---------------------- | ------- | --------- | ------- | ---------- | -------------------- | ----------------- |
| 12,000 | 0.288232 (0.011945)       | 0.339007 (0.011012)    | +0.0508 | +15.12    | 0.00000 | 12/12      | 99.7%                | 50.0%             |
| 24,000 | 0.272037 (0.013052)       | 0.310140 (0.005242)    | +0.0381 | +8.44     | 0.00000 | 12/12      | 98.3%                | 23.5%             |
| 48,000 | 0.279863 (0.021281)       | 0.302503 (0.008634)    | +0.0226 | +3.47     | 0.00522 | 10/12      | 97.2%                | 30.7%             |

**The win is not a budget artifact.** `mayfly-single` is ahead at every rung, and at the two lower
rungs no seed dissents. Quadrupling the budget does not reverse the engine-shape re-take; whatever
that result was, it was not an artefact of stopping the search early.

**But the margin narrows monotonically**, +0.0508 → +0.0381 → +0.0226, and the narrowing comes
almost entirely from `mayfly-r16` improving (0.3390 → 0.3101 → 0.3025) while `mayfly-single` gains
from 12k to 24k and then gives some of it back (0.2882 → 0.2720 → 0.2799). A single-run arm has
nothing to spend a larger budget on but a longer run, and a longer run of one population is a
better search only while the population is still moving.

**The variance goes the other way from the mean.** `mayfly-single`'s spread grows with budget
(sd 0.0119 → 0.0131 → 0.0213) while `mayfly-r16`'s falls (0.0110 → 0.0052 → 0.0086). At 48k
`mayfly-single` loses blocks 8 and 9 — the first blocks either arm has lost anywhere in this
ladder — and its interquartile range, 0.263 to 0.296, is more than three times `mayfly-r16`'s. That
is the restart schedule doing exactly what a restart schedule is for: sixteen rounds average away
the seed, and one long run does not. The two arms are not "better and worse" so much as
**lower-median and wider** against **higher-median and tight**.

**The mechanism is in the last two columns.** `mayfly-single` reaches its best at 97–99.7% of its
budget at _every_ rung, 48k included, so it is still improving when the cap cuts it and no budget
tested here is enough to converge it. `mayfly-r16` reaches its best between 23.5% and 50% and
spends the rest not improving: quadrupling its budget bought it more restarts that recover ground
it had already covered, not four times the search. Their curves are shaped differently enough that
"which is better" is a question about the budget, and the honest answer over this range is that
`mayfly-single` wins the median at all three and buys it with spread that grows as the cap rises.

### What this does to the promotion rule

The per-term half of the rule is measured here too, paired within each rung. On C5, at a
one-percent-of-norm materiality threshold:

| budget | terms `mayfly-single` materially regresses on C5 |
| ------ | ------------------------------------------------ |
| 12,000 | `partial_level_db`, +32.6% of norm, 11/12 blocks |
| 24,000 | none                                             |
| 48,000 | none                                             |

At the tight budget `mayfly-single` buys its spectral win with partial level error, and by 24,000
evaluations it has stopped doing so — `partial_level_db` swings from +32.6% of norm to −31.4%, and
at 48k it reads +0.77%, inside the threshold. **On the primary reference, at both the campaign
budget and above it, `mayfly-single` regresses no term of `balanced`.**

**`mayfly-r16` is still the default, and for the same reason as before**: the rule requires no
material regression on _either_ reference, and the A4 block found `onset_db` at +5.73% of norm and
`envelope_db` at +1.87%, both unanimous over eight seeds. Nothing in this ladder touches A4, so
nothing here promotes anything.

What the ladder does change is the standing of that blocker. It shows a material regression
**dissolving with budget on this very comparison** — `partial_level_db` was material at 12k and
gone at 24k — and the A4 block was run at 24,000 evaluations, the middle rung. So the A4 regression
is now a candidate for the same explanation rather than a settled property of the arm, and the
follow-on is an A4 ladder rung at 48,000 evaluations. That is a new measurement and is not taken
here; until it is, the rule refuses the promotion and the refusal stands on evidence.

## What the baseline is for

- **Norms.** Phase 8.2 scaled each term of the composite objective so that no term saturates on
  the shipped presets; the composite table is what it scaled against.
- **The promotion rule.** Phase 8.6 changes a default only when it wins its registered contrast
  and regresses no term on either reference by more than one percent of that term's norm. This
  table is the "before"; "The default shape" above is where the rule was applied and where the
  threshold came from.
- **Reference handling.** Every C5 row of the legacy baseline is a whole-file or hand-cut
  comparison against channel zero at the recording's own level. Phase 8.1's loader cuts to the
  first strike and records what it did; since 8.2 every fit reads through it, and the composite
  table above is the re-take.

## The hollandm pack, 2026-09-05

Phase 9's three steps -- fit every note, find what depends on the note, generalise into one
preset -- taken against `testdata/reference/packs/hollandm-toy-glockenspiel`, the only
chromatic run among the four packs and the richest at 5.2 partials a note. Twenty consecutive
semitones, MIDI 84 to 103.

Provenance. The twenty per-note fits were run by
`a63247972ec94d68d74897390508fd740026e2bc4a3a5134e87909c118113a34` at revision `bb07c14`; the
joint fit by `93c80353...` at revision `0f52053`; the derived tables by `cad66e25...` at
revision `6a2c433`. Every fit ran at 24,000 evaluations, `mayfly/desma` with population 10 and
15 restarts, profile `balanced`, `TimeBudget: 0` and the clock off, twelve workers. **The
worker width is part of the provenance**: the search is reproducible at a fixed seed _and_ a
fixed width, not at a fixed seed alone.

### The pack, and the note each file actually sounds

Ten of the twenty files arrived from Freesound sharing a name with their own sharp, because the
upload form strips `#`. The note is therefore taken from the measured fundamental and the file
name is only checked against it; a file whose name and pitch disagree by more than 50 cents is
refused rather than fitted a semitone away from itself. `cents` below is how far the recording
sits from equal temperament, and it is part of what one preset structurally cannot follow: the
spread is -0.45 to +4.67 cents, about 0.3 on `partial_cents` against its norm of 10.

| note | file      | cents | reference sha256   |   seed |    score |   s |
| ---: | --------- | ----: | ------------------ | -----: | -------: | --: |
|   84 | `c6.wav`  | -0.45 | `45b0682fd98047c4` | 130000 | 0.356647 | 164 |
|   85 | `cs6.wav` | +1.87 | `bb38fd931f17a782` | 130001 | 0.352803 | 297 |
|   86 | `d6.wav`  | -0.20 | `f93606c19065630a` | 130002 | 0.368057 | 256 |
|   87 | `ds6.wav` | +2.08 | `f012eaba050ff26b` | 130003 | 0.370669 | 239 |
|   88 | `e6.wav`  | +1.69 | `88132d7872635d18` | 130004 | 0.351347 | 481 |
|   89 | `f6.wav`  | -0.76 | `6d284f25d445ec2f` | 130005 | 0.389584 | 433 |
|   90 | `fs6.wav` | +3.47 | `63696f44dfdd1e0e` | 130006 | 0.356153 | 231 |
|   91 | `g6.wav`  | +0.45 | `b3048703e20b7e89` | 130007 | 0.359447 | 272 |
|   92 | `gs6.wav` | +1.34 | `61a6fd0d818876ec` | 130008 | 0.318498 | 219 |
|   93 | `a6.wav`  | +3.41 | `8f717888c91a7dd6` | 130009 | 0.300934 | 222 |
|   94 | `as6.wav` | +4.67 | `b248665f6184e9a0` | 130010 | 0.358214 | 145 |
|   95 | `b6.wav`  | +2.56 | `1188be8b50c0949f` | 130011 | 0.322725 | 103 |
|   96 | `c7.wav`  | +3.15 | `f66be9c5a277812b` | 130012 | 0.306971 | 190 |
|   97 | `cs7.wav` | +0.09 | `031dbe8d82c45816` | 130013 | 0.328851 | 192 |
|   98 | `d7.wav`  | +0.49 | `5b4341b2d5754d29` | 130014 | 0.284294 | 136 |
|   99 | `ds7.wav` | +1.34 | `651216fa7d30df75` | 130015 | 0.320145 | 196 |
|  100 | `e7.wav`  | +2.55 | `5c10df34686a3db4` | 130016 | 0.341532 | 197 |
|  101 | `f7.wav`  | +2.60 | `e2d7deb749271672` | 130017 | 0.305849 | 116 |
|  102 | `fs7.wav` | +3.76 | `515f19c91effecf6` | 130018 | 0.238406 | 112 |
|  103 | `g7.wav`  | +0.90 | `cd79f0f3af171b66` | 130019 | 0.257320 |  92 |

Mean of the twenty, each note fitted to itself: **0.329422**. That is the
floor the rest of this section is measured against, and it is unreachable by construction --
it is twenty presets, not one.

### The transposition matrix

Each of the twenty per-note presets was transposed to all twenty notes with
`TransposeToNote`, its output gain solved in closed form at each, and scored under the same
aggregate objective the joint fit minimises: the mean of the per-note `Score(profile)`, never
the mean of the terms then one score. The joint preset is the twenty-first row. The full
20x21 table is [docs/data/pack-hollandm-matrix.csv](data/pack-hollandm-matrix.csv); the row
means are:

| preset authored at | mean over 84-103 |     | preset authored at | mean over 84-103 |
| -----------------: | ---------------: | --- | -----------------: | ---------------: |
|                 84 |         0.632260 |     |                 94 |         0.487500 |
|                 85 |         0.551303 |     |                 95 |         0.563090 |
|                 86 |         0.563241 |     |                 96 |         0.508973 |
|                 87 |         0.517042 |     |                 97 |         0.439204 |
|                 88 |         0.637414 |     |                 98 |         0.476017 |
|                 89 |         0.578130 |     |                 99 |         0.448135 |
|                 90 |         0.507894 |     |                100 |         0.446613 |
|                 91 |         0.618787 |     |                101 |         0.491812 |
|                 92 |         0.441039 |     |                102 |         0.482035 |
|                 93 |         0.472866 |     |                103 |         0.500822 |

|                                           | mean over 84-103 |
| ----------------------------------------- | ---------------: |
| best single-note preset (note 97)         |         0.439204 |
| **the joint preset**                      |     **0.428765** |
| each note fitted to itself (the diagonal) |         0.329422 |

**The joint row beats every single-note row**, not merely the average one, which is the
registered claim and the reason to fit jointly at all rather than to pick the best bar and
transpose it. And:

> **the price of one preset covering twenty notes is +0.099343**

which is the deliverable of the whole exercise: 0.329 is what twenty presets buy, 0.429 is
what one buys, and the difference is what a single transposed preset structurally cannot
reach.

### The prediction that was falsified

Before the matrix existed, PLAN.md registered the prediction that the loss would be
bottom-heavy, because the shared mode ceiling binds at the top note: with the joint fit
authored at 94 and the top note at 103, the box ceiling is `min(default, 0.45*rate / max_i
r_i)` = 11,800 Hz, or 6.33x f0, against 19x f0 for a single-note fit at c6. The measurement
says otherwise:

| half of the range | loss against the diagonal |
| ----------------- | ------------------------: |
| bottom (84-93)    |                  +0.10530 |
| top (94-103)      |                  +0.09338 |

The two worst single notes are 95 (+0.17268) and 98 (+0.16530), both in the top half. The
ceiling still caps what the preset can represent -- that is arithmetic -- but **it is not what
one preset costs**. What a single preset cannot carry is the individual bar, not the band
limit, and the twenty-note regression says the same thing from the other direction: 0.33
octaves of bar-to-bar decay scatter against a term norm of 0.5.

### What the joint fit did with its seed

The seed was pooled over all twenty notes at coverage 0.35 -- every note's measured partials
divided by `2^((n_i-94)/12)`, clustered in log frequency, and the clusters admitted that at
least seven of twenty notes agree on. That gives three modes at 1.002x, 2.723x and 5.330x f0.
The 2.723x cluster is the free-free bar's second partial, present in 18 of the 20 bars and
holding to 1.4% with no drift across the range; two further clusters at 6.90x and 8.93x were
admitted by coverage but sit outside the shared box, because the top note cannot hold them.

The fit did not keep the second partial. It returned:

| ratio to f0 | frequency at note 94 |    decay | amplitude |
| ----------: | -------------------: | -------: | --------: |
|      1.001x |            1866.9 Hz | 361.4 ms |   -0.0839 |
|      1.002x |            1868.7 Hz |   5.6 ms |   -0.5327 |
|      5.306x |            9893.7 Hz |  20.8 ms |   +0.5510 |

Two of the three modes are the fundamental: one long and quiet, one 5.6 ms and loud. That is
an attack transient on the fundamental rather than a second mode, so the search spent a mode
shaping the onset instead of placing the 2.723x partial it was seeded with. **Nothing pinned
-- 0 of 15 dimensions sit on a box edge -- so the box did not force this**; fitting twenty
notes at once, a well-shaped fundamental envelope simply scores better than a real second
mode does. This is a finding about the objective, not about the bar.

### Reproducibility, checked rather than assumed

The joint fit was re-run from scratch on a later binary carrying the schema-stamping fix, at
the same seed and the same worker width. The two runs agree on **every one of the 473
iterations the second reached** -- identical `current`, `best` and `evaluations` at each --
including the final `best` of 0.428765. The second run was then killed by the machine for
memory at evaluation 20,147 and was not restarted: a run that has reached the same best by the
same path has nothing left to demonstrate. So the stamping change is numerically inert, as a
change to a version string ought to be.

One consequence worth writing down: `out/pack/hollandm-joint/preset.json` says version 4.0
while carrying no `decay_keytrack`, because the binary that wrote it predates the fix that
earns a version from the fields a document actually uses. Its parameters are right and its
version is not. Nothing ships from `out/`.

### What this could not measure

- **A loudness curve.** Each note's level is solved in closed form and divided out, so the
  joint objective is blind to relative level across notes. Correct for this pack, five of whose
  files touch full scale -- but the preset has not learned a loudness law and cannot have.
- **Bar-to-bar scatter.** 0.33 octaves of decay spread and an idiosyncratic third mode are
  properties of twenty distinct pieces of metal. Only a zone or multisample layer reaches them.
