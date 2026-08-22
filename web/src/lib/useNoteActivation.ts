import { useCallback, useEffect, useRef, useState } from "react";

/** How long a struck bar stays lit, in milliseconds. */
const ACTIVE_MS = 180;

export interface NoteActivation {
  activeNotes: ReadonlySet<number>;
  activate: (note: number) => void;
}

/**
 * Tracks which notes are currently lit.
 *
 * web/ui.js did this by adding a class and hanging the clearing timer off the
 * element as `element._activeTimer`. Here the set is state and the timers live
 * in a ref, so that a re-strike restarts one note's timer without disturbing
 * the others and nothing survives an unmount.
 */
export function useNoteActivation(): NoteActivation {
  const [activeNotes, setActiveNotes] = useState<ReadonlySet<number>>(
    () => new Set(),
  );
  const timers = useRef(new Map<number, number>());

  const activate = useCallback((note: number) => {
    const running = timers.current.get(note);
    if (running !== undefined) {
      window.clearTimeout(running);
    }

    setActiveNotes((previous) => {
      if (previous.has(note)) {
        return previous;
      }

      const next = new Set(previous);
      next.add(note);

      return next;
    });

    timers.current.set(
      note,
      window.setTimeout(() => {
        timers.current.delete(note);
        setActiveNotes((previous) => {
          const next = new Set(previous);
          next.delete(note);

          return next;
        });
      }, ACTIVE_MS),
    );
  }, []);

  useEffect(() => {
    const pending = timers.current;

    return () => {
      for (const timer of pending.values()) {
        window.clearTimeout(timer);
      }

      pending.clear();
    };
  }, []);

  return { activeNotes, activate };
}
