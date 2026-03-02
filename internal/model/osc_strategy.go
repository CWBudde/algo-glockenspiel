package model

import "sync"

type avx2OscillatorStrategy int

const (
	avx2OscillatorStrategyModeParallel avx2OscillatorStrategy = iota
	avx2OscillatorStrategyModeBlock4
)

var (
	avx2OscillatorStrategyMu     sync.RWMutex
	forcedAVX2OscillatorStrategy *avx2OscillatorStrategy
)

func currentAVX2OscillatorStrategy() avx2OscillatorStrategy {
	avx2OscillatorStrategyMu.RLock()
	defer avx2OscillatorStrategyMu.RUnlock()

	if forcedAVX2OscillatorStrategy != nil {
		return *forcedAVX2OscillatorStrategy
	}

	// Default to the existing mode-parallel kernel. It is smaller and easier to maintain.
	return avx2OscillatorStrategyModeParallel
}

func setForcedAVX2OscillatorStrategy(strategy avx2OscillatorStrategy) {
	avx2OscillatorStrategyMu.Lock()
	defer avx2OscillatorStrategyMu.Unlock()

	copy := strategy
	forcedAVX2OscillatorStrategy = &copy
}

func resetAVX2OscillatorStrategy() {
	avx2OscillatorStrategyMu.Lock()
	defer avx2OscillatorStrategyMu.Unlock()

	forcedAVX2OscillatorStrategy = nil
}
