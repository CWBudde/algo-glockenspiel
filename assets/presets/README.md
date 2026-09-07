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

| File                           | Sound                   | Note | Modes | Schema |
| ------------------------------ | ----------------------- | ---- | ----- | ------ |
| `default.json`                 | Default Glockenspiel    | 69   | 4     | 2.0    |
| `morphagene-glockenspiel.json` | Morphagene Glockenspiel | 84   | 1     | 4.0    |
| `recorded-bar.json`            | Recorded Bar            | 69   | 12    | 2.0    |
| `toy-glockenspiel.json`        | Toy Glockenspiel        | 94   | 3     | 3.0    |

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

The keyboard is the orchestral glockenspiel's sounding range, **G5 to C8, MIDI
79 to 108** (`model.KeyboardFirstNote`, `model.KeyboardLastNote`). All three
constraints below are consequences of transposing across it, so all three moved
when it did.

**Its decays clear the ceiling at the bottom key.** Transposing down divides
every decay by the frequency ratio, so a preset authored at note 100 may carry
at most 1487 ms and one at note 108 only 936 ms; anything at or below note 79
may use the full 5000 ms. `model.ValidateAuthoredBarParams` enforces this and
says so.

**Its decays clear the _floor_ at the top key.** The exact mirror, and the one
that bites a preset authored low: transposing up divides decays, so a preset
authored at note 69 may carry no mode whose decay falls under
`model.DecayMsMin × 9.51` — 9.51 being the ratio from note 69 to note 108.
`default.json`'s shortest mode is 0.5605 ms and lands at 0.0589 ms there, which
is why the floor is 0.01 ms rather than the 0.1 ms it was.

**Its modes clear the frequency ceiling at the top key.** Nothing validates
this one: transposing up multiplies mode frequencies, so a preset authored at
note 69 may carry no mode above roughly **21 kHz** — 200 kHz,
`model.FrequencyMaxHz`, divided by the ratio from note 69 to note 108. A preset
that breaks it fails `NewBar` at the top of the keyboard and its note-ons are
discarded without a sound. `TestEveryKeyboardNoteRendersAudio` sweeps every
embedded preset across the whole range for exactly this reason.

Both ceilings scale with the same ratio, so **re-authoring a preset at a
different note changes neither**: `max_mode × 2^((top−note)/12)` and
`min_decay ÷ 2^((top−note)/12)` are invariant under `model.TransposeToNote`.
The only levers are the constants themselves, which is why raising the top key
to C8 moved `FrequencyMaxHz` and `DecayMsMin` rather than the preset files.

## Provenance

`default.json` is the original shipped preset, re-fitted against
`testdata/reference/legacy_synth_a4.wav`; `just refit-default` re-runs it.

`recorded-bar.json` was fitted against the first second of
`testdata/reference/glockenspiel_c5.wav`, a real room recording — see
[the reference notes](../../testdata/reference/README.md). The recorded bar's
fundamental is 1053.6 Hz, and the preset is retuned so that it lands instead on
the default preset's own first mode, 1756.5243 Hz, making the two sounds a
unison rather than a sixth apart. Two modes fitted at 8.0 and 9.8 kHz were
dropped to stay under the then-10.5 kHz ceiling once retuned, which cost about 1 dB
of fit; the shipping commit records that the retuned fit reaches a residual
11.1 dB below the reference RMS.

That 11.1 dB has never been reproduced and should not be relied on. The fitted
preset itself was not kept, so the closest available reconstruction — this
preset with every mode frequency divided by 1.66720, undoing the retune —
reaches 4.8 dB on the first second, measured again on 2026-09-05 and unchanged
since 2026-09-02. The difference is what the hand retune, the two deleted modes
and the unrecorded fit command cost together.
[docs/training.md](../../docs/training.md) has both readings.

`toy-glockenspiel.json` is the first preset here fitted against **more than one
recording**. It is the Phase 9 joint fit over all twenty chromatic notes of
`testdata/reference/packs/hollandm-toy-glockenspiel`, authored at note 94, the
median of that pack, and scored as the mean of the per-note composite scores
rather than against any single bar. It reaches **0.428765** on that mean, where
the unreachable floor -- every note fitted to itself, twenty separate presets --
is **0.329422**. The gap, **+0.099343, is what one preset covering twenty notes
costs**, and it is the deliverable of that phase rather than a defect.

Two things about it differ from the two presets above and are worth knowing
before comparing them.

**It is the first v3 document here**, so it is the first to carry
`output_gain_db` -- +16.64 dB, solved in closed form so the render matches the
reference's level rather than chosen by ear. Both older presets are v2 and have
no such field.

**It renders at -3 dBFS at its own note and hotter below it.** At note 79, the
bottom key, the raw `glockenspiel synth` path peaks at full scale and clips
about 500 samples in three seconds. That is the raw render, not the instrument:
the realtime engine calibrates a per-note trim table against the authored note
(`calibrateNoteTrims`), which is exactly the mechanism that levels the keyboard,
so the browser and the audio callback do not clip. `synth` deliberately renders
without it. Lowering the gain to fix the raw path is not available and should
not be attempted -- `TestBuiltinPresetsRenderNearMinusThreeDBFS` pins the
authored note at -3 dBFS, which is the headroom rule the trims are measured
against.

`morphagene-glockenspiel.json` is the joint fit over all fifteen notes of
`testdata/reference/packs/radiohummingbird-morphagene-glockenspiel`, authored at
note 84, and it is **the first v4 document anywhere in this repository**: it is
the only preset that carries `decay_keytrack`. Its exponent is **0.6492**, and
that number was earned rather than assumed -- twelve paired ablation blocks put
it at 0.6241 +- 0.0435, and this full-budget fit landed inside that range. A v3
reader will accept the file, ignore the key and divide by the full frequency
ratio, which renders correctly at note 84 and drifts further from it with
distance; that is the ladder rule, and it is why the version is 4.0.

**It has one mode, and that is the recording rather than a truncated fit.** The
pack is effectively single-partial -- its second partial is 39 to 67 dB down,
and thirteen of its fifteen notes measure one partial -- so a richer preset
fitted here would be fitting noise. It sounds like a struck bell rather than
like `toy-glockenspiel.json`'s three modes, and the difference between the two
is the difference between the two instruments, not between two fits.

It shares the level behaviour described above for the same reason: -3 dBFS at
its own note, and about 27 samples at full scale at the bottom key through the
raw `synth` path, which the realtime trim table removes.
