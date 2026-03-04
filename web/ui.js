export const FIRST_NOTE = 60; // C4
export const LAST_NOTE = 84; // C6
export const TOTAL_WHITE_UNITS = 15;
export const WHITE_OFFSETS = new Set([0, 2, 4, 5, 7, 9, 11]);
export const KEY_BINDINGS = [
  "A", "W", "S", "E", "D", "F", "T", "G", "Y", "H", "U", "J",
  "K", "O", "L", "P", ";", "'", "]", "\\", "Z", "X", "C", "V", "B",
];

const NOTE_NAMES = ["C", "C#", "D", "D#", "E", "F", "F#", "G", "G#", "A", "A#", "B"];

export function midiToName(note) {
  const pitchClass = note % 12;
  const octave = Math.floor(note / 12) - 1;
  return `${NOTE_NAMES[pitchClass]}${octave}`;
}

export function computeNoteLayout() {
  const naturals = [];
  const accidentals = [];
  let whiteIndex = 0;

  for (let note = FIRST_NOTE; note <= LAST_NOTE; note += 1) {
    const pitchClass = note % 12;
    if (WHITE_OFFSETS.has(pitchClass)) {
      naturals.push({
        note,
        name: midiToName(note),
        center: whiteIndex + 0.5,
        length: naturalLength(note),
      });
      whiteIndex += 1;
    } else {
      accidentals.push({
        note,
        name: midiToName(note),
        center: whiteIndex,
        length: accidentalLength(note),
      });
    }
  }

  return { naturals, accidentals };
}

function naturalLength(note) {
  const ratio = (note - FIRST_NOTE) / (LAST_NOTE - FIRST_NOTE);
  return Math.round(238 - ratio * 92);
}

function accidentalLength(note) {
  const ratio = (note - FIRST_NOTE) / (LAST_NOTE - FIRST_NOTE);
  return Math.round(178 - ratio * 64);
}

function centerPercent(xUnits) {
  return (xUnits / TOTAL_WHITE_UNITS) * 100;
}

export function buildUI({ naturalContainer, accidentalContainer, keyboardContainer, onStrike }) {
  const { naturals, accidentals } = computeNoteLayout();
  const noteButtons = new Map();
  const pianoKeys = new Map();

  naturals.forEach((entry, index) => {
    const button = createBarButton(entry, "natural", KEY_BINDINGS[index] || "", onStrike);
    naturalContainer.appendChild(button);
    noteButtons.set(entry.note, button);
  });

  accidentals.forEach((entry) => {
    const index = entry.note - FIRST_NOTE;
    const button = createBarButton(entry, "accidental", KEY_BINDINGS[index] || "", onStrike);
    accidentalContainer.appendChild(button);
    noteButtons.set(entry.note, button);
  });

  naturals.forEach((entry) => {
    const key = createPianoKey(entry, "white", onStrike);
    keyboardContainer.appendChild(key);
    pianoKeys.set(entry.note, key);
  });

  accidentals.forEach((entry) => {
    const key = createPianoKey(entry, "black", onStrike);
    keyboardContainer.appendChild(key);
    pianoKeys.set(entry.note, key);
  });

  return {
    noteButtons,
    pianoKeys,
    activateNote(note, duration = 180) {
      const button = noteButtons.get(note);
      const key = pianoKeys.get(note);
      [button, key].forEach((element) => {
        if (!element) return;
        element.classList.add("is-active");
        window.clearTimeout(element._activeTimer);
        element._activeTimer = window.setTimeout(() => {
          element.classList.remove("is-active");
        }, duration);
      });
    },
  };
}

function createBarButton(entry, kind, keyHint, onStrike) {
  const button = document.createElement("button");
  button.type = "button";
  button.className = `bar ${kind}`;
  button.dataset.note = String(entry.note);
  button.style.setProperty("--center", `${centerPercent(entry.center)}%`);
  button.style.setProperty("--length", `${entry.length}px`);

  const note = document.createElement("span");
  note.className = "bar-note";
  note.textContent = entry.name;

  const hint = document.createElement("span");
  hint.className = "bar-key";
  hint.textContent = keyHint;

  button.append(note, hint);
  button.addEventListener("pointerdown", (event) => {
    event.preventDefault();
    onStrike(entry.note);
  });

  return button;
}

function createPianoKey(entry, kind, onStrike) {
  const key = document.createElement("button");
  key.type = "button";
  key.className = `piano-key ${kind}`;
  key.dataset.note = String(entry.note);
  key.style.left = `${leftPercent(entry.x)}%`;
  if (kind === "black") {
    key.style.transform = "translateX(-50%)";
  }

  const label = document.createElement("span");
  label.className = "piano-note";
  label.textContent = entry.name;
  if (!entry.name.startsWith("C") && kind === "white") {
    label.textContent = "";
  }

  key.append(label);
  key.addEventListener("pointerdown", (event) => {
    event.preventDefault();
    onStrike(entry.note);
  });

  return key;
}

export function wireKeyboard({ onStrike, activateNote }) {
  const pressed = new Set();
  const keyMap = new Map();
  for (let note = FIRST_NOTE; note <= LAST_NOTE; note += 1) {
    keyMap.set(KEY_BINDINGS[note - FIRST_NOTE], note);
  }

  document.addEventListener("keydown", (event) => {
    if (event.repeat) {
      return;
    }

    const key = event.key.toUpperCase();
    const note = keyMap.get(key);
    if (note === undefined || pressed.has(key)) {
      return;
    }

    pressed.add(key);
    activateNote(note);
    onStrike(note);
  });

  document.addEventListener("keyup", (event) => {
    pressed.delete(event.key.toUpperCase());
  });
}

export function bindDial(input, output, formatter) {
  const assembly = input.closest(".dial-control");
  const face = assembly?.querySelector("[data-dial-face]");

  const sync = () => {
    const min = Number(input.min || 0);
    const max = Number(input.max || 100);
    const value = Number(input.value);
    const ratio = (value - min) / (max - min || 1);
    const turn = -132 + ratio * 264;
    if (face) {
      face.style.setProperty("--turn", `${turn}deg`);
    }
    if (output) {
      output.textContent = formatter(value);
    }
  };

  input.addEventListener("input", sync);
  sync();
}
