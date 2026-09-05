// Package pack fits a directory of per-note recordings, one fit per note, and
// collects the results into a table a note-versus-partial regression can be run
// on.
//
// It is deliberately not part of internal/campaign. A campaign compares arms
// against one recording and its results.csv header is a frozen contract with no
// note column; twenty notes are not twenty arms, and adding a column to that
// header would make campaign analyze refuse the archived result sets in
// docs/data. The two share fitrun and the provenance discipline, nothing else.
package pack

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
)

// A4 is the tuning reference the note names are measured against: MIDI 69 at
// 440 Hz, which is what testdata/reference/packs names its files by.
const (
	a4Note = 69
	a4Hz   = 440.0
)

// semitoneNames indexes a pitch class to its name, sharps spelled with "s"
// because Freesound strips "#" from an upload's name and the packs are named
// the way they arrived.
var semitoneNames = [12]string{"c", "cs", "d", "ds", "e", "f", "fs", "g", "gs", "a", "as", "b"}

// fileNamePattern matches the pack naming convention: a pitch class, an
// optional "s" for sharp, and a scientific-pitch octave number. "cs6" is C#6.
var fileNamePattern = regexp.MustCompile(`^([a-g])(s?)(-?\d+)$`)

// NoteFromName parses a pack file's stem into a MIDI note.
//
// The octave is scientific pitch notation, where C4 is middle C at MIDI 60, so
// c6 is 84 and g7 is 103.
func NoteFromName(stem string) (int, error) {
	match := fileNamePattern.FindStringSubmatch(strings.ToLower(stem))
	if match == nil {
		return 0, fmt.Errorf("file name %q is not a pack note name like c6 or cs6", stem)
	}

	class := match[1] + match[2]

	semitone := -1

	for i, name := range semitoneNames {
		if name == class {
			semitone = i

			break
		}
	}

	if semitone < 0 {
		return 0, fmt.Errorf("file name %q names no pitch class", stem)
	}

	octave, err := strconv.Atoi(match[3])
	if err != nil {
		return 0, fmt.Errorf("file name %q has no octave: %w", stem, err)
	}

	note := (octave+1)*12 + semitone
	if note < 0 || note > 127 {
		return 0, fmt.Errorf("file name %q is MIDI %d, outside 0..127", stem, note)
	}

	return note, nil
}

// NameFromNote is NoteFromName's inverse, used to say what a measurement found
// when it disagrees with a file's name.
func NameFromNote(note int) string {
	return fmt.Sprintf("%s%d", semitoneNames[((note%12)+12)%12], note/12-1)
}

// NoteFromFrequency returns the nearest equal-tempered note to a measured
// fundamental, and how far off it the measurement sits in cents.
//
// This is the authority, not the file name. Ten of the hollandm pack's twenty
// files arrived from Freesound sharing a name with their own sharp, because the
// site strips "#" from an upload's title, and measuring is what told the pairs
// apart. A harness that trusted the name would fit half that pack a semitone
// away from the recording it was scoring against.
func NoteFromFrequency(hz float64) (note int, cents float64, err error) {
	if !(hz > 0) || math.IsInf(hz, 0) {
		return 0, 0, fmt.Errorf("fundamental %g Hz is not a pitch", hz)
	}

	exact := a4Note + 12*math.Log2(hz/a4Hz)

	note = int(math.Round(exact))
	if note < 0 || note > 127 {
		return 0, 0, fmt.Errorf("fundamental %.1f Hz is MIDI %d, outside 0..127", hz, note)
	}

	return note, (exact - float64(note)) * 100, nil
}

// ResolveNote reconciles a file's name with its measured fundamental.
//
// The measurement wins, and a disagreement is an error rather than a warning: a
// file whose name and pitch differ is either mislabelled or mis-measured, and
// both are things to look at before spending an hour fitting it. maxCents
// bounds how far the measured pitch may sit from equal temperament before the
// file is refused outright, which catches a recording that is not a struck bar
// at all rather than a bar that is merely out of tune.
func ResolveNote(stem string, fundamentalHz, maxCents float64) (note int, cents float64, err error) {
	measured, cents, err := NoteFromFrequency(fundamentalHz)
	if err != nil {
		return 0, 0, fmt.Errorf("%s: %w", stem, err)
	}

	named, nameErr := NoteFromName(stem)
	if nameErr != nil {
		return 0, 0, nameErr
	}

	if named != measured {
		return 0, 0, fmt.Errorf(
			"%s.wav is named %s (MIDI %d) but sounds %.1f Hz, which is %s (MIDI %d, %+.0f cents); "+
				"rename the file to its sounding pitch or check the recording",
			stem, stem, named, fundamentalHz, NameFromNote(measured), measured, cents)
	}

	if math.Abs(cents) > maxCents {
		return 0, 0, fmt.Errorf(
			"%s.wav sounds %.1f Hz, %+.0f cents from %s, past the %.0f cent limit; "+
				"a bar this far out of tune is fitted at a note its name does not mean",
			stem, fundamentalHz, cents, NameFromNote(measured), maxCents)
	}

	return measured, cents, nil
}
