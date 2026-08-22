import type { CSSProperties } from "react";

import {
  centerPercent,
  computeKeyboardLayout,
  type KeyEntry,
} from "../lib/layout";
import { isActivationKey } from "./Rack";

const LAYOUT = computeKeyboardLayout();

export interface KeyboardProps {
  onStrike: (note: number) => void;
  activeNotes: ReadonlySet<number>;
}

/** The piano the rack is aligned against, C2 to C7. */
export function Keyboard({ onStrike, activeNotes }: KeyboardProps) {
  return (
    <section className="keyboard-panel" aria-label="Piano alignment">
      <div
        className="keyboard"
        style={
          {
            "--keyboard-white-count": LAYOUT.totalWhiteUnits,
          } as CSSProperties
        }
      >
        {LAYOUT.whites.map((entry) => (
          <PianoKey
            key={entry.note}
            entry={entry}
            kind="white"
            active={activeNotes.has(entry.note)}
            onStrike={onStrike}
          />
        ))}
        {LAYOUT.blacks.map((entry) => (
          <PianoKey
            key={entry.note}
            entry={entry}
            kind="black"
            active={activeNotes.has(entry.note)}
            onStrike={onStrike}
          />
        ))}
      </div>
    </section>
  );
}

interface PianoKeyProps {
  entry: KeyEntry;
  kind: "white" | "black";
  active: boolean;
  onStrike: (note: number) => void;
}

/**
 * One key.
 *
 * Only the C keys print their name, so every other key would otherwise be a
 * button with no accessible name at all -- 46 of the 61. The aria-label carries
 * the note name whether or not it is drawn.
 */
function PianoKey({ entry, kind, active, onStrike }: PianoKeyProps) {
  const style: CSSProperties =
    kind === "black"
      ? {
          left: centerPercent(entry.center, LAYOUT.totalWhiteUnits),
          transform: "translateX(-50%)",
        }
      : { left: centerPercent(entry.center - 0.5, LAYOUT.totalWhiteUnits) };

  // The visible label is the octave marker: C4, C5, and nothing in between.
  const label =
    kind === "white" && entry.name.startsWith("C") ? entry.name : "";

  return (
    <button
      type="button"
      className={`piano-key ${kind}${active ? " is-active" : ""}`}
      data-note={entry.note}
      aria-label={entry.name}
      style={style}
      onPointerDown={(event) => {
        if (event.pointerType !== "touch") {
          event.preventDefault();
        }
        onStrike(entry.note);
      }}
      onKeyDown={(event) => {
        if (isActivationKey(event)) {
          event.preventDefault();
          onStrike(entry.note);
        }
      }}
    >
      <span className="piano-note">{label}</span>
    </button>
  );
}
