package fitrun

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"os"

	"github.com/cwbudde/algo-glockenspiel/internal/optimizer"
)

// traceWriter appends one JSON object per progress report to trace.jsonl.
//
// The file is the record a campaign scores from. Its key order is fixed and
// written by hand rather than through a struct, because the collect step reads
// the same lines from runs produced months apart and a reordering by some
// future marshaller would be an invisible format change.
//
// The terms are attached only to a line whose best cost improved, and always
// to the first line. Measuring them costs one extra render per report, which
// is negligible against a generation, but a line that repeats the previous
// line's breakdown says nothing and the file is read start to finish. A line
// whose breakdown could not be measured is written without them and does not
// count as the improvement, so the terms land on the next line that can carry
// them.
type traceWriter struct {
	file     *os.File
	buffered *bufio.Writer
	best     float64
	written  bool
}

func newTraceWriter(path string) (*traceWriter, error) {
	file, err := os.Create(path)
	if err != nil {
		return nil, fmt.Errorf("create trace %q: %w", path, err)
	}

	return &traceWriter{file: file, buffered: bufio.NewWriter(file), best: math.Inf(1)}, nil
}

// append writes one line. metrics and score are used only when the line
// carries a breakdown, which improved reports.
func (w *traceWriter) append(progress optimizer.Progress, breakdown func() (optimizer.Metrics, float64, bool)) error {
	improved := !w.written || progress.BestCost < w.best

	var buffer bytes.Buffer

	_, _ = fmt.Fprintf(&buffer,
		`{"iteration":%d,"optimizer_iterations":%d,"restart":%d,"lambda":%d,"evaluations":%d,"elapsed_ms":%d,`,
		progress.Iteration, progress.OptimizerIterations, progress.Restart, progress.Lambda,
		progress.Evaluations, progress.Elapsed.Milliseconds())

	writeTraceFloat(&buffer, "current", progress.CurrentCost)
	buffer.WriteByte(',')
	writeTraceFloat(&buffer, "best", progress.BestCost)

	// A breakdown that could not be measured leaves the line without its
	// terms, and then the improvement has to stay unrecorded: advancing the
	// best here would let the next improving line be judged against a cost
	// whose breakdown was never written, and the run would end with no line
	// carrying terms at all.
	measured := false

	if improved {
		if metrics, score, ok := breakdown(); ok {
			measured = true

			buffer.WriteByte(',')
			writeTraceFloat(&buffer, "score", score)

			encoded, err := json.Marshal(metrics)
			if err != nil {
				return fmt.Errorf("encode trace terms: %w", err)
			}

			buffer.WriteString(`,"terms":`)
			buffer.Write(encoded)
		}
	}

	buffer.WriteString("}\n")

	if _, err := w.buffered.Write(buffer.Bytes()); err != nil {
		return fmt.Errorf("write trace line: %w", err)
	}

	if improved && measured {
		w.best = progress.BestCost
		w.written = true
	}

	// Flushed per line so a run killed mid-search still leaves a trace the
	// campaign can score, which is the whole reason the file is a stream of
	// lines rather than an array written at the end.
	return w.buffered.Flush()
}

func (w *traceWriter) Close() error {
	if err := w.buffered.Flush(); err != nil {
		_ = w.file.Close()

		return fmt.Errorf("flush trace: %w", err)
	}

	if err := w.file.Close(); err != nil {
		return fmt.Errorf("close trace: %w", err)
	}

	return nil
}

// writeTraceFloat writes a float the way optimizer.Metrics does, as null when
// it is not a number, because JSON has no NaN and a cost of +Inf is what an
// unmeasurable objective returns.
func writeTraceFloat(buffer *bytes.Buffer, name string, value float64) {
	buffer.WriteByte('"')
	buffer.WriteString(name)
	buffer.WriteString(`":`)

	if math.IsNaN(value) || math.IsInf(value, 0) {
		buffer.WriteString("null")

		return
	}

	encoded, err := json.Marshal(value)
	if err != nil {
		buffer.WriteString("null")

		return
	}

	buffer.Write(encoded)
}
