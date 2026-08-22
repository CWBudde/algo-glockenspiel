// Geometry of the playable surface: which bars exist, which piano keys they
// line up with, and where each of them sits horizontally. Everything here is
// pure, so it can be computed once at module load and rendered by React
// without any DOM of its own.

export const FIRST_NOTE = 60; // C4
export const LAST_NOTE = 84; // C6
export const KEYBOARD_FIRST_NOTE = 36; // C2
export const KEYBOARD_LAST_NOTE = 96; // C7

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
