import { useRef } from "react";
import type { PointerEvent as ReactPointerEvent } from "react";

/** A touch that travels further than this is a pan of the playfield, not a tap. */
const TOUCH_DRAG_THRESHOLD_PX = 10;

export interface StrikePointerHandlers {
  onPointerDown: (event: ReactPointerEvent<HTMLElement>) => void;
  onPointerMove: (event: ReactPointerEvent<HTMLElement>) => void;
  onPointerUp: (event: ReactPointerEvent<HTMLElement>) => void;
  onPointerCancel: (event: ReactPointerEvent<HTMLElement>) => void;
}

/**
 * Pointer handlers that strike a note.
 *
 * Mouse and pen strike on `pointerdown`, so the note lands when the mallet
 * does, and their default is prevented to stop the labels being selected. A
 * touch cannot be judged that early: on the mobile playfield the same gesture
 * may be a horizontal pan, so the strike is held until `pointerup` and dropped
 * once the finger has moved past the drag threshold.
 */
export function useStrikePointer(
  note: number,
  onStrike: (note: number) => void,
): StrikePointerHandlers {
  const pending = useRef<{ id: number; x: number; y: number } | null>(null);

  return {
    onPointerDown(event) {
      if (event.pointerType !== "touch") {
        event.preventDefault();
        onStrike(note);
        return;
      }
      pending.current = {
        id: event.pointerId,
        x: event.clientX,
        y: event.clientY,
      };
    },
    onPointerMove(event) {
      const start = pending.current;
      if (!start || start.id !== event.pointerId) {
        return;
      }
      const travelled = Math.hypot(
        event.clientX - start.x,
        event.clientY - start.y,
      );
      if (travelled > TOUCH_DRAG_THRESHOLD_PX) {
        pending.current = null;
      }
    },
    onPointerUp(event) {
      const start = pending.current;
      if (!start || start.id !== event.pointerId) {
        return;
      }
      pending.current = null;
      onStrike(note);
    },
    onPointerCancel(event) {
      if (pending.current?.id === event.pointerId) {
        pending.current = null;
      }
    },
  };
}
