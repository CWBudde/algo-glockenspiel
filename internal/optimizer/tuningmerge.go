package optimizer

import "reflect"

// Overlay returns a tuning document in which every key the override sets wins
// and every key it leaves out keeps the receiver's value.
//
// It exists so the scalar settings a front end exposes and the tuning document
// it accepts do not become two ways of configuring the same run. A front end
// builds a document from its own flags or form fields, overlays the caller's
// file on top, and hands the result to one applier. There is then exactly one
// place a knob is written, and precedence is one sentence: the document wins.
//
// Either side may be nil. The receiver is never modified.
func (t *MayflyTuning) Overlay(override *MayflyTuning) *MayflyTuning {
	if t == nil {
		return override
	}

	if override == nil {
		return t
	}

	merged := *t
	overlayStruct(reflect.ValueOf(&merged).Elem(), reflect.ValueOf(override).Elem())

	return &merged
}

// overlayStruct copies every set field of src onto dst.
//
// It is reflective rather than a written-out list of forty assignments for the
// same reason the knob table is: a field added to MayflyTuning and forgotten
// here would be silently unmergeable, and nothing would fail.
func overlayStruct(dst, src reflect.Value) {
	for i := range src.NumField() {
		from := src.Field(i)
		if from.Kind() != reflect.Pointer || from.IsNil() {
			continue
		}

		to := dst.Field(i)

		// A nested block is merged key by key rather than replaced wholesale,
		// so a document that sets one convergence key does not silently discard
		// the others a front end had already filled in.
		if from.Type().Elem().Kind() == reflect.Struct && !to.IsNil() {
			clone := reflect.New(to.Type().Elem())
			clone.Elem().Set(to.Elem())
			overlayStruct(clone.Elem(), from.Elem())
			to.Set(clone)

			continue
		}

		to.Set(from)
	}
}
