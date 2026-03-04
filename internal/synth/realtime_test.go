package synth

import (
	"math"
	"testing"
)

func TestRealtimeEngineRetriggeredC5StaysFiniteAndDecays(t *testing.T) {
	p := loadTestPreset(t)

	s, err := NewSynthesizer(p, 48000)
	if err != nil {
		t.Fatalf("NewSynthesizer failed: %v", err)
	}

	engine := NewRealtimeEngine(s)
	engine.SetMasterGain(0.7)

	const (
		blockFrames = 128
		totalFrames = 48000
		note        = 72
		velocity    = 100
	)

	triggerFrames := map[int]struct{}{
		0:     {},
		12000: {},
		24000: {},
		36000: {},
	}

	firstBlockPeak := 0.0
	firstBlockRMS := 0.0
	lastBlockPeak := 0.0
	lastBlockRMS := 0.0
	maxBlockRMS := 0.0
	for frame := 0; frame < totalFrames; frame += blockFrames {
		if _, ok := triggerFrames[frame]; ok {
			engine.NoteOn(note, velocity)
			if engine.ActiveVoices() != 1 {
				t.Fatalf("expected retriggered note to replace existing voice, active=%d", engine.ActiveVoices())
			}
		}

		block := engine.ProcessBlock(blockFrames)
		if len(block) != blockFrames*2 {
			t.Fatalf("unexpected block size: got %d want %d", len(block), blockFrames*2)
		}

		for _, sample := range block {
			if math.IsNaN(float64(sample)) || math.IsInf(float64(sample), 0) {
				t.Fatalf("invalid sample value: %v", sample)
			}
			if sample < -1 || sample > 1 {
				t.Fatalf("sample out of full-scale range: %v", sample)
			}
		}

		blockPeak, blockRMS := blockStats(block)
		if blockRMS > maxBlockRMS {
			maxBlockRMS = blockRMS
		}
		if frame == 0 {
			firstBlockPeak = blockPeak
			firstBlockRMS = blockRMS
		}
		if frame >= totalFrames-blockFrames {
			lastBlockPeak = blockPeak
			lastBlockRMS = blockRMS
		}
	}

	if lastBlockPeak == 0 {
		t.Fatal("expected some residual decay at end of render")
	}
	if maxBlockRMS == 0 {
		t.Fatal("expected non-zero block energy during render")
	}
	if lastBlockRMS >= maxBlockRMS {
		t.Fatalf("expected final block to be below peak run energy: first_peak=%f last_peak=%f first_rms=%f last_rms=%f max_rms=%f", firstBlockPeak, lastBlockPeak, firstBlockRMS, lastBlockRMS, maxBlockRMS)
	}
}

func blockStats(block []float32) (peak, rms float64) {
	if len(block) == 0 {
		return 0, 0
	}

	sum := 0.0
	for _, sample := range block {
		v := float64(sample)
		abs := math.Abs(v)
		if abs > peak {
			peak = abs
		}
		sum += v * v
	}

	return peak, math.Sqrt(sum / float64(len(block)))
}
