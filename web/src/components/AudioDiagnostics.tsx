import type { AudioDiagnostics as Consumer } from "../audio/useAudioEngine";
import type { ProducerStats } from "../audio/protocol";

/**
 * The transport's own numbers, shown on `?debug=audio`.
 *
 * It exists because "dropouts, but the CPU is idle" is not a question the
 * dropout counter can answer. A starved queue has two possible causes with
 * opposite fixes -- the host drains faster than the producer is given a chance
 * to refill, or the producer is genuinely late -- and they are indistinguishable
 * from the output. The three numbers that separate them are the consumer's
 * burst length, the queue's low-water mark, and the producer's wake-up jitter,
 * so those are the ones on the panel.
 */
export interface AudioDiagnosticsProps {
  consumer: Consumer;
  producer: ProducerStats | null;
}

function ms(frames: number, sampleRate: number): string {
  if (sampleRate <= 0) {
    return "-";
  }

  return `${((frames / sampleRate) * 1000).toFixed(1)} ms`;
}

function seconds(value: number): string {
  return `${(value * 1000).toFixed(1)} ms`;
}

function spread(stats: { p50: number; p99: number; max: number }): string {
  return `${stats.p50.toFixed(2)} / ${stats.p99.toFixed(2)} / ${stats.max.toFixed(2)} ms`;
}

export function AudioDiagnostics({
  consumer,
  producer,
}: AudioDiagnosticsProps) {
  const { stats, latency, transport, sampleRate } = consumer;

  const rows: [string, string][] = [
    ["Transport", transport],
    [
      "Host buffer",
      latency
        ? `base ${seconds(latency.base)}, output ${seconds(latency.output)}`
        : "-",
    ],
    // The headline pair. A burst above 1 means the host asks for several render
    // quanta at once, and the queue has to be deeper than the burst or every
    // burst ends in silence however prompt the producer is.
    ["Callback burst", stats ? `${stats.maxBurst} quanta` : "-"],
    [
      "Credit target",
      stats
        ? `${stats.target} blocks (${ms(stats.target * 128, sampleRate)})`
        : "-",
    ],
    [
      "Queue low-water",
      stats
        ? `${stats.minDepth} frames (${ms(stats.minDepth, sampleRate)})`
        : "-",
    ],
    [
      "Queue now",
      stats ? `${stats.depth} frames (${ms(stats.depth, sampleRate)})` : "-",
    ],
    ["Dropouts", stats ? `${stats.underruns}` : "-"],
    ["Producer wake-up", producer ? spread(producer.wakeup) : "-"],
    [
      "Render load",
      producer
        ? `${(producer.load * 100).toFixed(0)}% of realtime (${producer.blocks} blocks)`
        : "-",
    ],
    ["Silent blocks", producer ? `${producer.silentBlocks}` : "-"],
  ];

  return (
    <div className="audio-diagnostics">
      <h2>Audio transport</h2>
      <dl>
        {rows.map(([label, value]) => (
          <div key={label}>
            <dt>{label}</dt>
            <dd>{value}</dd>
          </div>
        ))}
      </dl>
    </div>
  );
}
