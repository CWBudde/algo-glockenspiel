package cpufeat

import (
	"runtime"
	"testing"
)

func TestDetectReturnsValidFeatures(t *testing.T) {
	t.Cleanup(ResetDetection)
	ResetDetection()

	features := Detect()

	if runtime.GOARCH == "amd64" && features.HasAVX2 != Detect().HasAVX2 {
		t.Fatal("expected stable cached detection result")
	}
}

func TestSetForcedFeaturesOverridesDetection(t *testing.T) {
	t.Cleanup(ResetDetection)
	ResetDetection()

	SetForcedFeatures(Features{HasAVX2: true})
	if !Detect().HasAVX2 {
		t.Fatal("expected forced AVX2 feature to be visible")
	}

	SetForcedFeatures(Features{HasAVX2: false})
	if Detect().HasAVX2 {
		t.Fatal("expected forced AVX2 disable to be visible")
	}
}

func TestResetDetectionClearsForcedFeatures(t *testing.T) {
	t.Cleanup(ResetDetection)
	ResetDetection()

	SetForcedFeatures(Features{HasAVX2: true})
	if !Detect().HasAVX2 {
		t.Fatal("expected forced feature to be active")
	}

	ResetDetection()
	_ = Detect()
}
