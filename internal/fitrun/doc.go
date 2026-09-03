// Package fitrun runs one end-to-end fit and writes a self-describing run
// directory for it.
//
// It is the library form of what `glockenspiel fit` does: load a reference,
// measure it, seed a preset from it, search, optionally polish, and write the
// result. The difference is what it leaves behind. A campaign compares dozens
// of runs against each other months apart, so every run records the inputs it
// was given, the values the backend resolved for itself, the build that ran
// it, and a trace of the search, rather than printing them to a terminal that
// is gone by the time the numbers are read.
package fitrun
