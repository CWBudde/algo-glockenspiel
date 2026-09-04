# User Guide

This guide focuses on the three supported end-user workflows in this repository:

1. rendering notes with `glockenspiel synth`
2. fitting presets with `glockenspiel fit`
3. scoring a preset against a reference with `glockenspiel distance`

## Render With `synth`

The `synth` command renders one note from one preset to a mono WAV file.

Basic example:

```bash
glockenspiel synth \
  --preset assets/presets/default.json \
  --note 69 \
  --velocity 100 \
  --duration 2.0 \
  --sample-rate 44100 \
  --output out/a4.wav
```

Useful variations:

Render a higher note from the same preset:

```bash
glockenspiel synth \
  --preset assets/presets/default.json \
  --note 72 \
  --velocity 110 \
  --duration 2.0 \
  --output out/c5.wav
```

Stop automatically once the tail is quiet:

```bash
glockenspiel synth \
  --preset assets/presets/default.json \
  --note 69 \
  --velocity 100 \
  --duration 5.0 \
  --auto-stop \
  --decay-dbfs -80 \
  --output out/a4-short.wav
```

### What The Flags Do

- `--preset`: preset JSON to load; omit it to use the preset built into the binary
- `--output`: destination WAV file
- `--note`: MIDI note number used for frequency scaling
- `--velocity`: strike strength, `0..127`
- `--duration`: maximum render length in seconds
- `--sample-rate`: output WAV sample rate
- `--auto-stop`: trims the render once the tail stays below threshold
- `--decay-dbfs`: threshold used by auto-stop

### Practical Advice

- Start with `--duration 2.0` or `3.0`.
- Use `--auto-stop` when batch-rendering many notes.
- Keep `--sample-rate` equal to the sample rate of any reference material you plan to compare against later.

## Fit With `fit`

The `fit` command optimizes preset parameters against a mono reference WAV.

The default optimizer is `mayfly` in one warm round plus fifteen cold restarts, which is the arm
Phase 8.6's registered campaign measured as the best of five on the C5 recording
([training.md](training.md)). A plain run needs no backend flag at all:

```bash
glockenspiel fit \
  --reference testdata/reference/legacy_synth_a4.wav \
  --preset assets/presets/default.json \
  --output out/fitted-a4.json \
  --time-budget 60s \
  --polish cmaes \
  --work-dir out/fit-a4
```

Basic local-refinement example, with the standalone Nelder-Mead backend that
used to be the default:

```bash
glockenspiel fit \
  --reference testdata/reference/legacy_synth_a4.wav \
  --preset assets/presets/default.json \
  --output out/fitted-a4.json \
  --optimizer simple \
  --metric rms \
  --max-iter 100 \
  --time-budget 30s \
  --work-dir out/fit-a4
```

Broader search with Mayfly under the partial-heavy profile:

```bash
glockenspiel fit \
  --reference testdata/reference/legacy_synth_a4.wav \
  --preset assets/presets/default.json \
  --output out/fitted-a4.json \
  --optimizer mayfly \
  --mayfly-variant desma \
  --mayfly-pop 10 \
  --metric placement \
  --max-iter 200 \
  --time-budget 60s \
  --work-dir out/fit-a4
```

Covariance-adapting search with CMA-ES, restarting until the budget is spent:

```bash
glockenspiel fit \
  --reference testdata/reference/legacy_synth_a4.wav \
  --preset assets/presets/default.json \
  --output out/fitted-a4.json \
  --optimizer cmaes \
  --cmaes-covariance block \
  --time-budget 60s \
  --work-dir out/fit-a4
```

Resume from the latest checkpoint in the work directory:

```bash
glockenspiel fit \
  --reference testdata/reference/legacy_synth_a4.wav \
  --preset assets/presets/default.json \
  --output out/fitted-a4.json \
  --work-dir out/fit-a4 \
  --resume
```

### The Browser Fit Is A Demonstration, Not A Campaign Path

`glockenspiel serve` hosts a web app whose Optimize tab can also start a fit. Two different
things answer that tab's requests, and they are not interchangeable:

- When `glockenspiel serve` itself is reachable, the tab's requests go over HTTP to the same
  `fitrun` engine this command and the training campaign use, run at full native speed on every
  core. This is the serious path: it is what a campaign, or any fit whose result you plan to
  keep, should go through, and its run directories, history and restart recovery are documented
  in [serve.md](serve.md).
- When it is not — GitHub Pages, or `serve` not running — the tab falls back to a second path
  that fits entirely inside the browser, compiled to WebAssembly. It exists so the algorithm can
  be tried with no terminal, no Go toolchain and no server, and it says so in the UI. It runs
  single-threaded: Go's `js/wasm` target has no goroutines to spare, so Mayfly and CMA-ES
  evaluate one candidate at a time instead of one per core, there is no run history across a
  reload, and it is markedly slower than either the CLI or the server path. See
  [web-app.md](web-app.md#fitting-on-pages) for how it is wired.

Neither browser path is this command: both call the same optimizer code `fit` does, but a
campaign, a batch of presets, or anything scripted belongs on this command line or on
`glockenspiel serve`'s HTTP API, not in a browser tab.

### What The Flags Do

- `--reference`: mono WAV file to match
- `--preset`: starting preset JSON; omit it to use the preset built into the binary
- `--bounds`: JSON file narrowing the search box, see [Narrowing The Search With `--bounds`](#narrowing-the-search-with---bounds)
- `--output`: destination fitted preset JSON
- `--note`: note number used when rendering candidates
- `--velocity`: strike velocity for candidate renders
- `--sample-rate`: must match the reference WAV sample rate
- `--optimizer`: `simple`, `mayfly` (the default since Phase 8.6, measured) or `cmaes`; all three stay selectable
- `--metric`: a composite profile — `balanced` (the default), `placement` or `polish` — or a single legacy term, `rms`, `log` or `spectral`. See [Choosing Optimizer And Metric](#choosing-optimizer-and-metric)
- `--downmix`: which channel of a multi-channel reference the fit sees, `first` (the default) or `mean`
- `--window`: cut the reference to this length after its onset instead of where the strike ends; by default the loader cuts at a second event or where the tail stops falling
- `--keep-level`: keep the reference at the file's level instead of peak-normalising it; only a legacy metric without `--normalize-gain` can tell the difference
- `--analysis`: an `analysis.json` from `glockenspiel analyze` whose partials the partial term, the seed and the frequency box use; by default the reference is measured before the fit starts
- `--modes`: where the starting modes come from. `0` (the default) seeds one mode per partial the analysis lists, at its frequency, attack level and half-life; `N` seeds the strongest `N`; `-1` keeps the starting preset's own modes. A seeded fit from a v1 preset writes a v2 preset, because v1 holds exactly four modes
- `--max-iter`: iteration cap passed to the optimizer
- `--max-evals`: cap on the objective evaluations the whole search may spend; `0` (the default) leaves it uncapped and only `--max-iter` and `--time-budget` bound the run. It is the budget two backends can be compared on, because an evaluation is one audio render whichever backend spends it while an iteration means a generation to one and a simplex step to another. A generation is the smallest unit a population method can abandon, so a run may overrun the cap by at most one of them, and a run the cap ended reports `stop=max_evaluations`
- `--time-budget`: wall-clock budget as a Go duration, for example `30s` or `10m`; a bare number is still read as seconds
- `--align`: time-align each candidate to the reference before scoring, on by default. Leave it on for recorded references: a few samples of offset invert the phase of a high partial, so the correct parameters would score worse than incorrect ones
- `--normalize-gain`: under a legacy metric, divide out the scalar gain that best matches the reference level, off by default. Use it when the reference level is unknown; it makes the model's amplitude parameters unidentifiable, so leave it off when the level is meaningful. A composite profile solves its gain in closed form regardless
- `--report-every`: progress print interval, counted in the chosen optimizer's own iterations. The default is 10, and 1 under `mayfly`, because a mayfly iteration is a whole generation, roughly fifty renders, against about one for a simple major iteration
- `--checkpoint-interval`: write a checkpoint once this many of the backend's own iterations have passed since the last one, counted in the same unit as `--max-iter` and independent of `--report-every`; `0` disables checkpointing entirely, including the final checkpoint. A backend reports on its own schedule, so the spacing is "at least this many" rather than exactly
- `--seed`: the one random seed for every backend, Mayfly, CMA-ES and the polish stage alike. `0` (the default) picks a seed, prints it, and records it in the checkpoint, so the run stays reproducible and a resume continues the same stream. `--mayfly-seed` and `--cmaes-seed` remain as deprecated aliases that write the same option; combining one with `--seed` is an error. Run _k_ of a restarting fit, and each Mayfly round, draws a stream mixed out of the seed rather than offset from it, so two fits whose seeds differ by one share no restart
- `--workers`: how many goroutines evaluate candidates in parallel; `0` (the default) follows the machine's CPU count. The resolved width is printed, recorded in the checkpoint, and reused by `--resume` unless `--workers` is written again, so a fit continued on another machine reproduces the run it is continuing rather than the machine it lands on
- `--work-dir`: stores checkpoints and `fitted_output.wav`, resolved relative to the current directory (default `out/fit`)
- `--resume`: restart from the latest `checkpoint_*.json` in `work-dir`
- `--mayfly-variant`: which of Mayfly's eight dialects to run. The measured effect of the choice is small, so it is rarely the setting worth reaching for first
- `--mayfly-pop`: Mayfly male/female population size. Bigger is not better at a fixed budget: larger populations were measured as _worse_, because each iteration costs more
- `--mayfly-preset`: start from one of Mayfly's named configurations, which pick a dialect and its knobs together. Cannot be combined with `--mayfly-variant`, and does not override `--max-iter` or `--mayfly-pop`
- `--mayfly-tuning`: JSON file setting individual Mayfly knobs, see [Tuning Mayfly](#tuning-mayfly)
- `--mayfly-epochs`: split the run into this many warm rounds, each reseeded from the best result so far (default `1`)
- `--mayfly-restarts`: append this many cold rounds, each starting from a fresh random population (default `0`)
- `--mayfly-stagnation`: stop a round after this many iterations without progress. Must be narrower than a round, or it could never fire. Writing `0` switches the rule off, which is how a `--mayfly-preset` or a tuning document that brought its own stagnation window is overridden; leaving the flag out changes nothing
- `--mayfly-target-cost`: stop once the best cost reaches this value
- `--mayfly-nc`: crossover offspring per iteration; `-1` derives it from the ratio, `0` disables crossover
- `--mayfly-nc-ratio`: offspring count as a multiple of the population, used only when `--mayfly-nc` is `-1`. Writing `0` keeps the variant's own ratio, and it is written into the tuning document like any other value; leaving the flag out changes nothing
- `--mayfly-selection`: `rank` or `tournament` parent selection
- `--cmaes-covariance`: what CMA-ES learns, `separable` (the default) for the diagonal only or `block` for a dense matrix per mode. A mode's amplitude, frequency and decay are the numbers that genuinely trade against each other, which is the structure `block` buys
- `--cmaes-lambda`: population size per generation; `0` (the default) takes Hansen's `4 + floor(3 ln n)`, which is twelve at the eighteen dimensions the default preset's four modes encode and fourteen at the thirty an eight-mode seed encodes
- `--cmaes-sigma`: initial step size as a fraction of the normalized search box, at most `1` (default `0.3`, which covers a third of it). A `0` takes that same default, as a zero population and a zero seed take theirs
- `--cmaes-restarts`: number of cold runs; `0` (the default) restarts until the budget is spent. Each run after the first starts from a fresh mean drawn uniformly in the box, so it is independent of the basin the previous one settled in
- `--cmaes-run-evals`: evaluations one cold run may spend before the next restart; `0` (the default) gives every run whatever is left of `--max-evals`. Together with `--cmaes-restarts 0` this is "restart on a fixed schedule until the budget is spent", which is how a campaign arm expresses a ladder of equal cold runs
- `--cmaes-lambda-growth`: the factor the population is multiplied by on every restart; `0` or `1` (the default) keeps it fixed, `2` is IPOP. Each run then ends on Hansen's own criteria and the next doubles the population, until the evaluation budget ends the ladder. The population of the run in progress is reported to every progress callback and recorded in each trace line
- `--polish`: run a local refinement stage after the main search, `none` (the default), `nelder-mead` or `cmaes`. The stage searches under the `polish` profile from the search result, but the result is kept only when it lowers the cost under the metric the fit was started with. See [The polish stage](optimizer.md#the-polish-stage)
- `--polish-iterations`: iteration cap for the polish stage (default `200`)
- `--polish-budget`: wall-clock budget for the polish stage as a Go duration; `0` (the default) leaves it uncapped, so only `--polish-iterations` ends the stage
- `--polish-sigma`: the stage's initial step as a fraction of the normalized search box (default `0.02`): the Nelder-Mead simplex size, or the CMA-ES step size

### Tuning Mayfly

`--mayfly-variant` and `--mayfly-pop` cover the common cases. Everything else
Mayfly exposes — the swarm coefficients, each dialect's own knobs, early
stopping, and the round schedule — is written in a JSON document, the same way
`--bounds` narrows the search box:

```bash
cat > tuning.json <<'JSON'
{
  "cooling_rate": 0.97,
  "cooling_schedule": "linear",
  "nc_ratio": 0.5,
  "convergence": { "stagnation_iterations": 40 },
  "schedule": { "epochs": 4 }
}
JSON

glockenspiel fit \
  --reference recordings/a4.wav \
  --output out/fit/a4.json \
  --optimizer mayfly --mayfly-variant gsasma \
  --mayfly-tuning tuning.json \
  --max-iter 2000 --time-budget 5m
```

Every key is optional, and an omitted key keeps whatever the dialect already
chose, so an empty document changes nothing. Unknown keys are rejected rather
than ignored: a misspelled knob that was silently dropped would run the fit at
the default while you believed you had tuned it. The full key list, with the
range each is validated against, is in [mayfly-tuning.md](mayfly-tuning.md).

A knob belonging to a different dialect is an error rather than a no-op, because
Mayfly ignores the fields of variants it is not running — the value would land
on the configuration, change nothing, and leave you believing otherwise.

The document is applied after the scalar flags, so a key written in both places
takes its value from the file.

#### Rounds

`--mayfly-epochs 4` runs four shorter searches instead of one long one, each
reseeded from the best result so far. `--mayfly-restarts` appends cold rounds
that start from a fresh random population instead. `--max-iter` stays the total
across every round, and one `--time-budget` covers them all.

Warm rounds are the ones worth reaching for: measured against the sibling
`algo-piano` project's audio objectives, round length was the dominant setting
and warm starting the second-largest effect, while cold restarts cost more than
they bought at typical budgets.

### Narrowing The Search With `--bounds`

By default `fit` searches the full parameter box, which spans several decades in
frequency. That is a lot of empty space for the optimizer to cross. When you
already know roughly where the answer lies, pass `--bounds` with a JSON file that
narrows the dimensions you care about:

```json
{
  "input_mix": [0.0, 2.0],
  "filter_freq": [500.0, 8000.0],
  "amplitude": [-1.0, 1.0],
  "frequency": [400.0, 12000.0],
  "decay_ms": [50.0, 400.0],
  "harmonic_gain": [0.0, 1.0]
}
```

`frequency` is a mode's frequency in hertz as the preset writes it. Without a
bounds file the fit narrows it to half the reference's fundamental up to 0.45
of the sample rate, and `decay_ms` to what a preset at the starting preset's
note may carry; a bounds file replaces the whole box, so its `frequency` key is
the box that runs. Two keys from before Phase 8.3 are refused with a reason:
`base_frequency`, which is no longer searched, and `frequency_mult`, which
became `frequency`.

Every key is optional and holds a `[min, max]` pair; an omitted key keeps the
default bound, so narrowing a single dimension needs a one-line file:

```bash
glockenspiel fit \
  --reference testdata/reference/legacy_synth_a4.wav \
  --output out/fitted-a4.json \
  --bounds bounds/a4.json
```

Unknown keys, non-finite numbers and ranges whose minimum is not below their
maximum are rejected.

### Choosing Optimizer And Metric

Use `simple` when:

- your starting preset is already close
- you want faster, more predictable local refinement
- you are iterating frequently
- you care more about optimizer iterations and lower allocation cost than broad exploration

Use `mayfly` when:

- the starting preset is weak
- the search surface is rough
- `simple` gets stuck too early
- you are willing to spend more memory and wall-clock time per run to search more broadly

Use `cmaes` when:

- the starting preset is weak and the budget is a wall-clock one, so a run that converges early
  is better spent on a cold restart than on more generations
- the dimensions trade against each other, which `--cmaes-covariance block` learns per mode
- you want the search to adapt its own step size rather than be told one

Which of the three actually fits better on this objective is not a question a single run answers,
and this guide does not claim one. [docs/campaign.md](campaign.md) describes the harness that
compares them over paired seed blocks at a matched evaluation budget, and `just campaign-smoke`
runs it end to end in seconds.

Benchmark snapshot from `internal/optimizer/perf_test.go` on 2026-09-02, short legacy fit,
Go 1.26.0 on twelve hardware threads (`go test ./internal/optimizer -run '^$' -bench LegacyShort -benchmem`):

- `simple`: `61.0 iter/s`, `1138 eval/s`, `147.6 convergence-ms`, `21.4 MB/op`
- `mayfly` (`desma`, population 10): `115.8 iter/s`, `4292 eval/s`, `172.6 convergence-ms`, `95.7 MB/op`

Both rows were measured under Mayfly v0.6.0. v0.7.0 changed the update rules, so any Mayfly
number recorded here before 2026-09-02 describes a different search; see
[optimizer.md](optimizer.md#the-version-this-is-pinned-to). `cmaes` has no row here because it
arrived with Phase 8.4 and the benchmark predates it; [training.md](training.md) has a smoke run
of all three engines on the C5 recording, which is a wiring check rather than a comparison.

An earlier snapshot in this place was taken before `internal/wavio` learned to decode 32-bit
float WAVs, so it timed fits against a square wave; it has been replaced, not corrected. Read
this one as throughput only: it says how many renders each backend gets through per second,
not which one finds a better fit. `mayfly` evaluates candidates in parallel and `simple` does
not, which is most of the evaluation-rate gap.

The default, `balanced`, scores every candidate on ten terms at once — the partials' pitch,
level, decay, and which are missing or extra; the log spectrum at a fine and a coarse resolution
above the reference's own noise floor; the broadband envelope and its decay slope; and the
aligned waveform residual — and folds them into one number in `[0, 1]`. Each term is printed
under every progress line and as a table at the end, so a run says _which_ thing is wrong rather
than how wrong in total. [optimizer.md](optimizer.md#the-composite-objective) defines each term.
The other two profiles weigh the same terms differently:

- `placement` puts most of its weight on where the partials are and whether they are all there,
  and almost none on the waveform. It is the profile for a global search from far away.
- `polish` puts half its weight on the waveform. It only makes sense once every partial is
  within a few cents, which is what a local refinement from a good result is.

The legacy single-term metrics remain for comparison with older runs:

- `rms` is the aligned time-domain difference. Its capture range is a few cents per partial:
  [training.md](training.md) measured the waveform gain against the recording at −52 to −93 dB
  for every shipped preset, which is no correlation at all.
- `log` is a monotone transform of `rms` with the same minimiser.
- `spectral` is the coarse STFT error with every bin counted, which the review found outvoted by
  empty bins.

Before switching profiles, run `distance` on the current result and look at which term is
large — see [Score With `distance`](#score-with-distance) and the tables in
[training.md](training.md).

Practical conclusion:

- start with `balanced`; measure with `distance` before changing anything
- a large `partial_missing` or `partial_cents` with small spectral terms means the search has not
  found the partials yet: try `placement`, or a wider search
- small partial terms with a large `waveform` means the partials are placed and the phase is
  not: `polish` from that result

## Score With `distance`

`distance` renders a preset once and prints what the fit objective would score it at, without
searching. It goes through the same codec, alignment, gain and metric code that scores a
candidate during a fit, so its numbers are the fit's numbers.

```bash
glockenspiel distance \
  --reference testdata/reference/legacy_synth_a4.wav \
  --preset assets/presets/default.json \
  --note 69
```

The reference is read the way `fit` reads it — one channel, cut to its first strike,
peak-normalised — and the first line says what the loader did. The legacy table has three rows,
one per policy a legacy fit can run under: `raw` compares sample for sample at natural level,
`aligned` is what an aligned fit scores, and `aligned+gain` is a `--normalize-gain` fit. Every
row carries the `rms`, `log` and `spectral` terms, the lag the alignment chose, and the
least-squares gain that best matches the render to the reference. The gain is measured under
every policy and divided out only under the third, so a gain far below 0 dB says the waveform
correlation could not see the reference; the baseline in [training.md](training.md) shows what
that looks like against a real recording.

Below that, the composite objective's breakdown: every term with its value, its norm, its weight
under `balanced` and its share of the score, then the score under each of the three profiles,
the gain applied, the waveform gain, and how many of the reference's partials the preset's modes
matched. This is the table a fit prints at its end, for a preset that was not fitted.

Below the table the report says where the preset sits in the search box: which dimensions are
on an edge, and which edges of the default box had to move to contain the preset at all. A
fitted preset with many pinned dimensions is one the search wanted to push further.

### What The Flags Do

- `--reference`: WAV file to score against
- `--downmix`, `--window`, `--keep-level`, `--analysis`: how the reference is read, as for `fit`
- `--preset`: preset JSON to score; omit it to score the preset built into the binary
- `--bounds`: JSON search box, kept strict as `fit` keeps it; a preset outside it is scored clamped into it, and the report says so
- `--note`, `--velocity`: how the preset is rendered
- `--sample-rate`: must match the reference WAV
- `--json`: the same report as JSON, for scripts, with the composite terms under `metrics` and the profile scores under `scores`; a term that could not be computed is `null`

## Measure With `analyze`

`analyze` reads a recording and says what is in it before anything is fitted to it: where the
strike starts and ends, what level it sits at, and which partials it holds.

```bash
glockenspiel analyze --reference testdata/reference/glockenspiel_c5.wav
```

The first block is the cut. The file is reduced to one channel, cut from the onset to where the
strike stops being the only thing in the file — a second event in the tail, or the tail no
longer falling — and scaled so its peak is full scale. Each of those decisions is printed, and
`--window 1s` or `--keep-level` overrides the last two. The table below it lists each partial
strongest first: its `level` against the strongest, its `amplitude` in dB against full scale of
the cut, its `attack` — the level its decay extrapolates to at the strike, which is what a model
mode's amplitude has to reach — and its `half-life`, with the T60 that half-life implies.

`--output analysis.json` writes the same thing as JSON, which is what a fit will read to size its
search space, and `--trimmed-out reference.wav` writes the cut itself so that `fit` and
`distance` can be pointed at the strike rather than the file.

### What The Flags Do

- `--reference`: WAV file to measure
- `--output`: write the analysis as JSON to this path
- `--trimmed-out`: write the cut, normalised reference as a 16-bit mono WAV to this path
- `--downmix`: `first` keeps channel zero (default); `mean` averages the channels
- `--window`: cut this long after the onset instead of finding where the strike ends
- `--keep-level`: leave the level alone instead of normalising the peak to full scale
- `--frame-size`, `--max-partials`, `--min-level`, `--min-frequency`: the spectrum window in samples, how many partials to report, how deep below the strongest to look, and where to stop ignoring hum
- `--json`: print the analysis as JSON instead of the text report

## Parameter Guide

### The Three Schema Versions

A preset declares its schema in a top-level `version` field, and the loader holds it to that version's rules rather than accepting the union of all three. A document carrying a field newer than its own version is rejected with a message naming the field, rather than rendering differently than its version claims.

**v1 (`"1.0"`)** is the original schema: exactly four modes, no per-mode harmonics, and a Chebyshev shaper that always sits on the excitation.

**v2 (`"2.0"`)** adds three things:

- a mode array of one to 512 modes, because the oscillator bank sizes itself at runtime. The bounds are real and enforced on load: an empty array is rejected by the v2 rules, and more than `model.MaxModes` (512) by `ValidateBarParams`
- `modes[i].harmonics`, a per-mode gain list that expands that mode into one rotor per integer-multiple partial
- `chebyshev.stage`, either `"excitation"` or `"output"`, making explicit the placement v1 left implicit

**v3 (`"3.0"`)** is what new presets are written in. It adds one field, `output_gain_db`.

One field earned a version of its own because of what an older reader does with it. A v2 reader meeting `output_gain_db` accepts the document, ignores the key it does not know, and renders at unity — up to 60 dB from the level the preset was calibrated to, with nothing anywhere reporting a problem. A reader that does not know v3 refuses the document instead, which is the only safe behaviour when the unknown field decides how loud the instrument is. The rule the ladder follows: a field a reader can ignore without changing the sound may extend a version, and a field it cannot must start a new one.

Saving preserves the version a preset was loaded with, so a hand-written v1 document stays v1 through a load and a save. A fit is the exception, and deliberately: it writes the level it measured, so its result is a v3 document whatever the template was. Converting on its own is explicit and, today, library-only: `preset.Upgrade` makes the v1 defaults explicit and restamps the document as v3. No CLI flag reaches it yet — writing a v3 preset by hand means editing the `version` field and the fields it unlocks.

### Fields

The top-level `parameters` object holds:

- `input_mix`: amount of dry filtered excitation mixed into the resonant output
- `filter_frequency`: lowpass cutoff for the excitation path, in Hz
- `base_frequency`: reference tuning for the preset note. It never reaches the audio, and a fit writes the starting preset's value through rather than searching it
- `output_gain_db`: v3 only, the level the bar renders at, in dB, bounded to ±60. Zero is unity and the field is omitted when it is zero, so a preset written without it renders exactly as it always did. A fit does not search this either: the objective solves the level in closed form and subtracts it from every term, leaving nothing to follow along it, so a fit measures the level from its finished render and writes it — targeting 3 dB below full scale, measured at 44.1 kHz, velocity 100 and the preset's own note. That measurement point is the one the promotion rule already uses, so a fit and the level test agree by construction; level is nonlinear in velocity, because the shaper's input clamp saturates, so the target only means something with its velocity named. It is a v3 field because a loader that ignored it would play the preset at the wrong level rather than refuse it
- `modes`: the resonant partials, exactly four in v1 and one to 512 in v2
- `chebyshev.enabled`: enables harmonic shaping
- `chebyshev.stage`: v2 only, `excitation` (the v1 behaviour, and the default) or `output`
- `chebyshev.harmonic_gains`: gain per generated harmonic. The shaper is
  DC-free — it subtracts its own value at zero, so silence in gives silence out —
  which matters most at the `excitation` stage, where anything it emits for a
  silent input becomes a DC excitation the oscillator bank never resolves

### Reading Mode Parameters

Each mode has:

- `amplitude`: linear mode gain
- `frequency`: modal frequency in Hz
- `decay_ms`: decay time in milliseconds
- `harmonics`: v2 only, optional, at most 64 entries (`model.MaxHarmonics`). Gains for the integer multiples of this mode's frequency; harmonic `k` runs at `(k+1) * frequency`, shares the mode's decay, and carries `amplitude * harmonics[k]`. Omitting the list leaves the mode as a single unity-gain fundamental, and different modes may carry different counts

In practice:

- the first mode usually dominates the perceived pitch
- higher modes shape brightness and attack character
- very short decay values mostly affect the transient
- harmonics are additional rotors, not waveshaping, so they cost render time in proportion to their count

One limit worth knowing before a long fit: the optimizer does not search per-mode harmonic gains. It sizes its parameter vector from the template preset's mode count and carries `harmonics` through unchanged.

## Troubleshooting

### `reference sample rate ... does not match requested sample rate`

Your `--sample-rate` must equal the WAV file sample rate. Either:

- rerun with the reference sample rate, or
- resample the WAV before fitting

### `unsupported metric "..."`

Use one of:

- `balanced`, `placement` or `polish`, the composite profiles
- `rms`, `log` or `spectral`, the legacy single terms

### `unsupported optimizer "..."`

Use one of:

- `simple`
- `mayfly`

### `mayfly-pop must be >= 2`

Set `--mayfly-pop` to `2` or higher.

### Output renders sound wrong or too short

Check:

- preset validity
- note number
- velocity
- `--auto-stop` and `--decay-dbfs` on `synth`
- reference/sample-rate mismatch on `fit`

### Resume did not seem to do anything

`--resume` only looks for the latest `checkpoint_*.json` in `--work-dir`. Make sure:

- `--work-dir` is the same directory used in the earlier run
- at least one checkpoint file exists
- the checkpoint matches the current preset/metric/dimension setup

When a checkpoint contains optimizer state, resume restores:

- optimizer identity (`simple`, `mayfly` or `cmaes`) unless you explicitly override it
- metric unless you explicitly override it
- the best encoded parameter vector found so far
- remaining iteration budget relative to the saved checkpoint iteration
- Mayfly variant and population, and the CMA-ES covariance, population and step size, unless you explicitly override them
- the resolved seed, unless `--seed` (or one of its deprecated aliases) is written on the resume command
- the resolved worker count, unless `--workers` is written on the resume command

Resume does not restore a full internal simplex or full Mayfly population snapshot. It resumes from the saved best point plus the persisted optimizer settings.

### Fitting does not improve much

First run `distance` on the fitted preset. A `gain` far below 0 dB means the time-domain terms
cannot see the reference at all, and no budget will help; a long list of pinned dimensions
means the search wanted to leave the box. Then try:

1. switching from `simple` to `mayfly`
2. starting from a closer preset
3. widening or narrowing the box with `--bounds`, guided by the pinned list
4. increasing `--max-iter`
5. increasing `--time-budget`
6. switching profiles — `placement` if the partials are missing or mispositioned, `polish` once
   they are not — rather than reaching for the legacy `spectral` term: the training review found
   it outvoted by empty bins, which is why `balanced` and its two relatives exist
