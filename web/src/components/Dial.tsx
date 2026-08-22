import {
  useCallback,
  useEffect,
  useRef,
  type CSSProperties,
  type PointerEvent,
} from "react";

/** How far the indicator travels either side of centre, in degrees. */
const SWEEP_DEGREES = 132;

/** One wheel notch moves the range by this fraction of its span. */
const WHEEL_STEPS = 80;

export interface DialProps {
  id: string;
  label: string;
  value: number;
  min: number;
  max: number;
  onChange: (value: number) => void;
  format: (value: number) => string;
  /** Renders the smaller of the two dial sizes. */
  small?: boolean;
}

/**
 * A knob drawn over a real `<input type="range">`.
 *
 * The range input is the control: it is what a screen reader announces and what
 * the arrow keys move. The visible face is a sibling that reads the value and
 * turns, and that translates pointer and wheel gestures back into it.
 *
 * The port from web/ui.js's `bindDial` is not mechanical. That version wrote
 * `input.value` and re-dispatched a synthetic `input` event so its own listener
 * would pick the change up again -- a loop a controlled component cannot have,
 * because the DOM value is a function of the React value. Every gesture here
 * calls `onChange` directly instead, and the input renders whatever comes back.
 */
export function Dial({
  id,
  label,
  value,
  min,
  max,
  onChange,
  format,
  small = false,
}: DialProps) {
  const faceRef = useRef<HTMLDivElement | null>(null);
  const controlRef = useRef<HTMLLabelElement | null>(null);
  const rangeRef = useRef({ min, max });
  const valueRef = useRef(value);

  // The wheel handler below is created once per onChange identity, so it needs
  // the live value and bounds rather than the ones its closure captured.
  useEffect(() => {
    rangeRef.current = { min, max };
    valueRef.current = value;
  });

  const span = max - min || 1;
  const turn = -SWEEP_DEGREES + ((value - min) / span) * (SWEEP_DEGREES * 2);

  const emitRatio = useCallback(
    (ratio: number) => {
      const bounds = rangeRef.current;
      const clamped = Math.min(1, Math.max(0, ratio));

      onChange(Math.round(bounds.min + clamped * (bounds.max - bounds.min)));
    },
    [onChange],
  );

  const applyPointer = useCallback(
    (event: PointerEvent<HTMLDivElement>) => {
      const face = faceRef.current;
      if (!face) {
        return;
      }

      const rect = face.getBoundingClientRect();
      const dx = event.clientX - (rect.left + rect.width / 2);
      const dy = event.clientY - (rect.top + rect.height / 2);
      const angle = (Math.atan2(dy, dx) * 180) / Math.PI + 90;
      const wrapped = angle < -180 ? angle + 360 : angle;
      const clamped = Math.min(
        SWEEP_DEGREES,
        Math.max(-SWEEP_DEGREES, wrapped),
      );

      emitRatio((clamped + SWEEP_DEGREES) / (SWEEP_DEGREES * 2));
    },
    [emitRatio],
  );

  // The wheel listener has to be attached by hand: React attaches its own
  // wheel handler passively, and a passive listener may not call
  // preventDefault, so the page would scroll while the knob turns.
  useEffect(() => {
    const control = controlRef.current;
    if (!control) {
      return;
    }

    const onWheel = (event: WheelEvent) => {
      event.preventDefault();

      const bounds = rangeRef.current;
      const step = (bounds.max - bounds.min) / WHEEL_STEPS;
      const delta = event.deltaY < 0 ? step : -step;

      onChange(
        Math.round(
          Math.min(bounds.max, Math.max(bounds.min, valueRef.current + delta)),
        ),
      );
    };

    control.addEventListener("wheel", onWheel, { passive: false });

    return () => {
      control.removeEventListener("wheel", onWheel);
    };
  }, [onChange]);

  return (
    <label className="dial-control" htmlFor={id} ref={controlRef}>
      <span className="dial-label">{label}</span>
      <div
        className={
          small ? "dial-assembly dial-assembly-small" : "dial-assembly"
        }
      >
        <input
          id={id}
          className="dial-input"
          type="range"
          min={min}
          max={max}
          value={value}
          onChange={(event) => {
            onChange(Number(event.target.value));
          }}
        />
        <div
          className="dial-face dial-face-aged-brass"
          ref={faceRef}
          style={{ "--turn": `${turn}deg` } as CSSProperties}
          onPointerDown={(event) => {
            event.preventDefault();
            faceRef.current?.setPointerCapture(event.pointerId);
            applyPointer(event);
          }}
          onPointerMove={(event) => {
            if ((event.buttons & 1) === 0) {
              return;
            }

            applyPointer(event);
          }}
        >
          <div className="dial-indicator" />
        </div>
      </div>
      <output htmlFor={id}>{format(value)}</output>
    </label>
  );
}
