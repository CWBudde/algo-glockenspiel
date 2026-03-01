package optimizer

import "testing"

func TestComputeSpectralErrorIdenticalSignals(t *testing.T) {
	signal := make([]float32, 1024)
	for i := range signal {
		if i%64 == 0 {
			signal[i] = 1
		}
	}

	got := ComputeSpectralError(signal, signal, 44100)
	if got != 0 {
		t.Fatalf("expected zero spectral error, got %g", got)
	}
}

func TestComputeSpectralErrorDetectsDifference(t *testing.T) {
	a := make([]float32, 1024)
	b := make([]float32, 1024)
	for i := range a {
		if i%64 == 0 {
			a[i] = 1
		}
		if i%32 == 0 {
			b[i] = 1
		}
	}

	got := ComputeSpectralError(a, b, 44100)
	if !(got > 0) {
		t.Fatalf("expected positive spectral error, got %g", got)
	}
}

func TestParseMetricSupportsSpectral(t *testing.T) {
	got, err := ParseMetric("spectral")
	if err != nil {
		t.Fatalf("ParseMetric failed: %v", err)
	}
	if got != MetricSpectral {
		t.Fatalf("unexpected metric: got %q", got)
	}
}
