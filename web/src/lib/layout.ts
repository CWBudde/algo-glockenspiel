// Geometry of the playable surface: which bars exist, which piano keys they
// line up with, and where each of them sits horizontally. Everything here is
// pure, so it can be computed once at module load and rendered by React
// without any DOM of its own.

export const FIRST_NOTE = 60; // C4
export const LAST_NOTE = 84; // C6
export const KEYBOARD_FIRST_NOTE = 36; // C2
export const KEYBOARD_LAST_NOTE = 96; // C7
export const MOBILE_WHITE_UNIT_PX = 44;
export const MOBILE_VIEWPORT_WHITE_UNITS = 7;
/** Fundamental-mode nodes of an ideal free-free bar, measured from each end. */
export const BAR_NODE_RATIO = 0.224;

export type BarKind = "natural" | "accidental";

const BAR_WIDTH_PX: Readonly<Record<BarKind, number>> = {
  natural: 32,
  accidental: 28,
};

const BAR_LANE_HEIGHT_PX: Readonly<Record<BarKind, number>> = {
  natural: 190,
  accidental: 142,
};

export const WHITE_OFFSETS: ReadonlySet<number> = new Set([
  0, 2, 4, 5, 7, 9, 11,
]);

export const KEY_BINDINGS: readonly string[] = [
  "A",
  "W",
  "S",
  "E",
  "D",
  "F",
  "T",
  "G",
  "Y",
  "H",
  "U",
  "J",
  "K",
  "O",
  "L",
  "P",
  ";",
  "'",
  "]",
  "\\",
  "Z",
  "X",
  "C",
  "V",
  "B",
];

const NOTE_NAMES: readonly string[] = [
  "C",
  "C#",
  "D",
  "D#",
  "E",
  "F",
  "F#",
  "G",
  "G#",
  "A",
  "A#",
  "B",
];

export function midiToName(note: number): string {
  const pitchClass = note % 12;
  const octave = Math.floor(note / 12) - 1;

  return `${NOTE_NAMES[pitchClass]}${octave}`;
}

/** One glockenspiel bar: where it sits and how long it is drawn. */
export interface BarEntry {
  note: number;
  name: string;
  /** Horizontal position in white-key units, counted from the left edge. */
  center: number;
  /** Drawn length in pixels; the bars taper as the pitch rises. */
  length: number;
  /** The computer-keyboard hint printed on the bar; empty when unbound. */
  keyHint: string;
}

export interface NoteLayout {
  naturals: readonly BarEntry[];
  accidentals: readonly BarEntry[];
  /**
   * How many white-key units the rack spans. The bars used to be positioned
   * against a hard-coded 15 while the keyboard used its computed count, so the
   * two alignments were only equal by coincidence and would have drifted apart
   * the moment the bar range moved.
   */
  totalWhiteUnits: number;
}

export interface BarGeometry {
  /** Constant visual width within one material row. */
  width: number;
  /** Top edge within the row lane. */
  top: number;
  /** Bottom edge within the row lane. */
  baseline: number;
  /** Centers of the two node-point mounting holes within the row lane. */
  mountCenterYs: readonly [number, number];
}

export interface BarSupportPoint {
  note: number;
  /** Horizontal position in the row's 0..100 SVG coordinate space. */
  x: number;
  /** Vertical position in lane pixels. */
  y: number;
}

export interface BarSupportGeometry {
  laneHeight: number;
  supports: readonly BarSupportLine[];
}

export interface BarSupportLine {
  position: "upper" | "lower";
  points: readonly BarSupportPoint[];
}

export function computeNoteLayout(): NoteLayout {
  const naturals: BarEntry[] = [];
  const accidentals: BarEntry[] = [];
  let whiteIndex = 0;

  for (let note = FIRST_NOTE; note <= LAST_NOTE; note += 1) {
    const pitchClass = note % 12;
    if (WHITE_OFFSETS.has(pitchClass)) {
      naturals.push({
        note,
        name: midiToName(note),
        center: whiteIndex + 0.5,
        length: naturalLength(note),
        keyHint: keyBindingFor(note),
      });
      whiteIndex += 1;
    } else {
      accidentals.push({
        note,
        name: midiToName(note),
        center: whiteIndex,
        length: accidentalLength(note),
        keyHint: keyBindingFor(note),
      });
    }
  }

  return { naturals, accidentals, totalWhiteUnits: whiteIndex };
}

/**
 * Draw geometry shared by the bar and the support behind its mount.
 *
 * Length still follows pitch, but width is a material property rather than an
 * accidental consequence of the SVG aspect ratio. Every bar is centered on
 * the same horizontal axis, so the top and bottom edges converge by equal
 * amounts and the complete row describes a symmetric trapezoid.
 */
export function computeBarGeometry(
  entry: BarEntry,
  kind: BarKind,
): BarGeometry {
  const centerY = BAR_LANE_HEIGHT_PX[kind] / 2;
  const top = centerY - entry.length / 2;
  const baseline = centerY + entry.length / 2;

  return {
    width: BAR_WIDTH_PX[kind],
    top,
    baseline,
    mountCenterYs: [
      top + entry.length * BAR_NODE_RATIO,
      top + entry.length * (1 - BAR_NODE_RATIO),
    ],
  };
}

/** Two coherent supports passing behind both node-point holes in a row. */
export function computeBarSupportGeometry(
  entries: readonly BarEntry[],
  kind: BarKind,
  totalWhiteUnits: number,
): BarSupportGeometry {
  const pointsForMount = (mountIndex: 0 | 1): readonly BarSupportPoint[] =>
    entries.map((entry) => ({
      note: entry.note,
      x: (entry.center / totalWhiteUnits) * 100,
      y: computeBarGeometry(entry, kind).mountCenterYs[mountIndex],
    }));

  return {
    laneHeight: BAR_LANE_HEIGHT_PX[kind],
    supports: [
      { position: "upper", points: pointsForMount(0) },
      { position: "lower", points: pointsForMount(1) },
    ],
  };
}

/** One piano key under the rack. Keys carry no length; the CSS sizes them. */
export interface KeyEntry {
  note: number;
  name: string;
  center: number;
}

export interface KeyboardLayout {
  whites: readonly KeyEntry[];
  blacks: readonly KeyEntry[];
  totalWhiteUnits: number;
}

export interface PlayfieldLayout {
  /** Width of one white-key pitch on the horizontally scrolling surface. */
  whiteUnitPx: number;
  /** Full C2-C7 keyboard span. */
  totalWhiteUnits: number;
  /** C4-C6 rack span. */
  rackWhiteUnits: number;
  /** White keys between the keyboard's C2 and the rack's C4. */
  rackOffsetWhiteUnits: number;
  /** Initial mobile scroll position, aligned to the rack's leading edge. */
  initialScrollLeft: number;
  /** Number of white-key pitches framed by the compact mobile viewport. */
  viewportWhiteUnits: number;
  /** Compact mobile viewport width in CSS pixels. */
  viewportWidth: number;
}

export function computeKeyboardLayout(): KeyboardLayout {
  const whites: KeyEntry[] = [];
  const blacks: KeyEntry[] = [];
  let whiteIndex = 0;

  for (let note = KEYBOARD_FIRST_NOTE; note <= KEYBOARD_LAST_NOTE; note += 1) {
    const pitchClass = note % 12;
    if (WHITE_OFFSETS.has(pitchClass)) {
      whites.push({
        note,
        name: midiToName(note),
        center: whiteIndex + 0.5,
      });
      whiteIndex += 1;
    } else {
      blacks.push({
        note,
        name: midiToName(note),
        center: whiteIndex,
      });
    }
  }

  return { whites, blacks, totalWhiteUnits: whiteIndex };
}

/**
 * Shared mobile pitch geometry for the rack and its full-range keyboard.
 * Keeping this derivation beside the note layouts prevents CSS offsets from
 * drifting when either range changes.
 */
export function computePlayfieldLayout(
  whiteUnitPx = MOBILE_WHITE_UNIT_PX,
  viewportWhiteUnits = MOBILE_VIEWPORT_WHITE_UNITS,
): PlayfieldLayout {
  const keyboard = computeKeyboardLayout();
  const rack = computeNoteLayout();
  const rackOffsetWhiteUnits = countWhiteNotes(KEYBOARD_FIRST_NOTE, FIRST_NOTE);

  return {
    whiteUnitPx,
    totalWhiteUnits: keyboard.totalWhiteUnits,
    rackWhiteUnits: rack.totalWhiteUnits,
    rackOffsetWhiteUnits,
    initialScrollLeft: rackOffsetWhiteUnits * whiteUnitPx,
    viewportWhiteUnits,
    viewportWidth: viewportWhiteUnits * whiteUnitPx,
  };
}

function countWhiteNotes(firstNote: number, lastNoteExclusive: number): number {
  let count = 0;

  for (let note = firstNote; note < lastNoteExclusive; note += 1) {
    if (WHITE_OFFSETS.has(note % 12)) {
      count += 1;
    }
  }

  return count;
}

function naturalLength(note: number): number {
  const ratio = (note - FIRST_NOTE) / (LAST_NOTE - FIRST_NOTE);

  return Math.round(190 - ratio * 74);
}

function accidentalLength(note: number): number {
  const ratio = (note - FIRST_NOTE) / (LAST_NOTE - FIRST_NOTE);

  return Math.round(142 - ratio * 51);
}

export function centerPercent(xUnits: number, totalWhiteUnits: number): string {
  return `${(xUnits / totalWhiteUnits) * 100}%`;
}

/**
 * keyBindingFor maps a bar to its computer-keyboard hint. The index is the
 * semitone offset from the first bar, so the accidentals share the run with the
 * naturals rather than having a table of their own.
 */
export function keyBindingFor(note: number): string {
  return KEY_BINDINGS[note - FIRST_NOTE] ?? "";
}

/** The computer-keyboard letter to MIDI note map, keyed by upper-case letter. */
export function computeKeyMap(): ReadonlyMap<string, number> {
  const keyMap = new Map<string, number>();

  for (let note = FIRST_NOTE; note <= LAST_NOTE; note += 1) {
    const binding = keyBindingFor(note);
    if (binding) {
      keyMap.set(binding, note);
    }
  }

  return keyMap;
}
