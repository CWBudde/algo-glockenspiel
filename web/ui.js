export const FIRST_NOTE = 60; // C4
export const LAST_NOTE = 84; // C6
export const KEYBOARD_FIRST_NOTE = 36; // C2
export const KEYBOARD_LAST_NOTE = 96; // C7
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

export function computeKeyboardLayout() {
  const whites = [];
  const blacks = [];
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

  return {
    whites,
    blacks,
    totalWhiteUnits: whiteIndex,
  };
}

function naturalLength(note) {
  const ratio = (note - FIRST_NOTE) / (LAST_NOTE - FIRST_NOTE);
  return Math.round(238 - ratio * 92);
}

function accidentalLength(note) {
  const ratio = (note - FIRST_NOTE) / (LAST_NOTE - FIRST_NOTE);
  return Math.round(178 - ratio * 64);
}

function centerPercent(xUnits, totalWhiteUnits) {
  return (xUnits / totalWhiteUnits) * 100;
}

export function buildUI({ naturalContainer, accidentalContainer, keyboardContainer, onStrike }) {
  const { naturals, accidentals } = computeNoteLayout();
  const keyboard = computeKeyboardLayout();
  const noteButtons = new Map();
  const pianoKeys = new Map();

  naturals.forEach((entry, index) => {
    const button = createBarButton(entry, "natural", KEY_BINDINGS[index] || "", onStrike, 15);
    naturalContainer.appendChild(button);
    noteButtons.set(entry.note, button);
  });

  accidentals.forEach((entry) => {
    const index = entry.note - FIRST_NOTE;
    const button = createBarButton(entry, "accidental", KEY_BINDINGS[index] || "", onStrike, 15);
    accidentalContainer.appendChild(button);
    noteButtons.set(entry.note, button);
  });

  keyboardContainer.style.setProperty("--keyboard-white-count", String(keyboard.totalWhiteUnits));

  keyboard.whites.forEach((entry) => {
    const key = createPianoKey(entry, "white", onStrike, keyboard.totalWhiteUnits);
    keyboardContainer.appendChild(key);
    pianoKeys.set(entry.note, key);
  });

  keyboard.blacks.forEach((entry) => {
    const key = createPianoKey(entry, "black", onStrike, keyboard.totalWhiteUnits);
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

function createBarButton(entry, kind, keyHint, onStrike, totalWhiteUnits) {
  const button = document.createElement("button");
  button.type = "button";
  button.className = `bar ${kind}`;
  button.dataset.note = String(entry.note);
  button.style.setProperty("--center", `${centerPercent(entry.center, totalWhiteUnits)}%`);
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

function createPianoKey(entry, kind, onStrike, totalWhiteUnits) {
  const key = document.createElement("button");
  key.type = "button";
  key.className = `piano-key ${kind}`;
  key.dataset.note = String(entry.note);
  if (kind === "black") {
    key.style.left = `${centerPercent(entry.center, totalWhiteUnits)}%`;
    key.style.transform = "translateX(-50%)";
  } else {
    key.style.left = `${centerPercent(entry.center - 0.5, totalWhiteUnits)}%`;
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
    if (KEY_BINDINGS[note - FIRST_NOTE]) {
      keyMap.set(KEY_BINDINGS[note - FIRST_NOTE], note);
    }
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
  const control = input.closest(".dial-control");
  const assembly = input.closest(".dial-assembly");
  const face = assembly?.querySelector("[data-dial-face]");
  const min = Number(input.min || 0);
  const max = Number(input.max || 100);

  const setValueFromRatio = (ratio) => {
    const clamped = Math.min(1, Math.max(0, ratio));
    const value = min + clamped * (max - min);
    input.value = String(Math.round(value));
    sync();
    input.dispatchEvent(new Event("input", { bubbles: true }));
  };

  const sync = () => {
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

  const applyPointer = (event) => {
    if (!face) return;
    const rect = face.getBoundingClientRect();
    const dx = event.clientX - (rect.left + rect.width / 2);
    const dy = event.clientY - (rect.top + rect.height / 2);
    const angle = Math.atan2(dy, dx) * 180 / Math.PI + 90;
    const wrapped = angle < -180 ? angle + 360 : angle;
    const clamped = Math.min(132, Math.max(-132, wrapped));
    setValueFromRatio((clamped + 132) / 264);
  };

  input.addEventListener("input", sync);
  face?.addEventListener("pointerdown", (event) => {
    event.preventDefault();
    face.setPointerCapture?.(event.pointerId);
    applyPointer(event);
  });
  face?.addEventListener("pointermove", (event) => {
    if ((event.buttons & 1) === 0) return;
    applyPointer(event);
  });
  control?.addEventListener("wheel", (event) => {
    event.preventDefault();
    const step = (max - min) / 80;
    const delta = event.deltaY < 0 ? step : -step;
    const next = Math.min(max, Math.max(min, Number(input.value) + delta));
    input.value = String(Math.round(next));
    sync();
    input.dispatchEvent(new Event("input", { bubbles: true }));
  }, { passive: false });
  sync();
}
