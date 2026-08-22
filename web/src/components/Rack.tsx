import type { CSSProperties, KeyboardEvent } from "react";

import mallet from "../../assets/mallet.svg";
import {
  centerPercent,
  computeBarGeometry,
  computeBarSupportGeometry,
  computeNoteLayout,
  type BarEntry,
  type BarKind,
  type BarSupportGeometry,
} from "../lib/layout";

const LAYOUT = computeNoteLayout();
const SUPPORTS = {
  accidental: computeBarSupportGeometry(
    LAYOUT.accidentals,
    "accidental",
    LAYOUT.totalWhiteUnits,
  ),
  natural: computeBarSupportGeometry(
    LAYOUT.naturals,
    "natural",
    LAYOUT.totalWhiteUnits,
  ),
};

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
          <div
            className="note-lane note-lane-sharps"
            style={
              {
                "--lane-height": `${SUPPORTS.accidental.laneHeight}px`,
              } as CSSProperties
            }
          >
            <RowSupport kind="accidental" geometry={SUPPORTS.accidental} />
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

          <div
            className="note-lane note-lane-naturals"
            style={
              {
                "--lane-height": `${SUPPORTS.natural.laneHeight}px`,
              } as CSSProperties
            }
          >
            <RowSupport kind="natural" geometry={SUPPORTS.natural} />
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
  kind: BarKind;
  active: boolean;
  onStrike: (note: number) => void;
}

/**
 * One bar.
 *
 * It listens for `pointerdown` so that a strike lands when the mallet does
 * rather than on mouse-up, and for Enter and Space so the keyboard reaches it
 * too. Mouse and pen pointer defaults are prevented to stop text selection;
 * touch pointers keep their default so the shared playfield can pan. The key
 * events are prevented to stop the browser synthesising a second `click`,
 * which would strike twice.
 */
function Bar({ entry, kind, active, onStrike }: BarProps) {
  const geometry = computeBarGeometry(entry, kind);

  return (
    <button
      type="button"
      className={`bar ${kind}${active ? " is-active" : ""}`}
      data-note={entry.note}
      data-baseline={geometry.baseline}
      data-mount-y={geometry.mountCenterY}
      aria-label={entry.name}
      style={
        {
          "--center": centerPercent(entry.center, LAYOUT.totalWhiteUnits),
          "--length": `${entry.length}px`,
          "--bar-top": `${geometry.top}px`,
          "--bar-width": `${geometry.width}px`,
        } as CSSProperties
      }
      onPointerDown={(event) => {
        // Let touch pointers start the playfield's horizontal pan. Mouse and
        // pen still suppress text selection and synthetic clicks.
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
      <span className="bar-note">{entry.name}</span>
      <span className="bar-key">{entry.keyHint}</span>
    </button>
  );
}

interface RowSupportProps {
  kind: BarKind;
  geometry: BarSupportGeometry;
}

/** One support follows the mounting-hole trajectory behind a complete row. */
function RowSupport({ kind, geometry }: RowSupportProps) {
  const points = geometry.points.map(({ x, y }) => `${x},${y}`).join(" ");

  return (
    <svg
      className={`row-support ${kind}`}
      data-support={kind}
      viewBox={`0 0 100 ${geometry.laneHeight}`}
      preserveAspectRatio="none"
      aria-hidden="true"
      focusable="false"
    >
      <polyline points={points} />
    </svg>
  );
}

export function isActivationKey(event: KeyboardEvent<HTMLElement>): boolean {
  return event.key === "Enter" || event.key === " ";
}
