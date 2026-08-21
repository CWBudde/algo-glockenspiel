package cpufeat

import (
	"sync"
	"sync/atomic"
)

// Features reports CPU capabilities used for optional SIMD dispatch.
type Features struct {
	HasAVX2 bool
	HasFMA  bool
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
