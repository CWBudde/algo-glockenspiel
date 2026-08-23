# Optimizer design

The fitting stack in `internal/optimizer` renders the current model against a reference WAV and
scores each candidate with the RMS, log-RMS, or spectral objective. The CLI workflow and flags
are documented in [user-guide.md](user-guide.md); this note records the less visible contracts
that implementations and checkpoints depend on.

## Parameter space

Both optimizer backends search the same unit cube. `ParamCodec` first encodes model parameters,
using logarithmic coordinates for positive quantities such as frequencies and decay times, and
then `Bounds.Normalize` maps every encoded dimension to `[0,1]`. This keeps step sizes comparable
across dimensions whose physical ranges differ by orders of magnitude.

Candidates are clamped at the boundary. They are not mirrored: mirroring makes the objective a
folded, many-to-one map and gives a local optimizer artificial continuations to chase across a
bound. User-supplied `--bounds` are hard constraints. `ObjectiveConfig.StrictBounds` prevents the
codec from widening them to contain the template preset, and the initial point is clamped into
the requested box.

## Objective evaluation

The objective keeps the reference and candidate in floating point; it does not quantize through
PCM16 before scoring. `ProjectToPCM16Domain` remains only for reporting. The spectral metric is a
multi-frame STFT over the whole signal rather than a comparison of a single frame.

Onset alignment is enabled by default because a small leading offset can reverse the phase of a
high partial and make the correct parameters score worse than incorrect ones. Gain normalization
is optional and off by default: it is useful when the recording level is unknown, but it makes
the model's amplitude parameters unidentifiable.

`ObjectiveFunction.Evaluate` is safe for concurrent calls. Each evaluation borrows independent
mutable render state from a pool while the reference, codec, and alignment plan remain immutable,
so population-based backends can evaluate candidates in parallel without racing.

## Reading the reference

References are loaded through `internal/wavio`, which exists because the obvious way to read a
WAV here is wrong. `go-audio/wav` decodes every sample format as an integer, so a 32-bit IEEE
float file comes back as its own bit patterns divided by 2^31 — a square wave at roughly ±0.5
for any recording at a sane level. A 32-bit float WAV is what a DAW exports by default, so
every fit against one was fitting a square wave, silently and plausibly, and so was every
legacy-reference regression test.

`internal/wavio` is the single loader for the CLI, the server and the tests; there were three
copies before, which is how the defect survived in all of them at once. The float fixture is
documented in `testdata/reference/README.md`.
## Tuning the Mayfly search

Mayfly ships seven algorithm dialects behind one configuration struct, and a
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

`nc` is the one key where "omitted" and "zero" genuinely differ. `-1` derives
the offspring count from `nc_ratio`, `0` means no crossover at all, and omitting
the key leaves mayfly's own default alone.

### Presets and automatic selection

`--mayfly-preset` starts from one of mayfly's named configurations, which choose
a dialect and a set of knobs together. It cannot be combined with an explicit
dialect, since it already selected one. It does not choose the size of the run:
`--max-iter` and `--mayfly-pop` are applied after it, because the budget is the
caller's to set.

`--mayfly-variant auto` measures the landscape and picks a dialect from it. It
is bounded on purpose. Mayfly's classifier samples the objective, estimates a
gradient across every dimension, and then runs three short searches through a
helper that calls `Optimize` rather than `OptimizeContext` — so it observes
neither cancellation nor the time budget. On this model that is thousands of
real renders before the fit starts. The wrapper therefore caps it at 400
evaluations, or a tenth of the time budget, whichever runs out first, and counts
every one of them in the run's evaluation total.

It is worth knowing what that budget buys. The sibling `algo-piano` project
compared all seven dialects on real audio objectives and measured the choice as
a small effect, with OLCE only marginally ahead of DESMA. The same budget spent
on iterations is usually the better trade.

## Rounds and restarts

A run can be split into several shorter searches:

```json
{ "schedule": { "epochs": 4, "restarts": 1 } }
```

An **epoch** reseeds the next search from the best candidate found so far, so it
inherits that basin and refines it. A **restart** does not chain: it draws a
fresh population and explores independently, which is how a run escapes a basin
it should not have entered. The defaults — one epoch, no restarts — reproduce a
single search exactly.

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

This matters for resume: a `--mayfly-seed 0` run now continues its original
random stream rather than starting a new one.

## Checkpoints and iteration counts

The checkpoint format is version `2.0`. Version `1.0` encoded decay linearly, so loading one under
the current logarithmic encoding would silently resume at different parameters. Old checkpoints
are therefore rejected with an explanatory error rather than reinterpreted.

Two counters have intentionally different meanings:

- `Iteration` counts progress reports and orders `checkpoint_*.json` files.
- `OptimizerIterations` is the backend's own iteration count, in the same unit as `--max-iter`.

Only `OptimizerIterations` may be subtracted from the remaining iteration budget on resume.
Mayfly checkpoints also retain the variant, preset, population, effective seed, round schedule,
and tuning document, but neither backend stores its complete internal population or simplex.
Resume continues from the best encoded parameter vector and the remaining budget; it is a coarse
restart, not a byte-for-byte continuation.

Checkpoint writes are atomic and durable: data is written and synced through a temporary file,
renamed into place, and followed by a directory sync where the platform supports it.
