//go:build !amd64 && !arm64

package oscbank

// No portable way exists to reach the floating-point control register, and
// several ports have none to reach: GOARCH=wasm, which this project builds for,
// specifies IEEE denormal behaviour and exposes no mode bits at all. Denormals
// therefore keep their IEEE semantics here and a decaying bank stays as slow as
// the host makes it.
const (
	flushDenormalsSupported = false

	fpFlushMask uint64 = 0
)

func getFPMode() uint64 { return 0 }

func setFPMode(uint64) {}
