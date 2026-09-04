package fitschema

import (
	"fmt"
	"math"
	"strconv"
	"time"
)

// ParseDuration reads a Go duration string such as "30s" or "1h30m", and,
// for compatibility with the earlier float-seconds flag the fit command
// carried, a bare number read as seconds.
//
// It used to be three functions: formDuration in internal/server/params.go,
// parseDuration in internal/browserfit/browserfit.go, and durationFlag.Set
// in internal/cli/fit.go. All three read the same two spellings the same
// way; only the error message's wording differed, which is not a reason for
// three implementations of a duration parser.
//
// A negative result is refused here rather than left to each caller's own
// range check: every duration this parser is ever asked for -- a time
// budget, a polish budget, a reference window -- is a length of time, and
// Go's own duration grammar happily parses "-1s" into one that is not.
func ParseDuration(raw string) (time.Duration, error) {
	parsed, err := parse(raw)
	if err != nil {
		return 0, err
	}

	if parsed < 0 {
		return 0, fmt.Errorf("must not be negative, got %q", raw)
	}

	return parsed, nil
}

func parse(raw string) (time.Duration, error) {
	if parsed, err := time.ParseDuration(raw); err == nil {
		return parsed, nil
	}

	seconds, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return 0, fmt.Errorf("must be a duration such as 30s or 10m, got %q", raw)
	}

	// ParseFloat accepts "NaN" and "Inf", and converting either to a
	// Duration is undefined in the language spec, so a caller's own bounds
	// check would be deciding on whatever the hardware happened to produce.
	if math.IsNaN(seconds) || math.IsInf(seconds, 0) {
		return 0, fmt.Errorf("must be a finite duration, got %q", raw)
	}

	return time.Duration(seconds * float64(time.Second)), nil
}
