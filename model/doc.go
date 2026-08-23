// Package model is the synthesis core of the instrument: one struck bar,
// rendered as a filtered and waveshaped excitation driving a bank of decaying
// oscillators.
//
// It is not a physical model. Nothing here solves a wave equation or derives a
// mode from a geometry; the modes are free parameters that a fit chooses
// against a reference recording, which is what makes the instrument
// algorithmic rather than simulated.
//
// # Shape is runtime configuration
//
// The mode count and each mode's harmonic count come from the parameters, not
// from a constant. [BarParams.Modes] is a slice, [ModeParams.Harmonics] is a
// slice, and a Bar sizes its bank from whatever it is handed, up to [MaxModes]
// and [MaxHarmonics]. A four-mode bar is a common case, not the shape of the
// package.
//
// # Using a bar
//
// [NewBar] builds one from a [BarParams] and a sample rate. [Bar.Synthesize]
// renders a struck note; [Bar.ProcessExcitation] drives the same chain from an
// excitation the caller supplies, which is how a decaying voice is carried past
// its strike. [Bar.UpdateParams] repoints an existing bar at new parameters
// without rebuilding it, and without allocating when the new shape fits the old
// one -- which is what lets a voice pool reuse bars across note-ons.
//
// [Bar.StartBankInput] and [Bar.FinishBankOutput] split the same chain either
// side of the oscillator bank, so a caller that renders many voices through one
// packed bank can do the per-voice work around it. They must be called as a
// pair over the same block.
//
// # Ranges
//
// Every parameter has an exported minimum and maximum, and [ValidateBarParams]
// enforces them. The decay dimension is the one asymmetry, and the one place a
// consumer is likely to guess wrong: there is no DecayMsMax. There are three
// constants, because a preset is authored at one note and played at another.
// [DecayMsMin] and [DecayMsSearchMax] bound what a preset file may be written
// with; [DecayMsValidationMax] is the far wider ceiling that BarParams must
// clear at the moment they reach NewBar, after [TransposeToNote] has divided
// every decay by the transposition ratio. Use [ValidateAuthoredBarParams] and
// [AuthoredDecayMsMax] to check a preset in the range it was authored in, which
// depends on its base note.
package model
