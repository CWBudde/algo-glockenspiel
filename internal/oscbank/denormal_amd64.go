//go:build amd64

package oscbank

const (
	flushDenormalsSupported = true

	// MXCSR bit 15 is flush-to-zero (denormal results become zero) and bit 6 is
	// denormals-are-zero (denormal operands read as zero). Both are needed: FTZ
	// alone still pays the microcode penalty on a denormal that is already in
	// the rotor state.
	fpFlushMask uint64 = 1<<15 | 1<<6
)

func getFPMode() uint64 { return uint64(loadMXCSR()) }

func setFPMode(mode uint64) { storeMXCSR(uint32(mode)) }

// loadMXCSR returns the current SSE control and status word.
func loadMXCSR() uint32

// storeMXCSR replaces the SSE control and status word.
func storeMXCSR(mode uint32)
