package fitschema

import "github.com/cwbudde/algo-glockenspiel/model"

// KeyboardFirstNote and KeyboardLastNote are the MIDI notes at the two ends of
// the playable keyboard, re-exported from model so the browser can be generated
// from the same constants the engine validates against.
//
// They are here rather than typed a second time in TypeScript because the two
// copies had already drifted apart once: the web layout hard-coded 36 and 96
// while model moved to the glockenspiel's G5..C8, which would have had the
// browser draw keys whose note-ons the engine refuses to build and drops
// without a sound. A generated constant cannot drift.
func KeyboardFirstNote() int { return model.KeyboardFirstNote }

// KeyboardLastNote is the top of the playable keyboard. See KeyboardFirstNote.
func KeyboardLastNote() int { return model.KeyboardLastNote }
