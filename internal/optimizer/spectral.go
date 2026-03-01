package optimizer

import (
	"errors"
	"fmt"
	"math"
	"math/cmplx"
	"sync"

	algofft "github.com/cwbudde/algo-fft"
)

var spectralPlanCache sync.Map // map[int]*spectralFFTPlan

type spectralFFTPlan struct {
	mu   sync.Mutex
	fast *algofft.FastPlanReal64
	safe *algofft.PlanRealT[float64, complex128]
}

// ComputeSpectralError returns weighted dB-domain magnitude-spectrum RMSE.
func ComputeSpectralError(synth, ref []float32, sampleRate int) float64 {
	aw, bw, binHz, bins := spectralWindowedInputs32(synth, ref, sampleRate)
	if bins < 2 {
		return math.Inf(1)
	}

	plan, err := getSpectralFFTPlan(len(aw))
	if err != nil {
		return spectralRMSEDBNaiveWindowed32(aw, bw, binHz, bins)
	}

	specA := make([]complex128, bins+1)
	specB := make([]complex128, bins+1)
	if err := plan.forward(specA, aw); err != nil {
		return spectralRMSEDBNaiveWindowed32(aw, bw, binHz, bins)
	}
	if err := plan.forward(specB, bw); err != nil {
		return spectralRMSEDBNaiveWindowed32(aw, bw, binHz, bins)
	}

	var weightedSum float64
	var weightTotal float64
	for k := 1; k < bins; k++ {
		ma := linToDB(cmplx.Abs(specA[k]))
		mb := linToDB(cmplx.Abs(specB[k]))
		delta := ma - mb
		weight := spectralBinWeight(float64(k) * binHz)
		weightedSum += weight * delta * delta
		weightTotal += weight
	}
	if weightTotal == 0 {
		return math.Inf(1)
	}
	return math.Sqrt(weightedSum / weightTotal)
}

func spectralWindowedInputs32(a, b []float32, sampleRate int) ([]float64, []float64, float64, int) {
	n := minInt(len(a), len(b))
	if n < 512 || sampleRate <= 0 {
		return nil, nil, 0, 0
	}
	if n > 4096 {
		n = 4096
	}
	if n%2 != 0 {
		n--
	}
	if n < 512 {
		return nil, nil, 0, 0
	}

	aw := make([]float64, n)
	bw := make([]float64, n)
	for i := 0; i < n; i++ {
		w := 0.5 - 0.5*math.Cos(2*math.Pi*float64(i)/float64(n-1))
		aw[i] = float64(a[i]) * w
		bw[i] = float64(b[i]) * w
	}
	return aw, bw, float64(sampleRate) / float64(n), n / 2
}

func getSpectralFFTPlan(n int) (*spectralFFTPlan, error) {
	if v, ok := spectralPlanCache.Load(n); ok {
		return v.(*spectralFFTPlan), nil
	}

	p := &spectralFFTPlan{}
	fast, err := algofft.NewFastPlanReal64(n)
	if err == nil {
		p.fast = fast
	} else if !errors.Is(err, algofft.ErrNotImplemented) {
		// Ignore fast-plan setup errors and rely on the safe plan.
	}

	safe, err := algofft.NewPlanReal64(n)
	if err != nil {
		if p.fast == nil {
			return nil, err
		}
	} else {
		p.safe = safe
	}

	actual, _ := spectralPlanCache.LoadOrStore(n, p)
	return actual.(*spectralFFTPlan), nil
}

func (p *spectralFFTPlan) forward(dst []complex128, src []float64) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.fast != nil {
		p.fast.Forward(dst, src)
		return nil
	}
	if p.safe != nil {
		return p.safe.Forward(dst, src)
	}
	return errors.New("optimizer: missing spectral FFT plan")
}

func spectralRMSEDBNaiveWindowed32(aw, bw []float64, binHz float64, bins int) float64 {
	if bins < 2 {
		return math.Inf(1)
	}

	var weightedSum float64
	var weightTotal float64
	for k := 1; k < bins; k++ {
		ma := linToDB(dftBinMag(aw, k))
		mb := linToDB(dftBinMag(bw, k))
		delta := ma - mb
		weight := spectralBinWeight(float64(k) * binHz)
		weightedSum += weight * delta * delta
		weightTotal += weight
	}
	if weightTotal == 0 {
		return math.Inf(1)
	}
	return math.Sqrt(weightedSum / weightTotal)
}

func dftBinMag(x []float64, bin int) float64 {
	n := len(x)
	var re, im float64
	for i := 0; i < n; i++ {
		phi := -2 * math.Pi * float64(bin*i) / float64(n)
		re += x[i] * math.Cos(phi)
		im += x[i] * math.Sin(phi)
	}
	return math.Hypot(re, im)
}

func linToDB(x float64) float64 {
	if x < 1e-12 {
		x = 1e-12
	}
	return 20 * math.Log10(x)
}

func spectralBinWeight(freqHz float64) float64 {
	switch {
	case freqHz < 500:
		return 3.0
	case freqHz < 2000:
		return 2.0
	default:
		return 1.0
	}
}

// ParseMetric converts a user-facing metric string into a Metric value.
func ParseMetric(value string) (Metric, error) {
	switch value {
	case string(MetricRMS):
		return MetricRMS, nil
	case string(MetricLog):
		return MetricLog, nil
	case string(MetricSpectral):
		return MetricSpectral, nil
	default:
		return "", fmt.Errorf("unsupported metric %q", value)
	}
}
