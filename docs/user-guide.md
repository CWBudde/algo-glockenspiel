# User Guide

This guide focuses on the two supported end-user workflows in this repository:

1. rendering notes with `glockenspiel synth`
2. fitting presets with `glockenspiel fit`

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

Basic local-refinement example:

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

Broader search with Mayfly and spectral matching:

```bash
glockenspiel fit \
  --reference testdata/reference/legacy_synth_a4.wav \
  --preset assets/presets/default.json \
  --output out/fitted-a4.json \
  --optimizer mayfly \
  --mayfly-variant desma \
  --mayfly-pop 10 \
  --metric spectral \
  --max-iter 200 \
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

### What The Flags Do

- `--reference`: mono WAV file to match
- `--preset`: starting preset JSON; omit it to use the preset built into the binary
- `--bounds`: JSON file narrowing the search box, see [Narrowing The Search With `--bounds`](#narrowing-the-search-with---bounds)
- `--output`: destination fitted preset JSON
- `--note`: note number used when rendering candidates
- `--velocity`: strike velocity for candidate renders
- `--sample-rate`: must match the reference WAV sample rate
- `--optimizer`: `simple` or `mayfly`
- `--metric`: `rms`, `log`, or `spectral`
- `--max-iter`: iteration cap passed to the optimizer
- `--time-budget`: wall-clock budget as a Go duration, for example `30s` or `10m`; a bare number is still read as seconds
- `--align`: time-align each candidate to the reference before scoring, on by default. Leave it on for recorded references: a few samples of offset invert the phase of a high partial, so the correct parameters would score worse than incorrect ones
- `--normalize-gain`: divide out the scalar gain that best matches the reference level, off by default. Use it when the reference level is unknown; it makes the model's amplitude parameters unidentifiable, so leave it off when the level is meaningful
- `--report-every`: progress print interval
- `--checkpoint-interval`: checkpoint write interval in progress reports; `0` disables checkpointing entirely, including the final checkpoint
- `--work-dir`: stores checkpoints and `fitted_output.wav`, resolved relative to the current directory (default `out/fit`)
- `--resume`: restart from the latest `checkpoint_*.json` in `work-dir`
- `--mayfly-variant`: Mayfly variant selector
- `--mayfly-pop`: Mayfly male/female population size
- `--mayfly-seed`: random seed for Mayfly

### Narrowing The Search With `--bounds`

By default `fit` searches the full parameter box, which spans several decades in
frequency. That is a lot of empty space for the optimizer to cross. When you
already know roughly where the answer lies, pass `--bounds` with a JSON file that
narrows the dimensions you care about:

```json
{
  "input_mix": [0.0, 2.0],
  "filter_freq": [500.0, 8000.0],
  "base_frequency": [430.0, 450.0],
  "amplitude": [-1.0, 1.0],
  "frequency_mult": [0.5, 10.0],
  "decay_ms": [50.0, 400.0],
  "harmonic_gain": [0.0, 1.0]
}
```

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

Benchmark snapshot from `internal/optimizer/perf_test.go` on 2026-03-02, short legacy fit:

- `simple`: `85.47 iter/s`, `220.8 eval/s`, `140.4 convergence-ms`, `3.56 MB/op`
- `mayfly` (`desma`, population 10): `19.98 iter/s`, `939.9 eval/s`, `1001 convergence-ms`, `38.4 MB/op`

Interpretation:

- `simple` is the better default for local refinement from a reasonable preset
- `mayfly` explores many more candidates per second, but it is materially heavier and slower to converge on this short benchmark

Use `rms` when:

- you want the simplest and fastest metric
- you are debugging obvious failures

Use `log` when:

- you want RMS behavior but less sensitivity to large absolute magnitude differences

Use `spectral` when:

- spectral shape matters more than waveform alignment
- the reference and candidate are perceptually close but time-domain metrics look poor
- you want an alternate search target after `rms` or `log` plateau, not a guaranteed better default

Recorded A4 comparison on 2026-03-02 with `simple` from `assets/presets/default.json`:

- `rms` and `log` converged to the same fitted preset and the same rendered output
- `spectral` converged to a different fit with worse time-domain error on that problem
- the `spectral` result also landed on a slightly lower first-mode frequency than the `rms`/`log` fit

The reference for that comparison is `testdata/reference/glockenspiel_a4.wav`, which is in the
repository. The three rendered results are not: they were written under `out/`, which is
gitignored local scratch. Reproduce them by running the same fit three times with
`--metric rms`, `--metric log` and `--metric spectral` into separate `--work-dir`s.

**That comparison predates the float-WAV decode fix and has not been re-taken.** The
reference is 32-bit IEEE float, and until `internal/wavio` learned to read that format it
decoded to a square wave — so all three metrics were compared on a signal the file does not
contain. Read the three bullets above as a record of what was run, not as current guidance
on which metric to pick. See [testdata/reference/README.md](../testdata/reference/README.md).

Practical conclusion:

- start with `simple` + `rms` or `log`
- try `spectral` when the attack brightness or overtone balance sounds wrong even though the basic pitch/tail are close

## Parameter Guide

### The Two Schema Versions

A preset declares its schema in a top-level `version` field, and the loader holds it to that version's rules rather than accepting the union of both.

**v1 (`"1.0"`)** is the original schema: exactly four modes, no per-mode harmonics, and a Chebyshev shaper that always sits on the excitation. A v1 document carrying a v2-only field is rejected with a message naming the field, rather than rendering differently than its version claims.

**v2 (`"2.0"`)** is what new presets are written in. It adds three things:

- a mode array of one to 512 modes, because the oscillator bank sizes itself at runtime. The bounds are real and enforced on load: an empty array is rejected by the v2 rules, and more than `model.MaxModes` (512) by `ValidateBarParams`
- `modes[i].harmonics`, a per-mode gain list that expands that mode into one rotor per integer-multiple partial
- `chebyshev.stage`, either `"excitation"` or `"output"`, making explicit the placement v1 left implicit

Saving preserves the version a preset was loaded with, so fitting from the shipped v1 `default.json` writes a v1 result. Converting is explicit and, today, library-only: `preset.Upgrade` makes the v1 defaults explicit and restamps the document as v2. No CLI flag reaches it yet — writing a v2 preset means hand-editing the `version` field and the fields it unlocks.

### Fields

The top-level `parameters` object holds:

- `input_mix`: amount of dry filtered excitation mixed into the resonant output
- `filter_frequency`: lowpass cutoff for the excitation path, in Hz
- `base_frequency`: reference tuning for the preset note
- `modes`: the resonant partials, exactly four in v1 and one to 512 in v2
- `chebyshev.enabled`: enables harmonic shaping
- `chebyshev.stage`: v2 only, `excitation` (the v1 behaviour, and the default) or `output`
- `chebyshev.harmonic_gains`: gain per generated harmonic

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

### `unsupported metric ""` or `unsupported metric "..."`

Use one of:

- `rms`
- `log`
- `spectral`

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

- optimizer identity (`simple` or `mayfly`) unless you explicitly override it
- metric unless you explicitly override it
- the best encoded parameter vector found so far
- remaining iteration budget relative to the saved checkpoint iteration
- Mayfly variant, population, and seed unless you explicitly override them

Resume does not restore a full internal simplex or full Mayfly population snapshot. It resumes from the saved best point plus the persisted optimizer settings.

### Fitting does not improve much

Try:

1. switching from `simple` to `mayfly`
2. using `spectral` instead of `rms`
3. starting from a closer preset
4. increasing `--max-iter`
5. increasing `--time-budget`
