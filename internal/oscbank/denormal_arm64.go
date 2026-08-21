//go:build arm64

package oscbank

const (
	flushDenormalsSupported = true

	// FPCR bit 24 is FZ. AArch64 has no separate denormals-are-zero control:
	// FZ flushes denormal inputs and results alike, so one bit covers what
	// MXCSR needs two for. FZ16 (bit 19) is left alone -- nothing here is
	// half-precision.
	fpFlushMask uint64 = 1 << 24
)

func getFPMode() uint64 { return loadFPCR() }

func setFPMode(mode uint64) { storeFPCR(mode) }

// loadFPCR returns the current floating-point control register.
func loadFPCR() uint64

// storeFPCR replaces the floating-point control register.
func storeFPCR(mode uint64)
