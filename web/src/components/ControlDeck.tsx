import { SOUND_PRESETS } from "../api/presets.generated";
import type { FittedPreset } from "../lib/fittedPreset";
import { Dial } from "./Dial";
import { StatusPanel } from "./StatusPanel";

export interface ControlDeckProps {
  /** Master output level as a percentage, 10..100. */
  gain: number;
  onGainChange: (gain: number) => void;
  /** The id of the sound the engine plays: a built-in one, or a fitted one. */
  presetId: string;
  onPresetChange: (presetId: string) => void;
  /**
   * Sounds the Optimize tab produced during this session. They are listed
   * apart from the built-ins because they are not the same kind of thing: a
   * built-in ships with the app, and these live until the page is reloaded.
   */
  fittedPresets: readonly FittedPreset[];
  /** Strike velocity, 1..127, the MIDI range the engine takes. */
  velocity: number;
  onVelocityChange: (velocity: number) => void;
  /** Reverb mix as a percentage, 0..100; the engine takes 0..1. */
  reverb: number;
  onReverbChange: (reverb: number) => void;
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
  fittedPresets,
  velocity,
  onVelocityChange,
  reverb,
  onReverbChange,
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

          {fittedPresets.length > 0 && (
            <optgroup label="Fitted this session">
              {fittedPresets.map((preset) => (
                <option key={preset.id} value={preset.id}>
                  {preset.label}
                </option>
              ))}
            </optgroup>
          )}
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

      <Dial
        id="reverb"
        label="Reverb"
        value={reverb}
        min={0}
        max={100}
        onChange={onReverbChange}
        format={(value) => `${value}%`}
        small
      />

      <div className="control-deck-status">
        <span className="status-label">Engine status</span>
        <StatusPanel message={status} error={statusIsError} />
      </div>
    </section>
  );
}
