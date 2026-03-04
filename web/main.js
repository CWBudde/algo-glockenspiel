import { bindDial, buildUI, wireKeyboard } from "./ui.js";

let audioContext = null;
let outputNode = null;
let wasmMemory = null;
let wasmReady = false;
let audioReady = false;
let initAudioPromise = null;
let masterGain = 0.7;
let strikeVelocity = 96;
let ui = null;

function updateStatus(message, isError = false) {
  const status = document.getElementById("status");
  status.textContent = message;
  status.dataset.error = isError ? "true" : "false";
}

function clamp(value, min, max) {
  return Math.min(max, Math.max(min, value));
}

async function initAudio() {
  if (audioReady) return;
  if (initAudioPromise) return initAudioPromise;

  initAudioPromise = (async () => {
    audioContext = new (window.AudioContext || window.webkitAudioContext)();

    const initError = wasmInit(audioContext.sampleRate);
    if (typeof initError === "string" && initError.length > 0) {
      throw new Error(initError);
    }

    outputNode = audioContext.createScriptProcessor(512, 0, 2);
    outputNode.onaudioprocess = (event) => {
      const output = event.outputBuffer;
      const left = output.getChannelData(0);
      const right = output.getChannelData(1);

      left.fill(0);
      right.fill(0);

      if (!wasmMemory || typeof wasmProcessBlock === "undefined") {
        return;
      }

      const interleavedPtr = wasmProcessBlock(left.length);
      if (!interleavedPtr) {
        return;
      }

      const interleaved = new Float32Array(
        wasmMemory.buffer,
        Number(interleavedPtr),
        left.length * 2,
      );

      for (let frame = 0; frame < left.length; frame += 1) {
        left[frame] = interleaved[frame * 2];
        right[frame] = interleaved[frame * 2 + 1];
      }
    };

    outputNode.connect(audioContext.destination);
    await audioContext.resume();

    if (typeof wasmSetMasterGain !== "undefined") {
      wasmSetMasterGain(masterGain);
    }

    audioReady = true;
    updateStatus(`Ready at ${Math.round(audioContext.sampleRate)} Hz`);
  })();

  try {
    await initAudioPromise;
  } finally {
    initAudioPromise = null;
  }
}

function strike(note) {
  if (!wasmReady || !ui) {
    return;
  }

  const start = () => {
    ui.activateNote(note);
    if (typeof wasmNoteOn !== "undefined") {
      wasmNoteOn(note, strikeVelocity);
    }
  };

  if (!audioReady) {
    initAudio().then(start).catch((error) => {
      console.error(error);
      updateStatus(error.message, true);
    });
    return;
  }

  start();
}

function bindControls() {
  const velocity = document.getElementById("velocity");
  const velocityValue = document.getElementById("velocity-value");
  const gain = document.getElementById("gain");
  const gainValue = document.getElementById("gain-value");

  bindDial(velocity, velocityValue, (value) => String(value));
  bindDial(gain, gainValue, (value) => `${value}%`);

  velocity.addEventListener("input", () => {
    strikeVelocity = clamp(Number(velocity.value), 1, 127);
  });

  gain.addEventListener("input", () => {
    masterGain = clamp(Number(gain.value) / 100, 0.1, 1.0);
    if (audioReady && typeof wasmSetMasterGain !== "undefined") {
      wasmSetMasterGain(masterGain);
    }
  });
}

async function init() {
  try {
    ui = buildUI({
      naturalContainer: document.getElementById("bars-natural"),
      accidentalContainer: document.getElementById("bars-accidental"),
      keyboardContainer: document.getElementById("piano"),
      onStrike: strike,
    });

    wireKeyboard({
      onStrike: strike,
      activateNote: ui.activateNote,
    });
    bindControls();

    const go = new Go();
    const response = await fetch("dist/glockenspiel.wasm");
    if (!response.ok) {
      throw new Error(`Failed to fetch WASM: ${response.status}`);
    }

    let result;
    try {
      result = await WebAssembly.instantiateStreaming(response.clone(), go.importObject);
    } catch (_streamingError) {
      const bytes = await response.arrayBuffer();
      result = await WebAssembly.instantiate(bytes, go.importObject);
    }

    wasmMemory = result.instance.exports.mem || result.instance.exports.memory || null;
    if (!wasmMemory) {
      throw new Error("WASM memory export not found");
    }

    go.run(result.instance);
    await new Promise((resolve) => window.setTimeout(resolve, 50));

    if (
      typeof wasmInit === "undefined" ||
      typeof wasmNoteOn === "undefined" ||
      typeof wasmProcessBlock === "undefined"
    ) {
      throw new Error("WASM exports not found");
    }

    wasmReady = true;
    updateStatus("WASM loaded. Strike a bar to start audio.");
  } catch (error) {
    console.error("Failed to load WASM demo", error);
    updateStatus(error.message, true);
  }
}

window.addEventListener("load", init);
