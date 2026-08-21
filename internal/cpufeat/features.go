package cpufeat

import (
	"sync"
	"sync/atomic"
)

// Features reports CPU capabilities used for optional SIMD dispatch.
//
// The flags are grouped by the backend that consumes them rather than by the
// order the vendor added them: a dispatcher picks the widest kernel whose flags
// are all set, so the useful question is always "can this backend run", not
// "which extension is this".
type Features struct {
	// HasSSE2 and HasSSE3 gate the 4-lane XMM kernel. SSE2 is architecturally
	// guaranteed on amd64, so the flag exists to be forced off in tests rather
	// than to be doubted on hardware.
	HasSSE2 bool
	HasSSE3 bool

	// HasAVX gates 256-bit loads and stores; HasAVX2 and HasFMA together gate
	// the 8-lane packed kernel, which needs both the integer shuffle for the
	// reduction permute and the fused multiply-add for the recursion.
	HasAVX  bool
	HasAVX2 bool
	HasFMA  bool

	// HasAVX512F and HasAVX512DQ gate a 16-lane ZMM kernel. F alone is not
	// enough: the doubleword/quadword subset carries the float32 broadcast and
	// insert forms such a kernel needs.
	HasAVX512F  bool
	HasAVX512DQ bool

	// HasASIMD is arm64 NEON. It is mandatory in ARMv8-A, so a NEON kernel must
	// not gate on it; the flag exists so a forced feature set can describe an
	// arm64 host as completely as an amd64 one.
	HasASIMD bool
}

// Detect is called from the audio path on every processed block, so the common
// case must not take a lock. The detected value is published once through an
// atomic pointer; the mutex only serializes the rare test-only overrides.
var (
	current  atomic.Pointer[Features]
	detectMu sync.Mutex
)

// Detect returns the cached CPU feature set for the current process.
func Detect() Features {
	if f := current.Load(); f != nil {
		return *f
	}

	detectMu.Lock()
	defer detectMu.Unlock()

	// Another goroutine may have published between the load and the lock.
	if f := current.Load(); f != nil {
		return *f
	}

	detected := detect()
	current.Store(&detected)

	return detected
}

// SetForcedFeatures overrides hardware detection for tests.
func SetForcedFeatures(f Features) {
	detectMu.Lock()
	defer detectMu.Unlock()

	forced := f
	current.Store(&forced)
}

// ResetDetection clears forced features and the detection cache.
func ResetDetection() {
	detectMu.Lock()
	defer detectMu.Unlock()

	current.Store(nil)
}
