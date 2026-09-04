package optimizer

import (
	"math"
	"testing"
)

// decayingTone is a fixed signal with something for every frame to see: two
// partials that decay at different rates, so the frames differ from one
// another and a transform that dropped or reordered them would show.
func decayingTone(sampleRate, sampleCount int) []float32 {
	signal := make([]float32, sampleCount)

	for i := range signal {
		seconds := float64(i) / float64(sampleRate)
		value := math.Sin(2*math.Pi*880*seconds)*math.Exp(-seconds/0.4) +
			0.3*math.Sin(2*math.Pi*2637*seconds)*math.Exp(-seconds/0.12)

		signal[i] = float32(value)
	}

	return signal
}

// The exported view has to be the objective's own transform and not a second
// implementation that agrees today, so this compares it bin for bin against
// the unexported spectrogram the composite objective scores from.
func TestComputeSpectrogramIsTheObjectivesOwnTransform(t *testing.T) {
	const (
		sampleRate  = 44100
		sampleCount = 44100
	)

	signal := decayingTone(sampleRate, sampleCount)

	for _, frameSize := range []int{SpectrogramCoarseFrameSize, SpectrogramFineFrameSize} {
		internal := newSpectrogram(signal, sampleRate, frameSize)
		if internal == nil {
			t.Fatalf("the objective took no spectrogram at frame size %d", frameSize)
		}

		view := ComputeSpectrogram(signal, sampleRate, frameSize)
		if view == nil {
			t.Fatalf("ComputeSpectrogram returned nothing at frame size %d", frameSize)
		}

		if view.FrameSize != internal.frameSize || view.Hop != internal.hop {
			t.Fatalf("frame size %d: view has frameSize=%d hop=%d, the objective has %d and %d",
				frameSize, view.FrameSize, view.Hop, internal.frameSize, internal.hop)
		}

		if view.FloorDB != internal.floorDB {
			t.Fatalf("frame size %d: view floor %v, the objective's floor %v",
				frameSize, view.FloorDB, internal.floorDB)
		}

		if view.Frames != len(internal.frames) || view.Frames != len(view.DB) {
			t.Fatalf("frame size %d: view has %d frames and %d rows, the objective has %d",
				frameSize, view.Frames, len(view.DB), len(internal.frames))
		}

		if view.Bins != frameSize/2+1 {
			t.Fatalf("frame size %d: view has %d bins, want %d", frameSize, view.Bins, frameSize/2+1)
		}

		for i, frame := range internal.frames {
			if len(view.DB[i]) != len(frame) {
				t.Fatalf("frame size %d, frame %d: view has %d bins, the objective has %d",
					frameSize, i, len(view.DB[i]), len(frame))
			}

			for k, want := range frame {
				if view.DB[i][k] != want {
					t.Fatalf("frame size %d, frame %d, bin %d: view has %v, the objective has %v",
						frameSize, i, k, view.DB[i][k], want)
				}
			}
		}
	}
}

// A signal shorter than a frame is one the objective measures no spectral
// term for at all, and the view says the same by returning nothing rather
// than a picture of zero frames a caller would draw as silence.
func TestComputeSpectrogramReturnsNothingBelowOneFrame(t *testing.T) {
	signal := decayingTone(44100, SpectrogramCoarseFrameSize-1)

	if view := ComputeSpectrogram(signal, 44100, SpectrogramCoarseFrameSize); view != nil {
		t.Fatalf("a signal shorter than a frame produced %d frames", view.Frames)
	}
}
