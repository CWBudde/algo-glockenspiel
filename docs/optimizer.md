# Optimizer design

The fitting stack in `internal/optimizer` renders the current model against a reference WAV and
scores each candidate with the composite objective below, or with one of the legacy single-term
metrics. Three backends search it: `simple`, the gonum Nelder-Mead simplex, kept as the local
one; `mayfly`, the swarm, the default since Phase 8.6 measured it against the other two; and
`cmaes`, which was the default from 8.4 until then. A `--polish`
stage may follow whichever ran. The CLI workflow and flags are documented in
[user-guide.md](user-guide.md); this note records the less visible contracts that
implementations and checkpoints depend on.

## Parameter space

Every optimizer backend searches the same unit cube. `ParamCodec` first encodes model parameters,
using logarithmic coordinates for positive quantities such as frequencies and decay times, and
then `Bounds.Normalize` maps every encoded dimension to `[0,1]`. This keeps step sizes comparable
across dimensions whose physical ranges differ by orders of magnitude.

The vector is the dry mix, the log of the excitation cutoff, then per mode the amplitude, the
log of the frequency in hertz and the log of the half-life, then the Chebyshev gains. Phase 8.3
reshaped it, for four reasons the review found:

- **The base frequency is not searched.** It never reaches the audio, so searching it was a
  gauge freedom — a flat ridge through every fit — and the codec writes the template's value
  through instead.
- **Mode frequencies are absolute.** The old multiplier against the base could exceed the
  model's ceiling and every candidate out there scored `+Inf`, a plateau a swarm cannot climb
  off. The box is `[20 Hz, 20 kHz]` by default and every point of it decodes; a fit narrows it
  to half the reference's fundamental up to 0.45 of the sample rate (`FrequencyBoundsFor`),
  converted to the note the preset is authored at.
- **Modes are written in ascending frequency.** `EncodeParams` sorts them and `DecodeParams`
  sorts them back, so the same sound always writes the same list and a population seeded from
  an ordered point stays in one ordering. The n! relabelings are still in the box — an encoding
  that removed them chains every mode to the one below it — but nothing that leaves the codec
  depends on which one the search sat in, and `Pinned` names a mode by its written index.
- **The decay box is `[0.5, 2000]` ms**, wide enough for the recording's 677 ms fundamental,
  and the objective narrows its ceiling to `model.AuthoredDecayMsMax` for the template's note —
  1487 ms at note 100 — so a fit cannot write a decay the preset file then refuses.

The **mode count comes from the reference**, not the template: `SeedPreset` replaces the
template's modes with one per measured partial, at its frequency, attack level and half-life
(`PresetFromAnalysis`), and a v1 template becomes a v2 preset because v1 holds exactly four.
`--modes N` keeps the strongest N, `--modes -1` keeps the template's own. The seed is where the
search starts, and under Mayfly it is where half of each population starts: the incumbent, then
Gaussian draws around it with σ 0.05 in the unit cube, then the uniform rest — CircleFit's
continuation profile, in place of the single seeded individual that was one in ten of the swarm.

Candidates are clamped at the boundary. They are not mirrored: mirroring makes the objective a
folded, many-to-one map and gives a local optimizer artificial continuations to chase across a
bound. User-supplied `--bounds` are hard constraints. `ObjectiveConfig.StrictBounds` prevents the
codec from widening them to contain the template preset, and the initial point is clamped into
the requested box. Every front end reports the dimensions of a result that finished on a bound,
because a pinned dimension is one the search wanted to push past the box.

## Objective evaluation

The objective keeps the reference and candidate in floating point; it does not quantize through
PCM16 before scoring. `ProjectToPCM16Domain` remains only for reporting.

`ObjectiveFunction.Evaluate` is safe for concurrent calls. Each evaluation borrows independent
mutable render state from a pool while the reference, codec, and alignment plan remain immutable,
so population-based backends can evaluate candidates in parallel without racing.

Onset alignment is enabled by default because a small leading offset can reverse the phase of a
high partial and make the correct parameters score worse than incorrect ones.

### The composite objective

The default metric, `balanced`, is a composite: every term below is measured for each candidate
as a raw physical number, `Metrics.Score` folds them into one number under a `Profile` of
weights and norms, and `Evaluate` returns that. `EvaluateMetrics` returns the `Metrics` struct
for any candidate under any metric, which is what the CLI prints under each progress line and
the server puts in every snapshot. The terms:

- **Partials.** The reference's partials come from `internal/analysis` — from the `--analysis`
  document when one is given, else measured from the reference at construction — with the level
  at the strike, which is what a mode amplitude has to reach. The candidate's partials come from
  its parameters: one per mode and per harmonic, transposed to the note, with the amplitude
  scaled by the excitation lowpass, and modes that die within a few milliseconds left out because
  no analysis of the render would list a click as a partial. Each reference partial, strongest
  first, takes the nearest unclaimed model partial within ±100 cents. The matched pairs give
  `partial_cents`, `partial_level_db` (after the mean offset between the two lists is solved
  out) and `partial_decay_octaves`, each a level-weighted RMS; `partial_missing` is the fraction
  of the reference's partial weight that nothing matched, and `partial_extra` the weight of
  unmatched model partials above the floor, in the reference's scale, as a fraction of the
  reference's weight. The last is what makes a cluster of beating modes faking an attack
  expensive.
- **Noise-aware log spectrum**, at two resolutions: `spectral_fine_db` with 8192-point frames
  (5.4 Hz per bin, nine cents at the C5 fundamental) for where a partial sits, and
  `spectral_coarse_db` with 2048-point frames for how each partial's magnitude falls off from
  frame to frame. The reference's frames are transformed once at construction. The floor per
  resolution is the reference's own noise estimate — the median of every weighted bin of every
  frame — plus 6 dB, or 60 dB under the reference's loudest bin, whichever is higher; a bin
  below the floor in both signals contributes nothing, and a bin above it in either is compared
  with both clamped to it. That is what stops a modal model being scored on its failure to
  synthesise the room. `ComputeSpectrogram` exports this same transform, floor included, so a
  caller outside the package can compute the exact frames the objective scored rather than a
  second STFT that might disagree with them; the server's `/compare` endpoint uses it to paint
  the reference and the render with the code that scored them.
- **Envelope.** `envelope_db` is the RMS difference between the broadband RMS envelopes over
  log-spaced windows from the strike — the first ends 5 ms after it, each is a quarter longer
  than the last — with both clamped to the reference's own floor. `decay_slope_dbps` is the
  difference between the broadband decay slopes, each the least-squares line from the envelope's
  peak down 30 dB or over one second, the same rule `analysis.DecaySlopeDBps` applies to the
  reference.
- **Waveform.** `waveform` is the existing aligned RMS residual after the least-squares gain, as
  a fraction of the reference RMS: zero is the same waveform, one is no correlation at all. It is
  the phase-sensitive term, and only the `polish` profile weights it highly.

Gain is solved in closed form before every term and reported as `gain_db`: the ratio of the
reference RMS to the candidate RMS over the aligned overlap, applied to the candidate for the
spectral and envelope terms. The waveform term uses its own least-squares gain, reported as
`waveform_gain_db`; on a recording that one sits tens of decibels below `gain_db`, which is the
diagnostic that the waveforms do not correlate. Every amplitude the model carries is therefore
relative, and `--normalize-gain` has no effect on a composite metric.

`Score` scales each measured term by its norm, passes it through `x / (1 + x)` — zero for a
perfect term, one half at the norm, never above one — and takes the weighted mean over the terms
that could be measured, so a reference too short for a spectral frame is scored on the same
scale as a long one. The norms in `DefaultNorms` were set against the shipped presets on both
references so that no term of `balanced` saturates there; [training.md](training.md) has the
table. Three profiles exist: `balanced` (partials 0.36, spectrum 0.22, the envelope group 0.32 —
envelope 0.13, onset 0.10, decay slope 0.09 — and waveform 0.10), `placement` (partial-heavy, for
a global stage) and `polish` (waveform-heavy, for a local one). `Contributions` breaks a score
down term by term for a report. The weights live in `ProfileBalanced`
(`internal/optimizer/metrics.go`); they were last reweighted when the onset term joined the
objective, which is why an older reading of `balanced` does not reproduce from the same terms.

`PresetFromAnalysis` writes the preset the partial term would call a perfect answer — one mode
per measured partial, at its frequency, attack level and half-life, authored at the template's
note — and is the seed Phase 8.3 gives the search.

### The legacy metrics

`rms`, `log` and `spectral` are still accepted. Each is a single term: `log` is a monotone
transform of `rms` with the same minimiser, and `spectral` is the coarse STFT error with every
bin counted and floored at −100 dBFS alone, which is the form Phase 8's review found outvoted by
empty bins. Under a legacy metric `--normalize-gain` divides out the least-squares gain, off by
default because it makes the model's amplitude parameters unidentifiable. `EvaluateMetrics`
works under a legacy metric too, as a report rather than the cost.

## Analysing the reference

`internal/analysis` measures a reference once, by code, the way `testdata/reference/README.md`
measured the C5 recording by hand. It sits below the optimizer on purpose: Phase 8.2's objective
reads its partials and Phase 8.3's codec reads its fundamental, so the optimizer imports it and
not the other way round. The onset detector the alignment uses lives there for the same reason —
`analysis.Onset` is the one definition of where a strike starts, and `AlignmentPlan` calls it.

`LoadReference` applies three decisions and records each: which channel (`first`, the default,
because a spaced pair comb-filters when summed; or `mean`), where the strike ends (a second
event climbing 6 dB above the quietest the tail has been, or the tail not falling for half a
second, or a fixed `Window`), and the level (peak-normalised unless `KeepLevel`). `Measure` takes
the cut and finds its partials: the averaged Hann spectrum at 16384 points, local maxima 12 dB
above the median of their neighbourhood, each refined by a parabola through three bins. A
candidate inside the line shape of a stronger partial — the Lorentzian of its decay or the
window's sidelobes — is that partial's skirt and is dropped; a 50 ms partial has a line tens of
hertz wide. Each partial's half-life is a least-squares line through the dB envelope of the
signal heterodyned to its frequency, over the first second after the envelope's peak or its
first 30 dB of decay, and the line's value at the onset is the partial's attack level: what a
model mode would need at the strike, which the average level is not.

`Analysis` is the document `glockenspiel analyze` writes as `analysis.json`: the cut record, the
partials, the fundamental and the floor an empty bin sits at. `ReadFile` refuses a file without
its marker. The `Options` block carries the frame size, because the floor is a per-bin figure
at that size.

## Reading the reference

The fit, the `distance` command, the server and the browser worker all read a reference through
`analysis.LoadReference`: one channel, cut to the first strike, peak-normalised, as the section
above describes. `--downmix`, `--window` and `--keep-level` are the loader's options on the
command line, and the server takes `downmix` and `window` as form fields.

References are loaded through `internal/wavio`, which exists because the obvious way to read a
WAV here is wrong. `go-audio/wav` decodes every sample format as an integer, so a 32-bit IEEE
float file comes back as its own bit patterns divided by 2^31 — a square wave at roughly ±0.5
for any recording at a sane level. A 32-bit float WAV is what a DAW exports by default, so
every fit against one was fitting a square wave, silently and plausibly, and so was every
legacy-reference regression test.

`internal/wavio` is the single loader for the CLI, the server and the tests; there were three
copies before, which is how the defect survived in all of them at once. The float fixture is
documented in `testdata/reference/README.md`.

## Evaluation budgets

`OptimizeOptions.MaxEvaluations` caps the number of objective evaluations a run may spend, and
zero means no cap. On the command line it is `--max-evals`, which `glockenspiel fit` accepts
alongside `--max-iter` and `--time-budget`; two more flags shape the CMA-ES restart ladder under
it, `--cmaes-run-evals` (a per-run cap, zero giving each run the whole remaining budget) and
`--cmaes-lambda-growth` (the factor the population is multiplied by on each restart, two being
IPOP). Between them a campaign arm can be reproduced by hand. The evaluation cap exists because
an iteration means something different to each backend (a mayfly generation costs tens of renders, a Nelder-Mead major iteration about one), while an
evaluation is one audio render whichever backend asked for it. A campaign comparing two engines
therefore matches this rather than `--max-iter`.

Every backend honours it, and every backend stops as soon as it can once its own count reaches
the cap. A generation is the smallest unit a population method can abandon, so a run may overrun
by at most one generation's worth of evaluations; `Result.Evaluations` reports the overrun rather
than clipping to the cap, because a campaign that scores against the budget has to know what was
actually spent. A run the cap ended reports `StopReason "max_evaluations"` and is never
`Converged`.

The three backends reach that guarantee differently. CMA-ES passes the remaining budget to the
library as `Config.MaxEvaluations`, which truncates its final generation to exactly what is left,
so the cap is normally exact. Mayfly has no evaluation budget at all, so the wrapper counts
evaluations and, at the first iteration boundary past the cap, cancels a context only the
current round can see; the library returns at that boundary and the wrapper starts the next
round rather than treating the cancellation as an abort. The cut is taken at the boundary and
not inside the parallel objective so that a capped run stays bit-identical at a fixed worker
width. A cancellation the caller or the time budget caused still reports
`context_canceled` or `time_budget`. The simple backend passes the cap to gonum's
`Settings.FuncEvaluations` and keeps gonum's own status string as the stop reason: nothing
compares Nelder-Mead runs against the population backends, so the count is what has to agree and
not the vocabulary.

The exchange rate a campaign needs to convert between the budgets is measured rather than
assumed. `optimizer.MayflyEvaluationsPerIteration()` returns the figure the
`mayflyEvaluationsPerIteration` constant records, whose comment names the date, the library
version and the configuration it was measured at: DESMA at a population of ten over twenty
iterations on the sphere. `optimizer.HansenPopulationSize(dims)` is the CMA-ES generation size at
a given dimensionality. `TestMayflyEvaluationsPerIterationIsRecorded` fails when a library
upgrade moves the mayfly figure by more than a fifth, which is the signal to remeasure the
constant rather than to widen the test.

## The CMA-ES backend

`optimizer.CMAESOptimizer` wraps [go-cma-es](https://github.com/CWBudde/go-cma-es) behind the
same `Optimizer` interface Mayfly and the gonum backend sit behind. It searches the unit cube,
like every other backend, so one step size is meaningful along every encoded axis.

Two covariance representations are offered. `separable` is the default and learns the diagonal
only: linear storage, linear per-generation work, and no cross-axis structure at all. `block`
learns a dense covariance per group of `CMAESOptimizer.BlockGroups`, which
`ParamCodec.BlockGroups()` fills in with one group per mode — amplitude, frequency and decay
together — and one group holding the scalars and the Chebyshev gains. A mode's three numbers are
the ones that genuinely trade against each other: move a partial in frequency and it is fitting a
different reference partial, so the amplitude and decay that match move with it. Correlations
between different modes are far weaker, so a three-by-three block per mode buys the structure
that matters without the dense matrix's cost. The library's dense mode is not exposed.

The step size is `InitialSigma`, 0.3 by default, which is Hansen's recommendation for a box and
covers a third of the cube. The population is `Lambda`, Hansen's `4 + floor(3 ln n)` by default,
which is twelve at the eighteen dimensions the default preset's four modes encode and fourteen
at the thirty an eight-mode seed encodes. Both are checked by `CMAESOptimizer.Validate` before a
run is accepted, except the block partition: it has to partition `[0, Dimension())` and the
dimension is only known inside `Optimize`, which is where that check lives.

The restart loop belongs to this wrapper, not to the library. `OptimizeWithRestartsContext`
implements IPOP and BIPOP against an evaluation budget, and a fit is bounded by wall-clock time,
so the wrapper runs cold restarts of its own until the time budget is spent. Run zero starts from
the caller's point, which is what carries `--preset` and a resumed checkpoint into the search;
every later run draws its mean uniformly in the cube, so it is independent of the basin the
previous runs settled in. A run ends on one of Hansen's own criteria — TolX, TolFun, TolXUp, the
condition number, the no-effect tests — and the next one starts; each run is cut by a derived
context so it stops mid-generation rather than overrunning the deadline, and a run that would
start with less than 5 % of the budget left is not started at all. `RestartLimit` bounds the
number of runs; zero means "until the budget is spent". Some budget is required: `Optimize`
refuses a run given neither an iteration cap, nor a time budget, nor a restart limit, because
the loop would then have no stopping rule of its own and a deadline on the context is not one
of the three; an evaluation cap counts as one of them.

`RunEvaluations` caps a single run rather than the whole loop, and zero gives every run the
remaining total. With `RestartLimit` zero it expresses "cold restarts of a fixed length until the
budget is spent", the shape CircleFit records as its open structural fix: a run that stagnates
early is abandoned on a schedule instead of spending the campaign's whole budget proving it. The
last run gets whatever is left, and a per-run cap below one generation is raised to one because
the library refuses anything smaller.

`LambdaGrowth` multiplies the population on every restart. Zero and one both mean a fixed
population; two is IPOP, where restart _k_ searches with `2^k` times the initial lambda and `Mu`
follows lambda/2 as it always does. `ResolvedCMAES.Lambda` stays the initial population and
`ResolvedCMAES.LambdaGrowth` reports the ladder, with a zero reported as one; `Progress.Lambda`
names the population of the run a report came from, and is zero for every other backend. A
restart is not started when what is left of the budget is smaller than its population, because
the library refuses a `MaxEvaluations` below `Lambda`; the loop then stops with
`max_evaluations`. `Validate` rejects a negative growth and a growth between zero and one.

The seed is reported through `OnResolve` the way Mayfly's is, and zero means "choose one and say
which". Run _k_ draws its library stream and its cold mean from two streams mixed out of the
reported seed, so a single restart can be reproduced on its own without the two generators
sharing a seed and replaying one sequence. The mixing, rather than the `seed + k` and
`seed - k - 1` offsets it replaced, is what keeps two runs apart as well as two streams: with an
offset, a run seeded _s_ and a run seeded _s+1_ share every restart but one, which silently
destroys the independence a paired campaign block depends on. `internal/optimizer/randomstream.go`
carries the reasoning, and Mayfly's rounds use the same derivation. `Progress.Restart` names the run in progress and
`Result.Restarts` counts the runs completed. `Result.Converged` is true only when `RestartLimit`
ended the loop and the last run stopped on a Hansen criterion: a loop the clock stopped has no
claim on convergence, whatever its final run ended on.

The dependency is pinned at v0.1.0. That version has a measured defect above a population of 256
in separable mode and 1024 in block mode, which does not bite at the populations this fit uses —
twelve by default, and `--cmaes-lambda` is not expected past 64. v0.2.0 changes the sampling
trajectory, which would make campaign numbers recorded before and after incomparable, so the
upgrade waits until the 8.6 figures are on record.

## The polish stage

The main search stops where its own stopping rule fires, which is rarely the bottom of the basin
it ended in. `optimizer.Polish` walks the incumbent the rest of the way down: `--polish
nelder-mead` runs the gonum simplex with `SimplexSize` at `--polish-sigma`, `--polish cmaes` runs
the CMA-ES backend with the same value as `InitialSigma` and `RestartLimit` 1. The default sigma
is 0.02, a fiftieth of the normalized box: the stage is there to refine the basin the search
found, not to search again, so the first simplex or generation has to land inside it. A restart
limit of one is part of the same reasoning, since a cold restart samples the whole box.

The stage searches under the `polish` profile, which weights the waveform term, and it runs over
the primary objective's own codec and encoded bounds — `ObjectiveFunction.WithMetric` rebuilds the
objective from the same template, reference, sample rate and configuration with only the metric
swapped, and it hands over the composite reference it already holds, so the reference is not
measured a second time. Dimension for dimension, an encoded vector means the same thing to both
objectives.

Acceptance is judged under the _primary_ metric, not under `polish`: the polished vector replaces
the incumbent only when its primary cost is strictly lower. Every report, `glockenspiel distance`
and every checkpoint score a preset under the metric the fit was started with, so a polish that
lowered the waveform term while raising the primary cost would ship a regression those very
reports go on to show. Both pairs of costs are printed, so an operator can see what the stage did
even when it was rejected:

```
polish (cmaes): primary 0.0421 -> 0.0388, polish 0.0517 -> 0.0402, accepted
```

The stage is CLI-only: the server and the browser fit do not run it. It neither resumes from a
checkpoint nor writes one, so the checkpoint stream stays the record of the search that
`--resume` continues. A cancelled context ends the stage without an error — the incumbent is
still the answer, and Ctrl-C during a polish costs the polish, not the fit.

## Tuning the Mayfly search

Mayfly ships eight algorithm dialects behind one configuration struct, and a
dialect is selected by a single flag on it. Naming one is enough to run:

```
--mayfly-variant gsasma
```

Anything beyond the dialect, the population and the seed is written in a JSON
tuning document, the same way `--bounds` narrows the search box:

```
--mayfly-tuning tuning.json
```

```json
{
  "cooling_rate": 0.97,
  "cooling_schedule": "linear",
  "nc_ratio": 0.5,
  "convergence": { "stagnation_iterations": 40 },
  "schedule": { "epochs": 4 }
}
```

Every key is optional and every omitted key keeps whatever the dialect already
chose, so an empty document is a no-op. Unknown keys and trailing content are
rejected rather than ignored: a misspelled knob that was silently dropped would
run the fit at the default while the caller believed it had been tuned. The full
key list, with the range each one is validated against, is in
[mayfly-tuning.md](mayfly-tuning.md); both that table and the browser form are
generated from `optimizer.MayflyTuningFields`, so neither can drift from the
validator.

A knob belonging to another dialect is an error, not a no-op. Mayfly ignores the
fields of the variants it is not running, so the value would land on the
configuration, change nothing, and leave the caller believing otherwise.

The document is applied **last**, after the dialect or preset and after the
scalar flags, so precedence is one sentence: a written key wins. The scalar
flags are themselves only a shorthand — each front end turns its flags into a
tuning document and overlays the caller's file on top — so there is exactly one
place a knob is written.

A document may also name `variant` or `preset`, so that one file describes a
whole run rather than only its knobs. Those are not tunable knobs and `Apply`
never writes them; they are read when the caller did not choose a dialect
itself. `--mayfly-variant` carries a default nobody wrote, so an unwritten flag
yields to a document that chooses — but an explicitly written one still wins,
and naming a dialect twice is an error rather than a silent preference.

`nc` is the one key where "omitted" and "zero" genuinely differ. `-1` derives
the offspring count from `nc_ratio`, `0` means no crossover at all, and omitting
the key leaves mayfly's own default alone.

### Presets

`--mayfly-preset` starts from one of mayfly's named configurations, which choose
a dialect and a set of knobs together. It cannot be combined with an explicit
dialect, since it already selected one. It does not choose the size of the run:
`--max-iter` and `--mayfly-pop` are applied after it, because the budget is the
caller's to set.

There used to be a `--mayfly-variant auto` that measured the landscape and
picked a dialect from it. It is gone. The classifier it called was rewritten in
mayfly v0.7.0 and the measurement never paid for itself: the sibling
`algo-piano` project compared all seven dialects on real audio objectives and
found the choice to be a small effect, with OLCE only marginally ahead of DESMA.
The same budget spent on iterations is the better trade, so naming a dialect --
or a preset -- is now the only way to choose one.

## Rounds and restarts

A run can be split into several shorter searches:

```json
{ "schedule": { "epochs": 4, "restarts": 1 } }
```

An **epoch** reseeds the next search from the best candidate found so far, so it
inherits that basin and refines it: the incumbent and half of each population
drawn around it, the same continuation profile the first round gives the
starting point. A **restart** does not chain: it draws a fresh population and
explores independently, which is how a run escapes a basin it should not have
entered. The defaults — one epoch, no restarts — reproduce a single search
exactly.

Which is worth reaching for is a measured question rather than a matter of
taste. `algo-piano`'s optimizer audit found round length to be the dominant
setting and warm starting the second-largest effect, while restarting cost more
than it bought at typical budgets, and larger populations were _worse_ at a
fixed evaluation budget. Warm rounds are therefore the default shape and cold
ones are opt-in.

`--max-iter` remains the total across every round; the schedule divides it and
gives the remainder to the earliest rounds. The split is never derived from the
population: the same audit found its own such derivation wrong by more than a
factor of two, because an iteration costs far more evaluations than a naive
count assumes. One `--time-budget` deadline covers the whole schedule.

## Convergence and early stopping

`Config.Convergence` is now set, so a run can stop before its budget is spent:

```json
{ "convergence": { "target_cost": 0.02, "stagnation_iterations": 40 } }
```

`Result.Converged` and the reported stop reason follow from it. A metaheuristic
never proves convergence, so the only claim made is the honest one: the run
stopped for a convergence criterion instead of exhausting its iterations.
`stopReason` can therefore now read `target_cost` or `stagnation` as well as
`maximum_iterations`.

A met `target_cost` ends the whole schedule, not just the round that reached it:
the target is the run's goal, so the remaining rounds would only spend renders
on a question already answered, and a cold restart could finish on
`maximum_iterations` and report the run as unconverged after it had converged.
Stagnation deliberately does not end the schedule — escaping a stagnated basin
is exactly what the next round is for.

A stagnation window is counted **within a round**, so one at least as wide as a
round is rejected rather than accepted. That is not a hypothetical: `algo-piano`
shipped the equivalent flag for a while and its audit recorded it as a measured
non-effect, because its rounds were always too short for the window to be
reached. Here the mistake is an error naming both numbers.

## Seeds

A zero seed means "choose one and report it", not "be unreproducible". It is
resolved before the configuration is built and reported alongside the resolved
dialect, and the fit command records it in the checkpoint. Feeding the reported
seed back in reproduces the run exactly.

One flag carries it. `--seed` feeds Mayfly, CMA-ES and the polish stage
alike, because a fit is one experiment however many engines it runs through,
and three flags meant three answers to "what seeded this run?". `--mayfly-seed`
and `--cmaes-seed` still work as deprecated aliases that write the same option;
combining one with `--seed` is refused rather than resolved, since there is no
reading of two different seeds for one run that is not a mistake.

This matters for resume: a `--seed 0` run continues its original random stream
rather than starting a new one. The checkpoint records the resolved seed in the
engine's own environment block, which is where it was recorded before the flags
were merged, so a checkpoint written by an older build resumes unchanged. A
resume takes that recorded seed unless the resume command names a seed itself.

The seed reaches mayfly as `Config.Seed` rather than as a caller-owned
`*rand.Rand`, so the value the library reports back in `Result.Seed` is the one
the run was actually built from instead of an invented time-based fallback.

### The version this is pinned to

Mayfly is pinned at **v0.7.1**. v0.7.0 was a correctness release: standard
attraction now uses the paper's scalar Cartesian distances, crossover retains
offspring sex, mutation draws its candidates from the matching incumbent
population, females take part in global-best and termination decisions, and
non-finite objective values are rejected rather than rewarded. Sequential and
parallel modes were also given the same proposal and commit semantics, so a run
at a fixed seed is bit-identical however many workers
`MayflyOptimizer.MaxWorkers` allows. v0.7.1 itself changed no behaviour.

**Every Mayfly number recorded here before 2026-09-02 was measured under
v0.6.0 and is not comparable to a run today.** That includes the benchmark
figures in `docs/user-guide.md`, the `algo-piano` results this file cites, and
the "roughly 47.7 evaluations per iteration" figure the code used to quote,
now remeasured as `mayflyEvaluationsPerIteration`. A
seeded trajectory from v0.6.0 does not reproduce, and the costs it reached are
not a baseline for v0.7.1.

## Provenance in written presets

A preset a fit writes carries an optional `provenance` block, `preset.Provenance`. It is metadata
and not a schema field: `Validate` ignores it, nothing in the synthesiser reads it, and a v1
document that carries one is still a v1 document, because nothing about it changes how the
parameters render.

It records `generated_by` (`glockenspiel fit` or `glockenspiel-campaign`), the build's `version`
and a `timestamp`, the `reference` as a path and a SHA-256, the `note`, `profile` and `seed`, an
`engine` block naming the backend and the fields that differ between backends (covariance,
variant, lambda, population, restarts), the `score` and the full `terms` breakdown of the shipped
vector, the `evaluations` spent, and the `libraries` versions the run was built against.

The point is that a fitted preset found months later can answer where it came from without the
run directory beside it, and that the reference hash rather than the reference's path is what
says two presets were fitted against the same recording. Nothing reads the block yet. Phase 8.6's
promotion rule is the first thing that will.

## Checkpoints and iteration counts

The checkpoint format is version `2.0`. Version `1.0` encoded decay linearly, so loading one under
the current logarithmic encoding would silently resume at different parameters. Old checkpoints
are therefore rejected with an explanatory error rather than reinterpreted.

Two counters have intentionally different meanings:

- `Iteration` counts progress reports and orders `checkpoint_*.json` files.
- `OptimizerIterations` is the backend's own iteration count, in the same unit as `--max-iter`.

Only `OptimizerIterations` may be subtracted from the remaining iteration budget on resume, and
it is also what `--checkpoint-interval` counts. Pacing the cadence by report count tied it to
`--report-every` and to how talkative a backend happens to be, so the same interval bought
wildly different checkpoint spacing per engine. A checkpoint is written once at least
`--checkpoint-interval` optimizer iterations have passed since the last one -- "at least",
because a backend reports on its own schedule and may step past the exact multiple -- plus the
final one every run writes. `--report-every` keeps its own meaning: it is about printing.

The checkpoint also records the resolved worker count in `OptimizerState.Workers`. A resume
reuses it unless `--workers` is written again, so a fit continued on a machine with a different
CPU count evaluates the same number of candidates at a time as the run it is continuing.
Mayfly checkpoints also retain the variant, preset, population, effective seed, round schedule,
and tuning document, but neither backend stores its complete internal population or simplex.
Resume continues from the best encoded parameter vector and the remaining budget; it is a coarse
restart, not a byte-for-byte continuation.

Checkpoint writes are atomic and durable: data is written and synced through a temporary file,
renamed into place, and followed by a directory sync where the platform supports it.
