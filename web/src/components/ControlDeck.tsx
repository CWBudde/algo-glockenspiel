import { Dial } from "./Dial";
import { StatusPanel } from "./StatusPanel";

export interface ControlDeckProps {
  /** Master output level as a percentage, 10..100. */
  gain: number;
  onGainChange: (gain: number) => void;
  /** Strike velocity, 1..127, the MIDI range the engine takes. */
  velocity: number;
  onVelocityChange: (velocity: number) => void;
  status: string;
  statusIsError: boolean;
}

/**
 * The controls that shape a performance, kept together above the instrument.
 *
 * The dials remain real range inputs. This component gives them one visual
 * hierarchy and keeps the engine's live announcement next to them.
 */
export function ControlDeck({
  gain,
  onGainChange,
  velocity,
  onVelocityChange,
  status,
  statusIsError,
}: ControlDeckProps) {
  return (
    <section className="control-deck" aria-label="Performance controls">
      <Dial
        id="gain"
        label="Volume"
        value={gain}
        min={10}
        max={100}
        onChange={onGainChange}
        format={(value) => `${value}%`}
        small
      />

      <Dial
        id="velocity"
        label="Velocity"
        value={velocity}
        min={1}
        max={127}
        onChange={onVelocityChange}
        format={(value) => String(value)}
        small
      />

      <div className="control-deck-status">
        <span className="status-label">Engine status</span>
        <StatusPanel message={status} error={statusIsError} />
      </div>
    </section>
  );
}
