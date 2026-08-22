import { Dial } from "./Dial";
import { StatusPanel } from "./StatusPanel";

export interface ControlRailProps {
  /** Master output level as a percentage, 10..100. */
  gain: number;
  onGainChange: (gain: number) => void;
  /** Strike velocity, 1..127, the MIDI range the engine takes. */
  velocity: number;
  onVelocityChange: (velocity: number) => void;
  status: string;
  statusIsError: boolean;
}

export function ControlRail({
  gain,
  onGainChange,
  velocity,
  onVelocityChange,
  status,
  statusIsError,
}: ControlRailProps) {
  return (
    <aside className="control-rail" aria-label="Performance controls">
      <Dial
        id="gain"
        label="Volume"
        value={gain}
        min={10}
        max={100}
        onChange={onGainChange}
        format={(value) => `${value}%`}
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

      <StatusPanel message={status} error={statusIsError} />
    </aside>
  );
}
