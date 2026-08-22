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

// init warms the cache so that the audio thread is never the goroutine that
// fills it.
//
// The steady state of Detect is a single atomic load, but the *first* call has
// to take detectMu, run the CPUID probe and publish the result. Nothing else in
// the process warmed it: the first Detect in a realtime session came from
// processRotorBlocks (internal/oscbank/kernel_amd64.go), which already runs on
// the audio thread. That is exactly one mutex acquisition per process on the
// audio path -- rare enough never to show up in a benchmark, and still a
// violation of the "no mutex acquisition on the audio path" rule, because the
// one callback that pays it can be preempted while holding a lock a non-
// realtime goroutine may also want. Package initialisation is guaranteed to
// finish before main runs, so after this init every Detect on the audio thread
// is the lock-free load.
//
// This is deliberately a plain Detect call and not a sync.Once: ResetDetection
// stores nil to force a real hardware re-detect, and a Once cannot be rearmed,
// so guarding the warm-up with one would freeze whatever the process detected
// first and silently disable the forced portable / SSE2-only kernel paths that
// validate the packed kernels. Calling Detect here is also why Detect must stay
// safe during package initialisation: internal/oscbank reads it from a
// package-level var (contract_test.go), and Go initialises this package -- init
// included -- before any package that imports it.
func init() {
	_ = Detect()
}

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
