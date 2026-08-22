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
import { useStrikePointer } from "../lib/strike-pointer";

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
            <RowSupports kind="accidental" geometry={SUPPORTS.accidental} />
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
            <RowSupports kind="natural" geometry={SUPPORTS.natural} />
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
 * Pointer strikes go through `useStrikePointer`, so a mouse or pen strikes as
 * the mallet lands while a touch waits to prove it is a tap and not a pan of
 * the playfield. Enter and Space reach it from the keyboard; their default is
 * prevented to stop the browser synthesising a second `click`, which would
 * strike twice.
 */
function Bar({ entry, kind, active, onStrike }: BarProps) {
  const geometry = computeBarGeometry(entry, kind);
  const strikeHandlers = useStrikePointer(entry.note, onStrike);

  return (
    <button
      type="button"
      className={`bar ${kind}${active ? " is-active" : ""}`}
      data-note={entry.note}
      data-baseline={geometry.baseline}
      aria-label={entry.name}
      style={
        {
          "--center": centerPercent(entry.center, LAYOUT.totalWhiteUnits),
          "--length": `${entry.length}px`,
          "--bar-top": `${geometry.top}px`,
          "--bar-width": `${geometry.width}px`,
          "--mount-upper": `${geometry.mountCenterYs[0] - geometry.top}px`,
          "--mount-lower": `${geometry.mountCenterYs[1] - geometry.top}px`,
        } as CSSProperties
      }
      {...strikeHandlers}
      onKeyDown={(event) => {
        if (isActivationKey(event)) {
          event.preventDefault();
          onStrike(entry.note);
        }
      }}
    >
      <span
        className="bar-mount"
        data-mount-position="upper"
        aria-hidden="true"
      />
      <span
        className="bar-mount"
        data-mount-position="lower"
        aria-hidden="true"
      />
      <span className="bar-note">{entry.name}</span>
      <span className="bar-key">{entry.keyHint}</span>
    </button>
  );
}

interface RowSupportProps {
  kind: BarKind;
  geometry: BarSupportGeometry;
}

/** Two supports follow the node-point mounting holes behind a complete row. */
function RowSupports({ kind, geometry }: RowSupportProps) {
  return (
    <>
      {geometry.supports.map((support) => (
        <svg
          key={support.position}
          className={`row-support ${kind}`}
          data-support={kind}
          data-mount-position={support.position}
          viewBox={`0 0 100 ${geometry.laneHeight}`}
          preserveAspectRatio="none"
          aria-hidden="true"
          focusable="false"
        >
          <polyline
            points={support.points.map(({ x, y }) => `${x},${y}`).join(" ")}
          />
        </svg>
      ))}
    </>
  );
}

export function isActivationKey(event: KeyboardEvent<HTMLElement>): boolean {
  return event.key === "Enter" || event.key === " ";
}
