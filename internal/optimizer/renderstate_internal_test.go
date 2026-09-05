package optimizer

import (
	"testing"

	"github.com/cwbudde/algo-glockenspiel/internal/preset"
	"github.com/cwbudde/algo-glockenspiel/internal/synth"
)

func renderStateForTest(tb testing.TB) (*renderState, *synth.Synthesizer) {
	tb.Helper()

	source, err := preset.Load("../../assets/presets/default.json")
	if err != nil {
		tb.Fatalf("load the default preset: %v", err)
	}

	working := *source
	working.Parameters = source.Parameters

	engine, err := synth.NewSynthesizer(&working, 44100)
	if err != nil {
		tb.Fatalf("build synthesizer: %v", err)
	}

	fresh, err := synth.NewSynthesizer(source, 44100)
	if err != nil {
		tb.Fatalf("build reference synthesizer: %v", err)
	}

	return &renderState{working: &working, engine: engine}, fresh
}

// TestPooledRenderMatchesRenderNote is the whole safety of pooling the voice:
// reusing it across notes must not carry a scrap of the previous strike into
// the next. Every note of the keyboard, in one order, against a synthesizer
// that allocates a fresh voice each time.
//
// The notes are rendered in sequence on the one state, which is the case that
// would catch a missed field in ResetVoice: a fresh voice cannot fail this
// test, and a reused one can.
func TestPooledRenderMatchesRenderNote(t *testing.T) {
	state, fresh := renderStateForTest(t)

	for note := 79; note <= 108; note++ {
		want := fresh.RenderNote(note, 100, 0.25)
		got := state.render(note, 100, 0.25)

		if len(got) != len(want) {
			t.Fatalf("note %d: pooled render is %d samples, want %d", note, len(got), len(want))
		}

		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("note %d: sample %d is %v, want exactly %v", note, i, got[i], want[i])
			}
		}
	}
}

// TestPooledRenderSurvivesAChangeOfParameters pins that the voice picks up the
// candidate's parameters rather than the ones it was built with. ResetVoice
// re-reads the synthesizer's preset, which is the mechanism Evaluate relies on
// when it writes each candidate into state.working.
func TestPooledRenderSurvivesAChangeOfParameters(t *testing.T) {
	state, _ := renderStateForTest(t)

	before := append([]float32(nil), state.render(93, 100, 0.25)...)

	state.working.Parameters.Modes[0].Frequency *= 1.5

	after := state.render(93, 100, 0.25)

	same := len(before) == len(after)
	for i := range before {
		if !same || before[i] != after[i] {
			same = false

			break
		}
	}

	if same {
		t.Error("moving a mode by a fifth changed nothing: the voice is rendering stale parameters")
	}
}

func BenchmarkRenderNoteAllocating(b *testing.B) {
	_, fresh := renderStateForTest(b)

	b.ReportAllocs()
	b.ResetTimer()

	for range b.N {
		_ = fresh.RenderNote(93, 100, 0.5)
	}
}

func BenchmarkRenderNotePooled(b *testing.B) {
	state, _ := renderStateForTest(b)

	b.ReportAllocs()
	b.ResetTimer()

	for range b.N {
		_ = state.render(93, 100, 0.5)
	}
}
