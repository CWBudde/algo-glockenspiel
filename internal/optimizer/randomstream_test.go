package optimizer

import (
	"fmt"
	"math"
	"testing"
)

// streamFamilyNames is every family derivedSeed serves, so a test that walks
// them fails when one is added without being considered here.
var streamFamilyNames = map[uint64]string{
	streamPrimary:  "primary",
	streamWarm:     "warm",
	streamColdMean: "cold-mean",
}

// TestStreamsAreIndependentAcrossAdjacentSeeds pins the property a paired
// campaign design depends on: two runs whose seeds differ by one share no
// random stream, in any family.
//
// The derivation this replaced offset the seed by the index, so block b's
// round k was block b+1's round k-1 and a restarting arm's blocks were not
// independent samples. The case below is the one that failed: a campaign's
// twelve consecutive block seeds over a sixteen-round schedule.
func TestStreamsAreIndependentAcrossAdjacentSeeds(t *testing.T) {
	t.Parallel()

	const (
		seedBase = 121_000
		blocks   = 12
		indices  = 16
	)

	if uint64(len(streamFamilyNames)) != streamFamilies {
		t.Fatalf("%d families are named but streamFamilies is %d", len(streamFamilyNames), streamFamilies)
	}

	seen := make(map[int64]string, blocks*indices*len(streamFamilyNames))

	for block := range blocks {
		base := int64(seedBase + block)

		for index := range indices {
			for family, name := range streamFamilyNames {
				stream := derivedSeed(base, family, index)
				where := fmt.Sprintf("%s stream of block %d index %d", name, block, index)

				if prior, ok := seen[stream]; ok {
					t.Errorf("%s reuses the stream of %s (seed %d)", where, prior, stream)
				}

				seen[stream] = where
			}
		}
	}
}

// TestPrimaryIndexZeroKeepsTheResolvedSeed pins the reproduction property the
// checkpoint depends on: the seed a run reports is the seed its first round or
// run went out with.
func TestPrimaryIndexZeroKeepsTheResolvedSeed(t *testing.T) {
	t.Parallel()

	for _, seed := range []int64{1, -7, 121_000} {
		if got := derivedSeed(seed, streamPrimary, 0); got != seed {
			t.Errorf("derivedSeed(%d, primary, 0) = %d, want the seed itself", seed, got)
		}
	}
}

// TestDerivedSeedsArePositiveAndNonZero pins the invariant every backend needs:
// zero means "choose one" elsewhere in this package, so no derivation may
// produce it, and a negative seed is not a value every library accepts.
func TestDerivedSeedsArePositiveAndNonZero(t *testing.T) {
	t.Parallel()

	for _, base := range []int64{0, 1, -1, math.MinInt64, math.MaxInt64, 121_000} {
		for index := 1; index < 64; index++ {
			for family, name := range streamFamilyNames {
				if stream := derivedSeed(base, family, index); stream <= 0 {
					t.Errorf("%s stream of base %d index %d is %d, want positive", name, base, index, stream)
				}
			}
		}
	}
}

// TestMayflyStreamHelpersUseTheSharedDerivation keeps the mayfly wrappers from
// drifting away from the families the test above walks.
func TestMayflyStreamHelpersUseTheSharedDerivation(t *testing.T) {
	t.Parallel()

	for round := range 4 {
		if got, want := roundStream(121_000, round), derivedSeed(121_000, streamPrimary, round); got != want {
			t.Errorf("roundStream(_, %d) = %d, want %d", round, got, want)
		}

		if got, want := warmStream(121_000, round), derivedSeed(121_000, streamWarm, round); got != want {
			t.Errorf("warmStream(_, %d) = %d, want %d", round, got, want)
		}
	}
}
