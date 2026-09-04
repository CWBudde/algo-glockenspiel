// Package fitschema is the one table a fit request's scalar fields, name
// lists and model bounds are described by.
//
// Before this package existed, that table was written three times: the
// constant block and defaultFitRequest in internal/server/fit.go, the
// scattered literals in internal/browserfit's validateRequest, and
// FIT_LIMITS/DEFAULT_FIT_REQUEST/the name-list constants hand-transcribed in
// web/src/api/types.ts. A range that moved in one had to be found and moved
// in the other two, and nothing enforced that it was: internal/browserfit's
// copy of the mayfly and CMA-ES ceilings had already drifted from the
// server's before this package was written, silently accepting requests the
// server would have refused. Every reader of this package -- the server, the
// browser fit and cmd/gen-fit-schema, which writes the TypeScript mirror --
// now walks the same Go value, so that kind of drift cannot happen again.
//
// The table is a plain Go value, in the style of optimizer.MayflyTuningFields:
// reflection-free, so a field can be added or renamed with one literal edit
// rather than a change to whatever walks a struct's tags.
package fitschema

import (
	"fmt"
	"time"

	"github.com/cwbudde/algo-glockenspiel/internal/optimizer"
)

// JSMaxSafeInteger is JavaScript's Number.MAX_SAFE_INTEGER, the largest
// integer a JS number represents exactly. cmaesSeed travels as a JSON number
// rather than the decimal string mayflySeed uses, so its range is held to
// this instead of an int64's own range: a seed outside it would reach the
// server as a different one than the caller typed.
const JSMaxSafeInteger = 1<<53 - 1

// Kind is the wire shape one field's value takes.
type Kind string

const (
	KindInt      Kind = "int"      // a whole JSON number
	KindFloat    Kind = "float"    // a JSON number
	KindSafeInt  Kind = "safeint"  // a JSON number, held inside JSMaxSafeInteger
	KindSeed     Kind = "seed"     // a decimal string, for an int64 a JS number cannot hold
	KindBool     Kind = "bool"     // a JSON boolean
	KindEnum     Kind = "enum"     // a JSON string from a fixed vocabulary
	KindDuration Kind = "duration" // a Go duration string, or a bare number of seconds
	KindBytes    Kind = "bytes"    // a byte count; FIT_LIMITS carries it as a bare ceiling
)

// Field describes one scalar field of POST api/fit/start: its wire name, the
// default defaultFitRequest gives it, and the range parseFitRequest and
// browserfit's validateRequest each hold it to.
type Field struct {
	// Key is the field's name in the multipart form and in FitRequestFields.
	Key string

	// LimitKey is Key's spelling in FIT_LIMITS, when it differs from Key.
	// Only "timeBudget" needs this: the request field is a duration string,
	// while FIT_LIMITS holds the range it is checked against in seconds.
	LimitKey string

	Kind Kind
	Unit string

	// InRequest says whether the field appears in FitRequestFields and
	// DEFAULT_FIT_REQUEST at all. maxReferenceBytes and renderSeconds are
	// server limits with no field of their own on the start request.
	InRequest bool

	// HasDefault and Default describe defaultFitRequest's value. Three
	// fields -- mayflyTargetCost, mayflyNc and mayflyNcRatio -- have no
	// default: each one's own zero is a real setting, so an absent field
	// must leave the knob unwritten rather than silently becoming zero, and
	// DEFAULT_FIT_REQUEST omits the key.
	HasDefault bool
	Default    any

	// HasLimit says whether the field is held to a numeric range at all. An
	// enum, a boolean or a seed owns its own vocabulary, or none, and is not
	// in FIT_LIMITS.
	HasLimit     bool
	Min          float64
	Max          float64
	MinExclusive bool
	MaxExclusive bool
}

// Fields is the ordered table of every scalar field a start request accepts,
// mirrored field for field into FIT_LIMITS and DEFAULT_FIT_REQUEST.
//
//nolint:funlen // one literal table entry per field; splitting it hides the table.
func Fields() []Field {
	return []Field{
		{
			Key: "maxReferenceBytes", Kind: KindBytes, Unit: "bytes",
			HasLimit: true, Max: DefaultMaxReferenceBytes,
		},
		{
			Key: "note", Kind: KindInt, InRequest: true,
			HasDefault: true, Default: 69,
			HasLimit: true, Min: 0, Max: 127,
		},
		{
			Key: "velocity", Kind: KindInt, InRequest: true,
			HasDefault: true, Default: 100,
			HasLimit: true, Min: 0, Max: 127,
		},
		{
			Key: "optimizer", Kind: KindEnum, InRequest: true,
			HasDefault: true, Default: "simple",
		},
		{
			Key: "metric", Kind: KindEnum, InRequest: true,
			HasDefault: true, Default: string(optimizer.MetricBalanced),
		},
		{
			Key: "maxIterations", Kind: KindInt, InRequest: true,
			HasDefault: true, Default: 100,
			HasLimit: true, Min: 1, Max: MaxFitIterations,
		},
		{
			Key: "timeBudget", LimitKey: "timeBudgetSeconds", Kind: KindDuration,
			InRequest:  true,
			HasDefault: true, Default: DefaultTimeBudget,
			Unit:         "s",
			HasLimit:     true,
			Min:          0,
			Max:          MaxFitTimeBudget.Seconds(),
			MinExclusive: true,
		},
		{
			Key: "reportEvery", Kind: KindInt, InRequest: true,
			HasDefault: true, Default: 10,
			HasLimit: true, Min: 0, Max: MaxFitIterations,
		},
		{
			Key: "align", Kind: KindBool, InRequest: true,
			HasDefault: true, Default: true,
		},
		{
			Key: "normalizeGain", Kind: KindBool, InRequest: true,
			HasDefault: true, Default: false,
		},
		{
			Key: "mayflyVariant", Kind: KindEnum, InRequest: true,
			HasDefault: true, Default: "desma",
		},
		{
			Key: "mayflyPopulation", Kind: KindInt, InRequest: true,
			HasDefault: true, Default: 10,
			HasLimit: true, Min: 2, Max: MaxMayflyPopulation,
		},
		{
			Key: "mayflySeed", Kind: KindSeed, InRequest: true,
			HasDefault: true, Default: int64(1),
		},
		{
			Key: "mayflyPreset", Kind: KindEnum, InRequest: true,
			HasDefault: true, Default: "",
		},
		{
			Key: "mayflyEpochs", Kind: KindInt, InRequest: true,
			HasDefault: true, Default: 1,
			HasLimit: true, Min: MayflyEpochsMin, Max: MayflyRoundsMax,
		},
		{
			Key: "mayflyRestarts", Kind: KindInt, InRequest: true,
			HasDefault: true, Default: 0,
			HasLimit: true, Min: 0, Max: MayflyRoundsMax,
		},
		{
			Key: "mayflyStagnation", Kind: KindInt, InRequest: true,
			HasDefault: true, Default: 0,
			HasLimit: true, Min: 0, Max: MaxFitIterations,
		},
		{
			Key: "mayflySelection", Kind: KindEnum, InRequest: true,
			HasDefault: true, Default: "",
		},
		{
			Key: "mayflyTargetCost", Kind: KindFloat, InRequest: true,
			HasLimit: true, Min: -MaxFitTargetCost, Max: MaxFitTargetCost,
		},
		{
			Key: "mayflyNc", Kind: KindInt, InRequest: true,
			HasLimit: true, Min: MayflyNCMin, Max: MaxMayflyPopulation,
		},
		{
			Key: "mayflyNcRatio", Kind: KindFloat, InRequest: true,
			HasLimit: true, Min: 0, Max: MaxMayflyPopulation,
		},
		{
			Key: "cmaesCovariance", Kind: KindEnum, InRequest: true,
			HasDefault: true, Default: "separable",
		},
		{
			Key: "cmaesLambda", Kind: KindInt, InRequest: true,
			HasDefault: true, Default: 0,
			HasLimit: true, Min: 0, Max: MaxCMAESLambda,
		},
		{
			Key: "cmaesSigma", Kind: KindFloat, InRequest: true,
			HasDefault: true, Default: 0.3,
			HasLimit: true, Min: 0, Max: 1,
		},
		{
			Key: "cmaesSeed", Kind: KindSafeInt, InRequest: true,
			HasDefault: true, Default: 0,
			HasLimit: true, Min: -JSMaxSafeInteger, Max: JSMaxSafeInteger,
		},
		{
			Key: "cmaesRestarts", Kind: KindInt, InRequest: true,
			HasDefault: true, Default: 0,
			HasLimit: true, Min: 0, Max: MaxCMAESRestarts,
		},
		{
			Key: "renderSeconds", Kind: KindFloat, Unit: "s",
			HasLimit: true, Min: 0, Max: MaxRenderSeconds, MinExclusive: true,
		},
	}
}

// byKey indexes Fields by its request key, built once rather than on every
// call, since parseFitRequest and validateRequest look up a handful of
// fields apiece per request.
var byKey = func() map[string]Field {
	fields := Fields()
	index := make(map[string]Field, len(fields))

	for _, field := range fields {
		index[field.Key] = field
	}

	return index
}()

// mustField looks a field up by its request key, panicking on a name that is
// not in the table. Every call site names a field literally, so a typo is a
// programming error caught the first time the code path runs rather than a
// silently unbounded check.
func mustField(key string) Field {
	field, ok := byKey[key]
	if !ok {
		panic(fmt.Sprintf("fitschema: no field %q", key))
	}

	return field
}

// IntLimit returns a field's accepted range as ints, for formInt and its
// browserfit equivalent.
func IntLimit(key string) (min, max int) {
	field := mustField(key)

	return int(field.Min), int(field.Max)
}

// FloatLimit returns a field's accepted range as float64s.
func FloatLimit(key string) (min, max float64) {
	field := mustField(key)

	return field.Min, field.Max
}

// DefaultInt returns a field's default, as an int.
func DefaultInt(key string) int {
	return mustField(key).Default.(int) //nolint:forcetypeassert // the table's own invariant
}

// DefaultInt64 returns a field's default, as an int64.
func DefaultInt64(key string) int64 {
	return mustField(key).Default.(int64) //nolint:forcetypeassert // the table's own invariant
}

// DefaultFloat returns a field's default, as a float64.
func DefaultFloat(key string) float64 {
	return mustField(key).Default.(float64) //nolint:forcetypeassert // the table's own invariant
}

// DefaultBool returns a field's default, as a bool.
func DefaultBool(key string) bool {
	return mustField(key).Default.(bool) //nolint:forcetypeassert // the table's own invariant
}

// DefaultString returns a field's default, as a string.
func DefaultString(key string) string {
	return mustField(key).Default.(string) //nolint:forcetypeassert // the table's own invariant
}

// DefaultDuration returns a field's default, as a time.Duration.
func DefaultDuration(key string) time.Duration {
	return mustField(key).Default.(time.Duration) //nolint:forcetypeassert // the table's own invariant
}
