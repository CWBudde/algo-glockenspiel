import { useLayoutEffect, useRef, type CSSProperties } from "react";

import { computePlayfieldLayout } from "../lib/layout";
import { Keyboard } from "./Keyboard";
import { Rack } from "./Rack";

const MOBILE_LAYOUT = computePlayfieldLayout();

export interface PlayfieldProps {
  onStrike: (note: number) => void;
  activeNotes: ReadonlySet<number>;
}

/**
 * Owns the rack and keyboard as one pitch-aligned surface. On narrow screens
 * the outer viewport is their only horizontal scroller; desktop keeps the
 * existing stacked layout.
 */
export function Playfield({ onStrike, activeNotes }: PlayfieldProps) {
  const viewportRef = useRef<HTMLDivElement>(null);
  const initializedMobileScroll = useRef(false);

  useLayoutEffect(() => {
    const viewport = viewportRef.current;
    if (viewport === null) {
      return;
    }

    const mobile = window.matchMedia("(max-width: 760px)");
    const initializeScroll = () => {
      if (!mobile.matches || initializedMobileScroll.current) {
        return;
      }

      viewport.scrollLeft = MOBILE_LAYOUT.initialScrollLeft;
      initializedMobileScroll.current = true;
    };

    initializeScroll();
    mobile.addEventListener("change", initializeScroll);

    return () => {
      mobile.removeEventListener("change", initializeScroll);
    };
  }, []);

  return (
    <div
      className="playfield-viewport"
      ref={viewportRef}
      style={
        {
          "--playfield-viewport-width": `${MOBILE_LAYOUT.viewportWidth}px`,
        } as CSSProperties
      }
    >
      <div
        className="playfield-track"
        style={
          {
            "--playfield-white-unit": `${MOBILE_LAYOUT.whiteUnitPx}px`,
            "--playfield-total-white-units": MOBILE_LAYOUT.totalWhiteUnits,
            "--playfield-rack-white-units": MOBILE_LAYOUT.rackWhiteUnits,
            "--playfield-rack-offset-white-units":
              MOBILE_LAYOUT.rackOffsetWhiteUnits,
          } as CSSProperties
        }
      >
        <div className="instrument-main">
          <Rack onStrike={onStrike} activeNotes={activeNotes} />
        </div>

        <Keyboard onStrike={onStrike} activeNotes={activeNotes} />
      </div>
    </div>
  );
}
