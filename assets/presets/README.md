# Built-in presets

Every `*.json` in this directory is embedded in the binary by
`assets/presets.go` and becomes a selectable sound. Its **filename stem is its
id** — what `--preset`, `assets.Preset` and the browser's `setPreset` address it
by — and the document's own `name` field is what a picker shows. There is no
registry to add a preset to: adding a file is adding a sound.

Regenerate the browser's copy of the list after any change here:

```bash
just gen-presets      # writes web/src/api/presets.generated.ts
just check-presets    # what CI runs
```

| File                | Sound                | Note | Modes |
| ------------------- | -------------------- | ---- | ----- |
| `default.json`      | Default Glockenspiel | 69   | 4     |
| `recorded-bar.json` | Recorded Bar         | 69   | 12    |

## What a preset here has to satisfy

Beyond `preset.Validate`, three constraints are not obvious from the schema and
are each pinned by a test.

**It renders near −3 dBFS at its own note**, at 44.1 kHz and velocity 100
(`TestBuiltinPresetsRenderNearMinusThreeDBFS`). That is a headroom rule, not a
loudness rule: the strike is a single-sample impulse, so a higher sample rate
feeds the modes a wider-band excitation and the peak rises. Two presets matched
at the peak are not matched in loudness — a bar with a taller strike transient
reaches the same peak at a lower RMS — and that difference is a property of the
bar rather than something to normalise away.

**Its decays clear the ceiling at the bottom key.** Transposing down divides
every decay by the frequency ratio, so a preset authored at note 69 may carry at
most 743 ms; `model.ValidateAuthoredBarParams` enforces this and says so.

**Its modes clear the frequency ceiling at the top key.** This is the mirror
image and nothing validates it: transposing up multiplies mode frequencies, so a
preset authored at note 69 may carry no mode above roughly **10.5 kHz** —
50 kHz, `model.FrequencyMaxHz`, divided by the ratio from note 69 to note 96. A
preset that breaks it fails `NewBar` at the top of the keyboard and its note-ons
are discarded without a sound. `TestEveryKeyboardNoteRendersAudio` sweeps every
embedded preset across the whole range for exactly this reason.

## Provenance

`default.json` is the original shipped preset, re-fitted against
`testdata/reference/legacy_synth_a4.wav`; `just refit-default` re-runs it.

`recorded-bar.json` was fitted against the first second of
`testdata/reference/glockenspiel_c5.wav`, a real room recording — see
[the reference notes](../../testdata/reference/README.md). The recorded bar's
fundamental is 1053.6 Hz, and the preset is retuned so that it lands instead on
the default preset's own first mode, 1756.5243 Hz, making the two sounds a
unison rather than a sixth apart. Two modes fitted at 8.0 and 9.8 kHz were
dropped to stay under the 10.5 kHz ceiling once retuned, which cost about 1 dB
of fit; the retuned fit reaches a residual 11.1 dB below the reference RMS.
