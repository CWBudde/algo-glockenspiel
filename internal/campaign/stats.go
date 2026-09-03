package campaign

import (
	"fmt"
	"math"
	"sort"

	"gonum.org/v1/gonum/stat/distuv"
)

// FamilyAlpha is the family-wise error rate the Holm correction controls. It
// is a constant rather than a flag because a threshold chosen after seeing the
// p-values is not a threshold.
const FamilyAlpha = 0.05

// bestOfMargin is how close to the best score a block still counts as "within
// reach of the best". It is relative rather than absolute because the scores
// this campaign compares live in [0, 1] and a fixed margin would mean
// something different at 0.02 than at 0.6.
const bestOfMargin = 1.05

// MeanSD returns the mean and the sample standard deviation of values.
//
// The sum of squares is taken in a second pass around the mean rather than as
// the difference of two large sums, because the differences this package feeds
// it are tiny next to the scores they came from and the one-pass form loses
// most of their digits. A single value has no spread, so its sd is zero.
func MeanSD(values []float64) (float64, float64) {
	if len(values) == 0 {
		return 0, 0
	}

	sum := 0.0
	for _, value := range values {
		sum += value
	}

	mean := sum / float64(len(values))

	if len(values) < 2 {
		return mean, 0
	}

	squares := 0.0

	for _, value := range values {
		delta := value - mean
		squares += delta * delta
	}

	return mean, math.Sqrt(squares / float64(len(values)-1))
}

// Median returns the median of values, averaging the two middle values of an
// even-sized sample. It sorts a copy, so the caller's slice keeps its order.
func Median(values []float64) float64 {
	return Quantile(values, 0.5)
}

// Quantile returns the p-quantile of values by linear interpolation between
// the two order statistics that straddle it, which is the definition R calls
// type 7 and the one every plotting library draws. Median is its p=0.5 case,
// so the quantile column and the median column of a report cannot disagree.
func Quantile(values []float64, probability float64) float64 {
	if len(values) == 0 {
		return math.NaN()
	}

	sorted := append([]float64(nil), values...)
	sort.Float64s(sorted)

	if len(sorted) == 1 {
		return sorted[0]
	}

	position := probability * float64(len(sorted)-1)

	lower := int(math.Floor(position))
	if lower < 0 {
		lower = 0
	}

	upper := lower + 1
	if upper > len(sorted)-1 {
		return sorted[len(sorted)-1]
	}

	weight := position - float64(lower)

	return sorted[lower]*(1-weight) + sorted[upper]*weight
}

// PairedGain compares two arms block by block.
//
// The arms of a block ran on one seed, so the block is the pair and the
// difference within it removes everything the two arms shared: the reference,
// the budget and the random stream. The difference is control minus candidate
// because a lower score is a better fit, so a positive gain means the
// candidate won.
//
// A zero-variance difference is a real answer rather than an error: every
// block moved the same way, so the t statistic is infinite with the sign of
// the mean, and a mean of exactly zero on zero variance is no difference at
// all.
func PairedGain(control, candidate map[int]float64) (float64, float64, int, int, error) {
	if len(control) != len(candidate) {
		return 0, 0, 0, 0, fmt.Errorf("paired gain over %d control blocks and %d candidate blocks",
			len(control), len(candidate))
	}

	blocks := make([]int, 0, len(control))

	for block := range control {
		if _, ok := candidate[block]; !ok {
			return 0, 0, 0, 0, fmt.Errorf("paired gain: block %d is missing from the candidate arm", block)
		}

		blocks = append(blocks, block)
	}

	sort.Ints(blocks)

	differences := make([]float64, 0, len(blocks))
	wins := 0

	for _, block := range blocks {
		difference := control[block] - candidate[block]
		if difference > 0 {
			wins++
		}

		differences = append(differences, difference)
	}

	if len(differences) == 0 {
		return 0, 0, 0, 0, fmt.Errorf("paired gain over no blocks")
	}

	mean, sd := MeanSD(differences)

	return mean, tStatistic(mean, sd, len(differences)), wins, len(differences), nil
}

// tStatistic is the one-sample t of a mean against zero. A zero standard
// error means the sample never varied, which the sign of the mean answers
// without arithmetic.
func tStatistic(mean, sd float64, count int) float64 {
	standardError := sd / math.Sqrt(float64(count))

	if standardError == 0 {
		switch {
		case mean > 0:
			return math.Inf(1)
		case mean < 0:
			return math.Inf(-1)
		default:
			return 0
		}
	}

	return mean / standardError
}

// TwoSidedP is the two-sided p-value of a t statistic at df degrees of
// freedom, from Student's t rather than the normal approximation: at the
// twelve blocks these designs run, the normal tail is optimistic by enough to
// change a verdict.
func TwoSidedP(t float64, df int) float64 {
	if math.IsNaN(t) {
		return math.NaN()
	}

	if math.IsInf(t, 0) {
		return 0
	}

	if df < 1 {
		return math.NaN()
	}

	student := distuv.StudentsT{Mu: 0, Sigma: 1, Nu: float64(df)}

	return 2 * (1 - student.CDF(math.Abs(t)))
}

// Holm applies the Holm step-down correction to a family of p-values and
// returns, in the caller's order, which contrasts are rejected.
//
// Holm rather than Bonferroni because it is uniformly more powerful at the
// same family-wise error rate, and step-down because the procedure stops at
// the first p it retains: once a contrast survives its threshold, every larger
// p is retained too, whatever its own threshold would have said.
func Holm(p []float64, alpha float64) []bool {
	rejected := make([]bool, len(p))

	if len(p) == 0 {
		return rejected
	}

	order := make([]int, len(p))
	for index := range order {
		order[index] = index
	}

	sort.SliceStable(order, func(a, b int) bool { return p[order[a]] < p[order[b]] })

	count := len(p)

	for rank, index := range order {
		if !(p[index] < alpha/float64(count-rank)) {
			break
		}

		rejected[index] = true
	}

	return rejected
}

// BestOfEntry is one arm's best result and the shape of the distribution
// around it. The best score alone would flatter a lucky arm, so it travels
// with the median, the quartiles and a count of how many blocks landed near
// the best: an arm that reaches its best once is a different proposition from
// one that reaches it half the time.
type BestOfEntry struct {
	Arm    string  `json:"arm"`
	Best   float64 `json:"best"`
	Block  int     `json:"block"`
	Seed   int64   `json:"seed"`
	Median float64 `json:"median"`

	// WithinMargin counts the blocks whose score is at most five percent above
	// the best.
	WithinMargin int `json:"within_margin"`

	Q25 float64 `json:"q25"`
	Q50 float64 `json:"q50"`
	Q75 float64 `json:"q75"`
}

// BestOf summarises one arm's rows. The rows must all belong to that arm;
// Analyze groups them before calling.
func BestOf(rows []Row) BestOfEntry {
	entry := BestOfEntry{Best: math.NaN(), Median: math.NaN(), Q25: math.NaN(), Q50: math.NaN(), Q75: math.NaN()}

	if len(rows) == 0 {
		return entry
	}

	entry.Arm = rows[0].Arm

	scores := make([]float64, 0, len(rows))
	best := rows[0]

	for _, row := range rows {
		scores = append(scores, row.Score)

		if row.Score < best.Score {
			best = row
		}
	}

	entry.Best = best.Score
	entry.Block = best.Block
	entry.Seed = best.Seed

	for _, score := range scores {
		if score <= best.Score*bestOfMargin {
			entry.WithinMargin++
		}
	}

	entry.Q25 = Quantile(scores, 0.25)
	entry.Q50 = Quantile(scores, 0.50)
	entry.Q75 = Quantile(scores, 0.75)
	entry.Median = entry.Q50

	return entry
}
