package synth

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/go-audio/wav"
)

func TestLegacyComparisonA4(t *testing.T) {
	if os.Getenv("GLOCKENSPIEL_STRICT_LEGACY_COMPARE") != "1" {
		t.Skip("set GLOCKENSPIEL_STRICT_LEGACY_COMPARE=1 to run strict legacy waveform comparison")
	}

	legacyPath := filepath.FromSlash("../../testdata/reference/legacy_synth_a4.wav")
	goPath := filepath.FromSlash("../../testdata/output/go_synth_a4.wav")

	if _, err := os.Stat(legacyPath); err != nil {
		t.Skipf("legacy reference missing: %v", err)
	}

	if _, err := os.Stat(goPath); err != nil {
		t.Skipf("go reference missing: %v", err)
	}

	legacySamples, legacyRate, err := loadMonoWAV(legacyPath)
	if err != nil {
		t.Fatalf("load legacy wav: %v", err)
	}

	goSamples, goRate, err := loadMonoWAV(goPath)
	if err != nil {
		t.Fatalf("load go wav: %v", err)
	}

	if legacyRate != goRate {
		t.Fatalf("sample-rate mismatch: legacy=%d go=%d", legacyRate, goRate)
	}

	n := minInt(len(legacySamples), len(goSamples))
	legacySamples = legacySamples[:n]
	goSamples = goSamples[:n]

	corr := correlation(legacySamples, goSamples)
	rms := rmsDifference(legacySamples, goSamples)

	if corr < 0.95 {
		t.Fatalf("correlation too low: got %.4f want >= 0.95 (rms=%.6f)", corr, rms)
	}
}

func loadMonoWAV(path string) ([]float64, int, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, 0, fmt.Errorf("open wav: %w", err)
	}

	defer func() {
		_ = file.Close()
	}()

	decoder := wav.NewDecoder(file)
	if !decoder.IsValidFile() {
		return nil, 0, fmt.Errorf("invalid wav file: %s", path)
	}

	intBuffer, err := decoder.FullPCMBuffer()
	if err != nil {
		return nil, 0, fmt.Errorf("decode pcm: %w", err)
	}

	if intBuffer == nil || intBuffer.Format == nil {
		return nil, 0, fmt.Errorf("invalid decoded buffer: %s", path)
	}

	bitDepth := intBuffer.SourceBitDepth
	if bitDepth <= 0 {
		bitDepth = 16
	}

	scale := math.Pow(2, float64(bitDepth-1))

	chans := intBuffer.Format.NumChannels
	if chans <= 0 {
		chans = 1
	}

	samples := make([]float64, len(intBuffer.Data)/chans)
	for i := range samples {
		samples[i] = float64(intBuffer.Data[i*chans]) / scale
	}

	return samples, intBuffer.Format.SampleRate, nil
}

func correlation(a, b []float64) float64 {
	if len(a) == 0 || len(a) != len(b) {
		return 0
	}

	meanA := mean(a)
	meanB := mean(b)

	var (
		num  float64
		denA float64
		denB float64
	)

	for i := range a {
		da := a[i] - meanA
		db := b[i] - meanB
		num += da * db
		denA += da * da
		denB += db * db
	}

	den := math.Sqrt(denA * denB)
	if den == 0 {
		return 0
	}

	return num / den
}

func rmsDifference(a, b []float64) float64 {
	if len(a) == 0 || len(a) != len(b) {
		return math.Inf(1)
	}

	sum := 0.0

	for i := range a {
		d := a[i] - b[i]
		sum += d * d
	}

	return math.Sqrt(sum / float64(len(a)))
}

func mean(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}

	sum := 0.0
	for _, v := range values {
		sum += v
	}

	return sum / float64(len(values))
}

func minInt(a, b int) int {
	if a < b {
		return a
	}

	return b
}
