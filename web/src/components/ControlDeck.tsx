import { SOUND_PRESETS } from "../api/presets.generated";
import { Dial } from "./Dial";
import { StatusPanel } from "./StatusPanel";

export interface ControlDeckProps {
  /** Master output level as a percentage, 10..100. */
  gain: number;
  onGainChange: (gain: number) => void;
  /** The id of the built-in sound the engine plays. */
  presetId: string;
  onPresetChange: (presetId: string) => void;
  /** Strike velocity, 1..127, the MIDI range the engine takes. */
  velocity: number;
  onVelocityChange: (velocity: number) => void;
  status: string;
  statusIsError: boolean;
}

/**
 * The controls that shape a performance, kept together above the instrument.
 *
 * The dials remain real range inputs, and the sound picker is a real select.
 * This component gives them one visual hierarchy and keeps the engine's live
 * announcement next to them.
 *
 * The options come from a generated table rather than from the engine. The deck
 * renders long before the WebAssembly module finishes loading, and on a static
 * host there is no service to ask, so a picker fed from the engine would be
 * empty exactly when someone first looks at it.
 */
export function ControlDeck({
  gain,
  onGainChange,
  presetId,
  onPresetChange,
  velocity,
  onVelocityChange,
  status,
  statusIsError,
}: ControlDeckProps) {
  return (
    <section className="control-deck" aria-label="Performance controls">
      <div className="deck-field">
        <label htmlFor="sound-preset">Sound</label>
        <select
          id="sound-preset"
          value={presetId}
          onChange={(event) => {
            onPresetChange(event.target.value);
          }}
        >
          {SOUND_PRESETS.map((preset) => (
            <option key={preset.id} value={preset.id}>
              {preset.label}
            </option>
          ))}
        </select>
      </div>

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
