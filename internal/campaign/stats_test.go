package campaign_test

import (
	"math"
	"testing"

	"github.com/cwbudde/algo-glockenspiel/internal/campaign"
)

// nearly reports whether two floats agree to within tolerance. The expected
// values in this file are hand-computed, so they are written to the digits a
// reader can check and compared loosely enough for that.
func nearly(got, want, tolerance float64) bool {
	return math.Abs(got-want) <= tolerance
}

func TestMeanSDMatchesAHandComputation(t *testing.T) {
	mean, sd := campaign.MeanSD([]float64{1, 2, 3, 4})

	if !nearly(mean, 2.5, 1e-12) {
		t.Errorf("mean is %g, want 2.5", mean)
	}

	// Deviations -1.5, -0.5, 0.5, 1.5 square to 5, and 5/3 has square root
	// 1.290994.
	if !nearly(sd, 1.2909944487, 1e-9) {
		t.Errorf("sd is %g, want 1.290994", sd)
	}

	if _, single := campaign.MeanSD([]float64{7}); single != 0 {
		t.Errorf("the sd of a single value is %g, want 0", single)
	}
}

func TestMedianHandlesOddAndEvenCounts(t *testing.T) {
	if got := campaign.Median([]float64{3, 1, 2}); got != 2 {
		t.Errorf("the median of {1,2,3} is %g, want 2", got)
	}

	if got := campaign.Median([]float64{4, 1, 3, 2}); got != 2.5 {
		t.Errorf("the median of {1,2,3,4} is %g, want 2.5", got)
	}

	if got := campaign.Median([]float64{5}); got != 5 {
		t.Errorf("the median of {5} is %g, want 5", got)
	}

	values := []float64{4, 1, 3, 2}
	campaign.Median(values)

	if values[0] != 4 {
		t.Errorf("Median reordered the caller's slice: %v", values)
	}
}

func TestPairedGainMatchesAHandComputation(t *testing.T) {
	// The candidate scores zero everywhere, so the differences are the
	// control's own scores: 1, 2, 3, 4. Their mean is 2.5, their sample sd is
	// 1.290994, and t is 2.5/(1.290994/2) = 3.872983.
	control := map[int]float64{1: 1, 2: 2, 3: 3, 4: 4}
	candidate := map[int]float64{1: 0, 2: 0, 3: 0, 4: 0}

	gain, tStat, wins, n, err := campaign.PairedGain(control, candidate)
	if err != nil {
		t.Fatalf("paired gain: %v", err)
	}

	if !nearly(gain, 2.5, 1e-12) {
		t.Errorf("gain is %g, want 2.5", gain)
	}

	if !nearly(tStat, 3.8729833462, 1e-9) {
		t.Errorf("t is %g, want 3.872983", tStat)
	}

	if wins != 4 || n != 4 {
		t.Errorf("the candidate won %d of %d blocks, want 4 of 4", wins, n)
	}
}

func TestPairedGainRefusesBlockSetsThatDoNotMatch(t *testing.T) {
	_, _, _, _, err := campaign.PairedGain(map[int]float64{1: 1, 2: 1}, map[int]float64{1: 1, 3: 1})
	if err == nil {
		t.Fatal("pairing two arms on different blocks succeeded")
	}
}

func TestPairedGainSignsAZeroVarianceDifference(t *testing.T) {
	cases := []struct {
		name      string
		candidate map[int]float64
		want      float64
	}{
		{"the candidate wins every block by the same amount", map[int]float64{1: 0.5, 2: 0.5}, math.Inf(1)},
		{"the control wins every block by the same amount", map[int]float64{1: 1.5, 2: 1.5}, math.Inf(-1)},
		{"the arms tie in every block", map[int]float64{1: 1, 2: 1}, 0},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			_, tStat, _, _, err := campaign.PairedGain(map[int]float64{1: 1, 2: 1}, testCase.candidate)
			if err != nil {
				t.Fatalf("paired gain: %v", err)
			}

			if tStat != testCase.want {
				t.Errorf("t is %g, want %g", tStat, testCase.want)
			}
		})
	}
}

func TestTwoSidedPMatchesTheTable(t *testing.T) {
	// 2.201 is the two-sided 5 percent critical value of Student's t at eleven
	// degrees of freedom, which is the df of the twelve-block designs.
	if got := campaign.TwoSidedP(2.201, 11); !nearly(got, 0.05, 1e-3) {
		t.Errorf("p at t=2.201, df=11 is %g, want 0.050", got)
	}

	if got := campaign.TwoSidedP(0, 11); !nearly(got, 1, 1e-12) {
		t.Errorf("p at t=0 is %g, want 1", got)
	}

	if got := campaign.TwoSidedP(math.Inf(1), 11); got != 0 {
		t.Errorf("p at an infinite t is %g, want 0", got)
	}

	if got := campaign.TwoSidedP(math.Inf(-1), 11); got != 0 {
		t.Errorf("p at a negative infinite t is %g, want 0", got)
	}

	if got := campaign.TwoSidedP(math.NaN(), 11); !math.IsNaN(got) {
		t.Errorf("p at a NaN t is %g, want NaN", got)
	}
}

func TestHolmStopsAtTheFirstRetainedContrast(t *testing.T) {
	// Thresholds at m=4 are 0.05/4, 0.05/3, 0.05/2 and 0.05. The third p-value
	// is 0.03, which is not below 0.05/2, so it is retained and so is the
	// fourth, whose own threshold it would have passed.
	got := campaign.Holm([]float64{0.001, 0.01, 0.03, 0.4}, campaign.FamilyAlpha)

	want := []bool{true, true, false, false}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("Holm returned %v, want %v", got, want)
		}
	}
}

func TestHolmRetainsEverythingAfterTheFirstFailure(t *testing.T) {
	// The smallest p-value fails its own 0.05/2 threshold, so the second is
	// retained even though 0.04 is below the 0.05 its rank would allow. That
	// early stop is what makes the procedure step-down rather than a
	// per-rank Bonferroni.
	got := campaign.Holm([]float64{0.04, 0.03}, campaign.FamilyAlpha)

	if got[0] || got[1] {
		t.Errorf("Holm returned %v, want both contrasts retained", got)
	}
}

func TestHolmCorrectsInTheCallersOrder(t *testing.T) {
	// The rejected contrast is the last one given, so a result reported in
	// sorted order rather than the caller's would mark the wrong one.
	got := campaign.Holm([]float64{0.9, 0.8, 0.001}, campaign.FamilyAlpha)

	want := []bool{false, false, true}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("Holm returned %v, want %v", got, want)
		}
	}
}

func TestBestOfDescribesTheDistributionAroundTheBest(t *testing.T) {
	rows := []campaign.Row{
		{Arm: "sep-cmaes-r", Block: 1, Seed: 11, Score: 0.5},
		{Arm: "sep-cmaes-r", Block: 2, Seed: 12, Score: 0.2},
		{Arm: "sep-cmaes-r", Block: 3, Seed: 13, Score: 0.21},
		{Arm: "sep-cmaes-r", Block: 4, Seed: 14, Score: 0.3},
	}

	entry := campaign.BestOf(rows)

	if entry.Arm != "sep-cmaes-r" {
		t.Errorf("the entry is for arm %q", entry.Arm)
	}

	if entry.Best != 0.2 || entry.Block != 2 || entry.Seed != 12 {
		t.Errorf("the best is %g in block %d at seed %d, want 0.2 in block 2 at seed 12",
			entry.Best, entry.Block, entry.Seed)
	}

	// Sorted, the scores are 0.2, 0.21, 0.3, 0.5, so the median is 0.255.
	if !nearly(entry.Median, 0.255, 1e-12) {
		t.Errorf("the median is %g, want 0.255", entry.Median)
	}

	// The margin is 0.2*1.05 = 0.21, which 0.21 itself meets.
	if entry.WithinMargin != 2 {
		t.Errorf("%d scores are within five percent of the best, want 2", entry.WithinMargin)
	}

	if !nearly(entry.Q25, 0.2075, 1e-12) {
		t.Errorf("q25 is %g, want 0.2075", entry.Q25)
	}

	if !nearly(entry.Q75, 0.35, 1e-12) {
		t.Errorf("q75 is %g, want 0.35", entry.Q75)
	}
}
