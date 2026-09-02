// Package analysis measures a reference recording once, by code, the way
// testdata/reference/README.md measured it by hand: which channel to keep,
// where the strike starts and ends, what level it sits at, and which partials
// it holds at what level and half-life.
//
// It sits below internal/optimizer on purpose. The objective of Phase 8.2 will
// read these measurements to place its partial term and its noise floor, and
// the codec of Phase 8.3 will read them to size its box, so this package must
// not import the optimizer; the optimizer imports it. The onset detector the
// optimizer aligns candidates with therefore lives here and is shared, so the
// sample the analysis calls the strike and the sample the alignment calls the
// strike are one definition.
//
// Everything a caller decides -- downmix, window, normalisation -- is recorded
// in the result next to what it produced. A fit that reads analysis.json can
// then say what reference it was actually fitted against, which the hand-cut
// "first second" behind assets/presets/recorded-bar.json never could.
package analysis
