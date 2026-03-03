package assets

import "testing"

func TestDefaultPreset(t *testing.T) {
	p, err := DefaultPreset()
	if err != nil {
		t.Fatalf("DefaultPreset() error = %v", err)
	}

	if p.Name == "" {
		t.Fatal("expected embedded preset name")
	}

	if p.Note < 0 || p.Note > 127 {
		t.Fatalf("embedded preset note out of MIDI range: %d", p.Note)
	}
}
