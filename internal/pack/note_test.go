package pack_test

import (
	"math"
	"strings"
	"testing"

	"github.com/cwbudde/algo-glockenspiel/internal/pack"
)

func TestNoteFromNameFollowsScientificPitch(t *testing.T) {
	for _, want := range []struct {
		stem string
		want int
	}{
		{"c4", 60},  // middle C
		{"a4", 69},  // the tuning reference
		{"c6", 84},  // the bottom of the hollandm pack
		{"cs6", 85}, // and its first sharp
		{"g7", 103}, // the top of it
		{"c8", 108}, // the top of the keyboard
		{"g5", 79},  // the bottom of the keyboard
		{"C6", 84},  // case does not matter
	} {
		got, err := pack.NoteFromName(want.stem)
		if err != nil {
			t.Errorf("NoteFromName(%q): %v", want.stem, err)

			continue
		}

		if got != want.want {
			t.Errorf("NoteFromName(%q) = %d, want %d", want.stem, got, want.want)
		}

		if back := pack.NameFromNote(want.want); !strings.EqualFold(back, want.stem) {
			t.Errorf("NameFromNote(%d) = %q, want %q", want.want, back, want.stem)
		}
	}
}

func TestNoteFromNameRefusesWhatIsNotANoteName(t *testing.T) {
	for _, stem := range []string{"", "h6", "c", "6", "cb6", "c#6", "reference", "c6-hard"} {
		if note, err := pack.NoteFromName(stem); err == nil {
			t.Errorf("NoteFromName(%q) = %d, want an error", stem, note)
		}
	}
}

// TestNoteFromFrequencyMatchesTheMeasuredPack uses the fundamentals the pack
// README records, which were measured with analysis.Measure -- the same code
// glockenspiel analyze runs. If this drifts, the naming of the pack and the
// arithmetic here have stopped agreeing.
func TestNoteFromFrequencyMatchesTheMeasuredPack(t *testing.T) {
	for _, want := range []struct {
		hz    float64
		note  int
		cents float64
	}{
		{1046.2, 84, 0},  // c6
		{1109.9, 85, 2},  // cs6, the file that shares a name with c6 on Freesound
		{1763.4, 93, 3},  // a6
		{3137.6, 103, 1}, // g7
		{1810.2, 93, 49}, // mooncubedesign a6: nearly halfway to A#6, but still A6
		{440.0, 69, 0},   // the reference itself
	} {
		note, cents, err := pack.NoteFromFrequency(want.hz)
		if err != nil {
			t.Errorf("NoteFromFrequency(%g): %v", want.hz, err)

			continue
		}

		if note != want.note {
			t.Errorf("NoteFromFrequency(%g) = MIDI %d, want %d", want.hz, note, want.note)
		}

		if math.Abs(cents-want.cents) > 1 {
			t.Errorf("NoteFromFrequency(%g) = %+.1f cents, want about %+.0f", want.hz, cents, want.cents)
		}
	}
}

// TestResolveNoteRefusesAMislabelledFile is the trap the hollandm pack sets.
// Freesound strips "#" from an upload's name, so ten of its files arrived
// sharing a name with their own sharp; a harness that trusted the name would
// fit half the pack a semitone from the recording it scores against.
func TestResolveNoteRefusesAMislabelledFile(t *testing.T) {
	// c6 named correctly.
	if note, _, err := pack.ResolveNote("c6", 1046.2, 60); err != nil || note != 84 {
		t.Fatalf("a correctly named file was refused: note %d, err %v", note, err)
	}

	// The same recording still carrying the name Freesound gave the sharp.
	_, _, err := pack.ResolveNote("c6", 1109.9, 60)
	if err == nil {
		t.Fatal("a file named c6 that sounds C#6 was accepted")
	}

	if !strings.Contains(err.Error(), "cs6") || !strings.Contains(err.Error(), "1109.9") {
		t.Errorf("the error does not name what was measured: %v", err)
	}
}

// TestResolveNoteRefusesABarTooFarOutOfTune is why maxCents exists: the
// mooncubedesign pack is up to 49 cents sharp, far enough that the note name
// stops meaning the pitch, and fitting it at the named note would charge the
// partial_cents term for the label rather than for the fit.
func TestResolveNoteRefusesABarTooFarOutOfTune(t *testing.T) {
	if _, _, err := pack.ResolveNote("a6", 1810.2, 25); err == nil {
		t.Fatal("a bar 49 cents sharp was accepted under a 25 cent limit")
	}

	// The same bar passes when the limit admits it, and reports the detune
	// rather than hiding it.
	note, cents, err := pack.ResolveNote("a6", 1810.2, 60)
	if err != nil {
		t.Fatalf("a 49 cent bar was refused under a 60 cent limit: %v", err)
	}

	if note != 93 || math.Abs(cents-49) > 1 {
		t.Errorf("resolved to MIDI %d at %+.0f cents, want 93 at about +49", note, cents)
	}
}
