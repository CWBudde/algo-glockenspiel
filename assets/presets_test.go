package assets

import (
	"slices"
	"strings"
	"testing"
)

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

// TestIDsAreSortedAndIncludeTheDefault guards the property a menu depends on.
// fs.ReadDir hands back the embed tool's order, so without the sort the option
// list would be free to reorder itself between builds.
func TestIDsAreSortedAndIncludeTheDefault(t *testing.T) {
	names, err := IDs()
	if err != nil {
		t.Fatalf("IDs() error = %v", err)
	}

	if len(names) < 2 {
		t.Fatalf("IDs() = %v, want at least two embedded presets", names)
	}

	if !slices.IsSorted(names) {
		t.Fatalf("IDs() = %v, want sorted", names)
	}

	if !slices.Contains(names, DefaultID) {
		t.Fatalf("IDs() = %v, want it to contain %q", names, DefaultID)
	}
}

// TestIDsReturnsACopy keeps a caller that sorts or truncates the listing from
// reordering it for everyone else, since the slice behind it is cached.
func TestIDsReturnsACopy(t *testing.T) {
	first, err := IDs()
	if err != nil {
		t.Fatalf("IDs() error = %v", err)
	}

	first[0] = "clobbered"

	second, err := IDs()
	if err != nil {
		t.Fatalf("IDs() error = %v", err)
	}

	if second[0] == "clobbered" {
		t.Fatal("IDs() handed out its cached slice")
	}
}

func TestPresetResolvesEveryID(t *testing.T) {
	names, err := IDs()
	if err != nil {
		t.Fatalf("IDs() error = %v", err)
	}

	for _, id := range names {
		p, err := Preset(id)
		if err != nil {
			t.Fatalf("Preset(%q) error = %v", id, err)
		}

		if p.Name == "" {
			t.Fatalf("Preset(%q): empty name", id)
		}
	}
}

// TestPresetEmptyIDIsTheDefault is what lets every front end thread an optional
// choice through without its own empty check.
func TestPresetEmptyIDIsTheDefault(t *testing.T) {
	fallback, err := Preset("")
	if err != nil {
		t.Fatalf("Preset(\"\") error = %v", err)
	}

	byName, err := Preset(DefaultID)
	if err != nil {
		t.Fatalf("Preset(%q) error = %v", DefaultID, err)
	}

	if fallback.Name != byName.Name {
		t.Fatalf("Preset(\"\") = %q, want %q", fallback.Name, byName.Name)
	}
}

func TestPresetRejectsUnknownIDAndNamesWhatItHas(t *testing.T) {
	_, err := Preset("no-such-preset")
	if err == nil {
		t.Fatal("Preset(\"no-such-preset\") = nil error, want a failure")
	}

	if !strings.Contains(err.Error(), DefaultID) {
		t.Fatalf("error = %q, want it to name the known presets", err)
	}
}

// TestPresetRejectsAPath is the check path.Base alone would not make: it would
// turn "../../etc/passwd" into "passwd" and answer for whatever that named,
// rather than reporting that the caller asked for something that is not a
// preset.
func TestPresetRejectsAPath(t *testing.T) {
	for _, id := range []string{"../presets/default", "sub/default", "./default"} {
		if _, err := Preset(id); err == nil {
			t.Fatalf("Preset(%q) = nil error, want a failure", id)
		}
	}
}

func TestListCarriesNameAndNoteForEveryID(t *testing.T) {
	listed, err := List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}

	names, err := IDs()
	if err != nil {
		t.Fatalf("IDs() error = %v", err)
	}

	if len(listed) != len(names) {
		t.Fatalf("List() has %d entries, IDs() has %d", len(listed), len(names))
	}

	for i, entry := range listed {
		if entry.ID != names[i] {
			t.Fatalf("List()[%d].ID = %q, want %q", i, entry.ID, names[i])
		}

		if entry.Name == "" {
			t.Fatalf("List()[%d] (%q): empty name", i, entry.ID)
		}

		if entry.Note < 0 || entry.Note > 127 {
			t.Fatalf("List()[%d] (%q): note %d out of MIDI range", i, entry.ID, entry.Note)
		}
	}
}
