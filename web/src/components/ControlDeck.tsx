import { getWoodSpeciesOptions } from "../lib/wood";
import { Dial } from "./Dial";
import { StatusPanel } from "./StatusPanel";

const SPECIES = getWoodSpeciesOptions();

export interface ControlDeckProps {
  /** Master output level as a percentage, 10..100. */
  gain: number;
  onGainChange: (gain: number) => void;
  /** Strike velocity, 1..127, the MIDI range the engine takes. */
  velocity: number;
  onVelocityChange: (velocity: number) => void;
  species: string;
  onSpeciesChange: (species: string) => void;
  status: string;
  statusIsError: boolean;
}

/**
 * The controls that shape a performance, kept together above the instrument.
 *
 * The dials remain real range inputs and the wood choice remains owned by the
 * play route. This component only gives those controls one visual hierarchy
 * and keeps the engine's live announcement next to them.
 */
export function ControlDeck({
  gain,
  onGainChange,
  velocity,
  onVelocityChange,
  species,
  onSpeciesChange,
  status,
  statusIsError,
}: ControlDeckProps) {
  const selected = SPECIES.find((entry) => entry.id === species);

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

      <div className="control-deck-appearance">
        <label className="wood-field">
          <span>Wood</span>
          <select
            value={species}
            aria-describedby="wood-description"
            onChange={(event) => {
              onSpeciesChange(event.target.value);
            }}
          >
            {SPECIES.map((entry) => (
              <option key={entry.id} value={entry.id}>
                {entry.label}
              </option>
            ))}
          </select>
        </label>

        <p id="wood-description" className="wood-description">
          {selected?.description ?? ""}
        </p>
      </div>

      <div className="control-deck-status">
        <span className="status-label">Engine status</span>
        <StatusPanel message={status} error={statusIsError} />
      </div>
    </section>
  );
}
