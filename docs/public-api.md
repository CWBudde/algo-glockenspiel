# The public API

This repository is two modules' worth of code in one. Everything under
`internal/` is private by Go's own rule, and one package — `model/` — is public,
because a second module builds against it:
[algo-glockenspiel-vst3](https://github.com/CWBudde/algo-glockenspiel-vst3)
depends on `github.com/cwbudde/algo-glockenspiel` the ordinary way, with no
`replace` directive.

That makes `model/` the only part of this repository where a change can break
something outside it, and the only part where "it compiles" is not the same
question as "a consumer can use it".

## What the split-out plugin actually uses

Read out of that repository rather than assumed, because the assumption in the
plan was wrong on two counts:

- Types: `Bar`, `BarParams`, `ModeParams`, `ChebyshevParams`
- Functions: `NewBar`, `TransposeToNote`, `ValidateBarParams`
- Methods: `Bar.Synthesize`, `Bar.ProcessExcitation`, `Bar.Reset`,
  `Bar.UpdateParams`
- Every range constant, including both decay ceilings

It uses none of `Clone`, `CopyInto`, `MaxModes`, `MaxHarmonics` or
`BankOscillators`. That is not a reason to delete them, but it is the list to
check a proposed removal against.

## Exported does not mean reachable

An exported method may only mention types a consumer can _name_. Go's internal
rule blocks importing `internal/...` from outside the module, so an exported
signature that names a type from there compiles perfectly here and is unusable
there.

`Bar.BankOscillators` shipped this way for two phases. It returned
`[]oscbank.Oscillator`, so the method was callable but its result could not be
assigned to a variable, passed to a function, or ranged into a typed loop
variable. Nothing inside this module noticed, because same-module code is
allowed to import `internal/`.

The fix is to **re-export the type through an alias**, not to define a new one:

```go
type Oscillator = oscbank.Oscillator
```

An alias makes the type nameable under the public path while keeping its
identity, so `internal/synth` still hands the slice straight to
`oscbank.Bank.SetVoice` with no conversion. A defined type would need a
conversion at every boundary, and `BankOscillators` returns the bar's own
storage precisely to avoid a copy on the note-on path.

`model/api_surface_test.go` enforces this. It parses the package and fails on
any exported signature, exported struct field, or result type qualified by an
`internal/...` import, allowing exported type aliases as the deliberate
re-export. It has to be a test _about the declarations_ rather than a test that
uses the API: a usage test written inside this module would pass either way.

## Two ceilings on decay, and no `DecayMsMax`

The single trap in the surface, and the one a consumer is most likely to guess
wrong, because the obvious name does not exist.

A preset is authored at one note and played at another, and `TransposeToNote`
divides every decay by the transposition ratio, so the decays that reach
`NewBar` are not the decays in the file. There are therefore three constants:

| Constant               | Bounds                                                                     |
| ---------------------- | -------------------------------------------------------------------------- |
| `DecayMsMin`           | the shortest decay, everywhere                                             |
| `DecayMsSearchMax`     | what a preset file may be **written** with, and what a fit searches        |
| `DecayMsValidationMax` | what a `BarParams` may carry when it reaches `NewBar`, after transposition |

`ValidateBarParams` enforces the wide one. To check a preset in the range it was
authored in — which depends on its base note — use `ValidateAuthoredBarParams`
and `AuthoredDecayMsMax`. Collapsing the two ceilings back into one is what
silenced MIDI 36–96's bottom 17 keys; see Phase 5.1 in `PLAN.md`.

## Shape is runtime configuration

The mode count and each mode's harmonic count come from the parameters, never
from a constant. `BarParams.Modes` is a slice, `ModeParams.Harmonics` is a
slice, and a `Bar` sizes its bank from whatever it is handed, up to `MaxModes`
and `MaxHarmonics`. A four-mode bar is a common case, not the shape of the
package — the only fixed `4` left is the unexported gain count of the AVX2
Chebyshev fast path, which has nothing to do with modes.

Any new exported item has to make sense for a variable count. A per-mode
constant, a fixed-length array in a signature, or a `[4]float64` field would all
fail that test.

## Value semantics

`Clone` and `CopyInto` are not the same tool, and the distinction is deliberate:

- **`Clone`** is the public idiom. It returns a deep copy from an empty
  destination, so every non-empty slice it copies is freshly allocated. Use it
  anywhere that is not the audio path.
- **`BarParams.CopyInto`** is the audio-path mechanism. It reuses the
  destination's slices wherever their capacity already suffices, which is what
  lets a pooled voice absorb a new parameter set on note-on without allocating.
  Its per-field counterparts are unexported: a caller reaching for one of those
  wants `CopyInto` on the whole struct.

Both are deep, and both preserve nil-ness rather than normalising it to an empty
slice, because `BarParams` round-trips through JSON where `null` and `[]` do not
encode alike.

## Versioning

The module is `github.com/cwbudde/algo-glockenspiel`, matching the repository,
and `v0.1.0` is tagged at the module-rename merge. Renaming the module rather
than the repository was the deliberate choice — a repository rename would have
left a redirect, a module rename does not — so a breaking change to `model/`
breaks the plugin repository outright rather than degrading. Both known
consumers are ours, which is what makes that affordable; it is not a licence to
change the surface casually.
