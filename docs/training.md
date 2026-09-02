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
peaks at full scale, the gain columns are unchanged, and `aligned+gain`'s spectral term climbs to
32–73 dB because a −25 to −65 dB gain shifts every candidate bin under the floor. Nothing there
changes a reading of the baseline.

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

## What the baseline is for

- **Norms.** Phase 8.2 scaled each term of the composite objective so that no term saturates on
  the shipped presets; the composite table is what it scaled against.
- **The promotion rule.** Phase 8.6 changes a default only when it wins its registered contrast
  and regresses no term on either reference. This table is the "before".
- **Reference handling.** Every C5 row of the legacy baseline is a whole-file or hand-cut
  comparison against channel zero at the recording's own level. Phase 8.1's loader cuts to the
  first strike and records what it did; since 8.2 every fit reads through it, and the composite
  table above is the re-take.
