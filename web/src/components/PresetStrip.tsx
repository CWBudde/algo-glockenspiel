import { getWoodSpeciesOptions } from "../lib/wood";

const SPECIES = getWoodSpeciesOptions();

export interface PresetStripProps {
  species: string;
  onSpeciesChange: (species: string) => void;
}

/**
 * The strip under the masthead.
 *
 * It used to hold a preset `<select>` with one hard-coded option, a Save button
 * and a Load button, all three disabled or inert, plus a note apologising for
 * them. A control that cannot do what it says is worse than no control, so only
 * the wood species -- which works -- is left, and it now carries its own
 * description rather than a placeholder.
 */
export function PresetStrip({ species, onSpeciesChange }: PresetStripProps) {
  const selected = SPECIES.find((entry) => entry.id === species);

  return (
    <section className="preset-strip" aria-label="Instrument appearance">
      <label className="preset-field">
        <span>Wood</span>
        <select
          value={species}
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

      <p className="preset-note">{selected?.description ?? ""}</p>
    </section>
  );
}
