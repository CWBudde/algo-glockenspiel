package cpufeat

import "sync"

// Features reports CPU capabilities used for optional SIMD dispatch.
type Features struct {
	HasAVX2 bool
}

var (
	detectOnce sync.Once
	detected   Features

	detectMu sync.Mutex

	forcedMu sync.RWMutex
	forced   *Features
)

// Detect returns the cached CPU feature set for the current process.
func Detect() Features {
	forcedMu.RLock()
	override := forced
	forcedMu.RUnlock()
	if override != nil {
		return *override
	}

	detectMu.Lock()
	detectOnce.Do(func() {
		detected = detect()
	})
	result := detected
	detectMu.Unlock()

	return result
}

// SetForcedFeatures overrides hardware detection for tests.
func SetForcedFeatures(f Features) {
	forcedMu.Lock()
	defer forcedMu.Unlock()

	copy := f
	forced = &copy
}

// ResetDetection clears forced features and the detection cache.
func ResetDetection() {
	forcedMu.Lock()
	forced = nil
	forcedMu.Unlock()

	detectMu.Lock()
	detectOnce = sync.Once{}
	detected = Features{}
	detectMu.Unlock()
}
