import type { CSSProperties, KeyboardEvent } from "react";

import mallet from "../../assets/mallet.svg";
import { centerPercent, computeNoteLayout, type BarEntry } from "../lib/layout";

const LAYOUT = computeNoteLayout();

export interface RackProps {
  onStrike: (note: number) => void;
  activeNotes: ReadonlySet<number>;
}

/** The bars, in their two lanes, over the wooden frame. */
export function Rack({ onStrike, activeNotes }: RackProps) {
  return (
    <section className="instrument-stage" aria-label="Playable glockenspiel">
      <div className="rack-wrap">
        <div className="rack-shadow" />
        <div className="rack">
          <div className="rail rail-sharp-back" />
          <div className="rail rail-sharp-front" />
          <div className="rail rail-natural-back" />
          <div className="rail rail-natural-front" />

          <div className="note-lane note-lane-sharps">
            {LAYOUT.accidentals.map((entry) => (
              <Bar
                key={entry.note}
                entry={entry}
                kind="accidental"
                active={activeNotes.has(entry.note)}
                onStrike={onStrike}
              />
            ))}
          </div>

          <div className="note-lane note-lane-naturals">
            {LAYOUT.naturals.map((entry) => (
              <Bar
                key={entry.note}
                entry={entry}
                kind="natural"
                active={activeNotes.has(entry.note)}
                onStrike={onStrike}
              />
            ))}
          </div>

          <img src={mallet} alt="" className="mallet" />
        </div>
      </div>
    </section>
  );
}

interface BarProps {
  entry: BarEntry;
  kind: "natural" | "accidental";
  active: boolean;
  onStrike: (note: number) => void;
}

/**
 * One bar.
 *
 * It listens for `pointerdown` so that a strike lands when the mallet does
 * rather than on mouse-up, and for Enter and Space so the keyboard reaches it
 * too. Both are prevented from their defaults: `pointerdown` to stop the drag
 * of a text selection across the rack, and the key events to stop the browser
 * synthesising a second `click` out of them, which would strike twice.
 */
function Bar({ entry, kind, active, onStrike }: BarProps) {
  return (
    <button
      type="button"
      className={`bar ${kind}${active ? " is-active" : ""}`}
      data-note={entry.note}
      aria-label={entry.name}
      style={
        {
          "--center": centerPercent(entry.center, LAYOUT.totalWhiteUnits),
          "--length": `${entry.length}px`,
        } as CSSProperties
      }
      onPointerDown={(event) => {
        event.preventDefault();
        onStrike(entry.note);
      }}
      onKeyDown={(event) => {
        if (isActivationKey(event)) {
          event.preventDefault();
          onStrike(entry.note);
        }
      }}
    >
      <span className="bar-note">{entry.name}</span>
      <span className="bar-key">{entry.keyHint}</span>
    </button>
  );
}

export function isActivationKey(event: KeyboardEvent<HTMLElement>): boolean {
  return event.key === "Enter" || event.key === " ";
}
