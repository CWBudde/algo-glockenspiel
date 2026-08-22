package synth

import (
	"testing"

	"github.com/cwbudde/glockenspiel/internal/preset"
	"github.com/cwbudde/glockenspiel/model"
)

// quickDecayPreset returns a preset that actually falls silent, so auto-stop is
// what ends the note rather than its nominal duration.
//
// Two edits, and both are needed. Short decays make the note end quickly. The
// Chebyshev shaper has to go because it emits a constant for a silent input --
// exactly -0.3 for the shipped gains -- and this preset places it ahead of the
// oscillator bank, so that constant is a DC excitation the bank never resolves.
// With it enabled the bar holds a steady RMS forever and auto-stop never fires
// at any decay setting, which is why the block-size dependence this file tests
// went unnoticed for so long: the one rule that would have exposed it was
// unreachable.
//
// TestRenderNoteAutoStop reaches auto-stop a different way, by passing a
// threshold of +20 dBFS so that everything counts as quiet. That works for
// asking whether auto-stop fires at all, but it makes the stop point
// independent of the signal, and the stop point is what this file is about.
func quickDecayPreset(t *testing.T) *preset.Preset {
	t.Helper()

	fast := loadTestPreset(t).Clone()
	fast.Parameters.Chebyshev.Enabled = false

	for i := range fast.Parameters.Modes {
		fast.Parameters.Modes[i].DecayMs = model.DecayMsMin
	}

	return fast
}

// renderInChunks streams a voice through fixed-size chunks and reports how many
// samples it produced before retiring.
func renderInChunks(t *testing.T, s *Synthesizer, chunk int) ([]float32, int) {
	t.Helper()

	voice, err := s.NewVoice(69, 100, 2.0, RenderOptions{AutoStop: true, DecayDBFS: -72})
	if err != nil {
		t.Fatalf("NewVoice at chunk %d: %v", chunk, err)
	}

	buf := make([]float32, chunk)
	out := make([]float32, 0, 4096)

	for voice.Active() {
		n := voice.RenderInto(buf)
		if n == 0 {
			break
		}

		out = append(out, buf[:n]...)
	}

	return out, len(out)
}

// TestAutoStopDoesNotDependOnTheCallersChunkSize pins the rule this change
// rewrote: where a note ends is a property of the note, not of the buffer size
// of whoever is rendering it.
//
// The old rule counted quiet *blocks* against autoStopBlockCount and measured
// RMS over len(block) -- both the caller's chunk. A host asking for 37 samples
// at a time therefore needed 8 x 37 = 296 samples of continuous quiet where one
// asking for 128 needed 1024, and the two averaged over different-length
// windows on top of that, so they crossed the threshold at different moments
// and could only stop on their own multiples anyway.
//
// The chunk sizes below are chosen to be awkward on purpose: 1 forces a window
// to fill one sample at a time, 37 is the size that first exposed this in
// TestVoiceRenderMatchesRenderNote, 129 straddles a window boundary on every
// call, and 512 is wider than a window so several complete per call.
func TestAutoStopDoesNotDependOnTheCallersChunkSize(t *testing.T) {
	s, err := NewSynthesizer(quickDecayPreset(t), 44100)
	if err != nil {
		t.Fatalf("NewSynthesizer: %v", err)
	}

	reference, wantLength := renderInChunks(t, s, defaultBlockSize)

	if wantLength == 0 {
		t.Fatal("the reference render produced nothing")
	}

	if wantLength >= int(2.0*44100) {
		t.Fatalf("the note ran its full duration (%d samples), so auto-stop never fired "+
			"and this test would pass without measuring anything", wantLength)
	}

	for _, chunk := range []int{1, 37, 129, 512} {
		samples, length := renderInChunks(t, s, chunk)

		if length != wantLength {
			t.Errorf("chunk %d retired after %d samples, chunk %d after %d",
				chunk, length, defaultBlockSize, wantLength)

			continue
		}

		for i := range samples {
			if samples[i] != reference[i] {
				t.Errorf("chunk %d: sample %d is %v, chunk %d says %v",
					chunk, i, samples[i], defaultBlockSize, reference[i])

				break
			}
		}
	}
}
