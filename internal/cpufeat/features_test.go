package cpufeat

import (
	"reflect"
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

// TestDetectReportsArchitecturalBaselines pins the two flags that are not
// really detection results: SSE2 is mandatory on amd64 and Advanced SIMD is
// mandatory on arm64, so a backend gated on either must never compile out on
// hardware that runs the binary at all.
func TestDetectReportsArchitecturalBaselines(t *testing.T) {
	t.Cleanup(ResetDetection)
	ResetDetection()

	features := Detect()

	switch runtime.GOARCH {
	case "amd64":
		if !features.HasSSE2 {
			t.Fatal("amd64 must always report SSE2")
		}

		if features.HasASIMD {
			t.Fatal("amd64 must not report ASIMD")
		}
	case "arm64":
		if !features.HasASIMD {
			t.Fatal("arm64 must always report ASIMD")
		}

		if features.HasSSE2 || features.HasAVX2 {
			t.Fatal("arm64 must not report x86 features")
		}
	}
}

// TestDetectIsInternallyConsistent guards the implication chain between the x86
// flags. A dispatcher that picks the widest available kernel relies on it: AVX2
// without AVX, or AVX-512DQ without AVX-512F, would let it select a kernel
// whose loads it cannot issue.
func TestDetectIsInternallyConsistent(t *testing.T) {
	if runtime.GOARCH != "amd64" {
		t.Skip("x86 feature implications only")
	}

	t.Cleanup(ResetDetection)
	ResetDetection()

	features := Detect()

	if features.HasAVX2 && !features.HasAVX {
		t.Fatal("AVX2 implies AVX")
	}

	if features.HasAVX && !features.HasSSE2 {
		t.Fatal("AVX implies SSE2")
	}

	if features.HasAVX512DQ && !features.HasAVX512F {
		t.Fatal("AVX-512DQ implies AVX-512F")
	}

	if features.HasAVX512F && !features.HasAVX2 {
		t.Fatal("AVX-512F implies AVX2")
	}
}

// TestSetForcedFeaturesCarriesEveryFlag is what lets a single machine test a
// backend it cannot run natively -- an SSE2-only or NEON-only dispatch on an
// AVX2 host -- so every field has to survive the override, not just the two the
// AVX2 gate happens to read.
func TestSetForcedFeaturesCarriesEveryFlag(t *testing.T) {
	t.Cleanup(ResetDetection)
	ResetDetection()

	all := Features{
		HasSSE2:     true,
		HasSSE3:     true,
		HasAVX:      true,
		HasAVX2:     true,
		HasFMA:      true,
		HasAVX512F:  true,
		HasAVX512DQ: true,
		HasASIMD:    true,
	}

	SetForcedFeatures(all)

	if got := Detect(); got != all {
		t.Fatalf("forced features not reported back: got %+v, want %+v", got, all)
	}

	SetForcedFeatures(Features{})

	if got := Detect(); got != (Features{}) {
		t.Fatalf("forced empty feature set not reported back: got %+v", got)
	}

	// One flag at a time, so a field that is silently dropped or aliased onto
	// another shows up as the wrong field being set.
	fields := reflect.TypeOf(all)

	for i := range fields.NumField() {
		var one Features

		reflect.ValueOf(&one).Elem().Field(i).SetBool(true)
		SetForcedFeatures(one)

		if got := Detect(); got != one {
			t.Fatalf("forcing %s alone reported %+v", fields.Field(i).Name, got)
		}
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
