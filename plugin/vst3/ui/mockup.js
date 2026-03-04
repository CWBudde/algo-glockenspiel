import { bindDial, buildUI, wireKeyboard } from "../../../web/ui.js";

function strike(note) {
  ui.activateNote(note, 220);
}

const ui = buildUI({
  naturalContainer: document.getElementById("bars-natural"),
  accidentalContainer: document.getElementById("bars-accidental"),
  keyboardContainer: document.getElementById("piano"),
  onStrike: strike,
});

wireKeyboard({
  onStrike: strike,
  activateNote: ui.activateNote,
});

bindDial(
  document.getElementById("velocity"),
  document.getElementById("velocity-value"),
  (value) => String(value),
);

bindDial(
  document.getElementById("gain"),
  document.getElementById("gain-value"),
  (value) => `${value}%`,
);
