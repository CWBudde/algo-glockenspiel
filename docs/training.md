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

The rows are committed as [data/engine-shape-results.csv](data/engine-shape-results.csv), so the
table above rebuilds without rerunning the campaign:
`glockenspiel-campaign analyze --csv docs/data/engine-shape-results.csv`.

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
