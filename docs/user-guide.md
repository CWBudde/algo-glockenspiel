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

- `--preset`: preset JSON to load
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
  --time-budget 30 \
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
  --time-budget 60 \
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
- `--preset`: starting preset JSON
- `--output`: destination fitted preset JSON
- `--note`: note number used when rendering candidates
- `--velocity`: strike velocity for candidate renders
- `--sample-rate`: must match the reference WAV sample rate
- `--optimizer`: `simple` or `mayfly`
- `--metric`: `rms`, `log`, or `spectral`
- `--max-iter`: iteration cap passed to the optimizer
- `--time-budget`: wall-clock budget in seconds
- `--report-every`: progress print interval
- `--checkpoint-interval`: checkpoint write interval in progress iterations
- `--work-dir`: stores checkpoints and `fitted_output.wav`
- `--resume`: restart from the latest `checkpoint_*.json` in `work-dir`
- `--mayfly-variant`: Mayfly variant selector
- `--mayfly-pop`: Mayfly male/female population size
- `--mayfly-seed`: random seed for Mayfly

### Choosing Optimizer And Metric

Use `simple` when:

- your starting preset is already close
- you want faster, more predictable local refinement
- you are iterating frequently

Use `mayfly` when:

- the starting preset is weak
- the search surface is rough
- `simple` gets stuck too early

Use `rms` when:

- you want the simplest and fastest metric
- you are debugging obvious failures

Use `log` when:

- you want RMS behavior but less sensitivity to large absolute magnitude differences

Use `spectral` when:

- spectral shape matters more than waveform alignment
- the reference and candidate are perceptually close but time-domain metrics look poor

## Parameter Guide

Presets contain a top-level `parameters` object with these main fields:

- `input_mix`: amount of dry filtered excitation added to the resonant output
- `filter_frequency`: lowpass cutoff for the excitation path
- `base_frequency`: reference tuning for the preset note
- `modes`: four resonant partials with amplitude, frequency, and decay
- `chebyshev.enabled`: enables harmonic excitation shaping
- `chebyshev.harmonic_gains`: gain per generated harmonic

### Reading Mode Parameters

Each mode has:

- `amplitude`: linear mode gain
- `frequency`: modal frequency in Hz
- `decay_ms`: decay time in milliseconds

In practice:

- the first mode usually dominates the perceived pitch
- higher modes shape brightness and attack character
- very short decay values mostly affect the transient

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

### Fitting does not improve much

Try:

1. switching from `simple` to `mayfly`
2. using `spectral` instead of `rms`
3. starting from a closer preset
4. increasing `--max-iter`
5. increasing `--time-budget`
