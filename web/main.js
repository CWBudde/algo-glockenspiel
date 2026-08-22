import { bindDial, buildUI, wireKeyboard } from "./ui.js";
import { applyWoodTexture, getWoodSpeciesOptions } from "./wood-texture.js";

// The name the Go module publishes on globalThis, and the hook it calls once
// every export is in place. Both are spelled out in cmd/glockenspiel-wasm/main.go
// and must stay in step with it: a rename on one side leaves the other waiting
// forever for a signal that never comes.
const WASM_NAMESPACE = "glockenspielWasm";
const WASM_READY_CALLBACK = "__glockenspielWasmReady";

// How long to wait for that ready signal before giving up. The signal itself is
// a real happens-after edge, so this is not a race workaround; it only turns a
// module that died before publishing its exports -- a Go panic during init, a
// trap in the runtime -- into a visible error instead of a page that sits on
// "loading" with an empty console.
const WASM_READY_TIMEOUT_MS = 10000;

let audioContext = null;
let outputNode = null;
let wasm = null;
let wasmMemory = null;
let wasmReady = false;
let audioReady = false;
let initAudioPromise = null;
let masterGain = 0.7;
let strikeVelocity = 96;
let ui = null;
let woodSpecies = "beech";

function updateStatus(message, isError = false) {
  const status = document.getElementById("status");
  status.textContent = message;
  status.dataset.error = isError ? "true" : "false";
}

function clamp(value, min, max) {
  return Math.min(max, Math.max(min, value));
}

// The cached view over Go's heap, plus the two facts that decide whether it is
// still valid: which buffer it was cut from and which region of it it covers.
let interleavedView = null;
let interleavedViewBuffer = null;
let interleavedViewPtr = 0;

// interleavedFrames returns a Float32Array over `frames` stereo frames starting
// at `ptr` in the WASM linear memory, reusing the previous view when nothing
// relevant has changed. A view per callback is one allocation every ~11.6 ms at
// 512 frames and 44.1 kHz, on the one thread that must not pause for a GC.
//
// The hazard this function exists for: a WebAssembly.Memory grows when Go's
// heap grows, and growing DETACHES the old ArrayBuffer. A view hoisted out of
// the callback and never rechecked then points into a buffer that no longer
// backs anything -- and it does not throw. Measured in Chrome, after
// `memory.grow(1)`: the old buffer reports byteLength 0, `memory.buffer` is a
// different object, the stale view's length drops to 0, and indexing it returns
// `undefined`, which becomes NaN the moment it is written into the output
// buffer. So the symptom is not an exception at the point of the mistake but a
// channel of NaN -- silence, or worse depending on what the graph does with it
// -- starting at whatever unrelated moment the heap happened to grow: typically
// minutes in, once, and never while a debugger is attached. Hence three checks,
// all of them cheap:
//
//   - buffer identity: memory.buffer returns a *new* ArrayBuffer object after a
//     grow, so an identity comparison catches the detachment directly;
//   - byteLength === 0: how a detached ArrayBuffer reports itself. Re-reading
//     memory.buffer every call should already have handed us the live buffer,
//     but constructing a view over a detached one throws, and throwing out of
//     onaudioprocess is not how this should fail. Skipping the block yields one
//     buffer of silence instead;
//   - the pointer and length: ProcessBlock hands back a pointer into a Go slice,
//     and Go is free to move or resize that allocation between calls, so a
//     stable buffer does not imply a stable region.
//
// Returns null when no view can be built, in which case the caller leaves the
// output silent for that block.
function interleavedFrames(ptr, frames) {
  const floats = frames * 2;
  const buffer = wasmMemory.buffer;

  if (buffer.byteLength === 0) {
    interleavedView = null;
    interleavedViewBuffer = null;

    return null;
  }

  if (
    interleavedView === null ||
    interleavedViewBuffer !== buffer ||
    interleavedViewPtr !== ptr ||
    interleavedView.length !== floats
  ) {
    interleavedView = new Float32Array(buffer, ptr, floats);
    interleavedViewBuffer = buffer;
    interleavedViewPtr = ptr;
  }

  return interleavedView;
}

async function initAudio() {
  if (audioReady) return;
  if (initAudioPromise) return initAudioPromise;

  initAudioPromise = (async () => {
    audioContext = new (window.AudioContext || window.webkitAudioContext)();

    const initError = wasm.init(audioContext.sampleRate);
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

      if (!wasmMemory || !wasm) {
        return;
      }

      const interleavedPtr = wasm.processBlock(left.length);
      if (!interleavedPtr) {
        return;
      }

      const interleaved = interleavedFrames(
        Number(interleavedPtr),
        left.length,
      );
      if (interleaved === null) {
        return;
      }

      for (let frame = 0; frame < left.length; frame += 1) {
        left[frame] = interleaved[frame * 2];
        right[frame] = interleaved[frame * 2 + 1];
      }
    };

    outputNode.connect(audioContext.destination);
    await audioContext.resume();

    wasm.setMasterGain(masterGain);

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
    wasm.noteOn(note, strikeVelocity);
  };

  if (!audioReady) {
    initAudio()
      .then(start)
      .catch((error) => {
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
  const woodSelect = document.getElementById("wood-species");
  const woodNote = document.getElementById("wood-note");

  bindDial(velocity, velocityValue, (value) => String(value));
  bindDial(gain, gainValue, (value) => `${value}%`);

  if (woodSelect) {
    const species = getWoodSpeciesOptions();
    woodSelect.replaceChildren(
      ...species.map(({ id, label }) => {
        const option = document.createElement("option");
        option.value = id;
        option.textContent = label;
        return option;
      }),
    );
    woodSelect.value = woodSpecies;
    if (woodNote) {
      const initial = species.find(({ id }) => id === woodSpecies);
      woodNote.textContent = initial?.description || "";
    }

    woodSelect.addEventListener("change", () => {
      woodSpecies = woodSelect.value;
      applyWoodTexture(document.documentElement, woodSpecies);
      if (woodNote) {
        const selected = species.find(({ id }) => id === woodSpecies);
        woodNote.textContent = selected?.description || "";
      }
    });
  }

  velocity.addEventListener("input", () => {
    strikeVelocity = clamp(Number(velocity.value), 1, 127);
  });

  gain.addEventListener("input", () => {
    masterGain = clamp(Number(gain.value) / 100, 0.1, 1.0);
    if (audioReady && wasm) {
      wasm.setMasterGain(masterGain);
    }
  });
}

// wasmModuleURL resolves the URL to fetch the module from, appending the
// content hash that scripts/build-wasm.sh records in dist/manifest.json.
//
// The artifact keeps its fixed name -- internal/server hard-codes
// "glockenspiel.wasm" to recognise a missing build and answer with the command
// that produces it -- so the fingerprint travels in the query string instead of
// the file name. A cache keyed on the full URL still sees a new resource per
// build, which is the point: the module is the one file here big enough that a
// stale copy matters, and the one whose staleness is invisible (old audio code,
// current UI).
//
// A missing or unreadable manifest is not fatal. A checkout built before this
// script existed, or served by something that does not hand out .json, should
// still load the demo; it just falls back to plain revalidation.
async function wasmModuleURL() {
  const url = "dist/glockenspiel.wasm";

  try {
    const response = await fetch("dist/manifest.json", { cache: "no-store" });
    if (!response.ok) {
      return url;
    }

    const manifest = await response.json();
    if (typeof manifest.hash === "string" && manifest.hash.length > 0) {
      return `${url}?v=${encodeURIComponent(manifest.hash)}`;
    }
  } catch (error) {
    console.warn(
      "No build manifest; fetching the module unfingerprinted",
      error,
    );
  }

  return url;
}

// waitForWasmReady installs the hook the Go module invokes once its exports are
// published, and resolves with the namespace object it passes.
//
// This replaces a `setTimeout(resolve, 50)` after `go.run(...)` followed by a
// typeof check. That sleep was a guess about how long a machine needs to get
// through the Go runtime's start-up, and it is wrong in both directions: too
// short on a loaded CI box or a cold cache, where the page reported "WASM
// exports not found" for a module that was seconds from being ready, and 50 ms
// of dead time on every load where it was not.
//
// The hook has to be installed before the runtime starts, because Go calls it
// from inside `go.run(...)` -- the module's main runs synchronously up to the
// point where it blocks -- so there is no later moment at which registering it
// would still be in time.
function waitForWasmReady() {
  return new Promise((resolve, reject) => {
    const timer = window.setTimeout(() => {
      delete window[WASM_READY_CALLBACK];
      reject(
        new Error(
          `WASM module did not signal ready within ${WASM_READY_TIMEOUT_MS} ms`,
        ),
      );
    }, WASM_READY_TIMEOUT_MS);

    window[WASM_READY_CALLBACK] = (api) => {
      window.clearTimeout(timer);
      delete window[WASM_READY_CALLBACK];
      resolve(api || window[WASM_NAMESPACE]);
    };
  });
}

async function init() {
  try {
    applyWoodTexture(document.documentElement, woodSpecies);

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
    const moduleURL = await wasmModuleURL();
    const response = await fetch(moduleURL);
    if (!response.ok) {
      throw new Error(`Failed to fetch WASM: ${response.status}`);
    }

    let result;
    try {
      result = await WebAssembly.instantiateStreaming(
        response.clone(),
        go.importObject,
      );
    } catch (_streamingError) {
      const bytes = await response.arrayBuffer();
      result = await WebAssembly.instantiate(bytes, go.importObject);
    }

    wasmMemory =
      result.instance.exports.mem || result.instance.exports.memory || null;
    if (!wasmMemory) {
      throw new Error("WASM memory export not found");
    }

    const ready = waitForWasmReady();
    // go.run resolves when the Go program exits, which for this module means
    // it died: main blocks forever on purpose. Reporting that beats leaving an
    // unhandled rejection in the console next to a page that stopped working.
    go.run(result.instance).catch((error) => {
      console.error("Go runtime stopped", error);
      updateStatus(`WASM runtime stopped: ${error.message}`, true);
    });
    wasm = await ready;

    if (
      !wasm ||
      typeof wasm.init !== "function" ||
      typeof wasm.noteOn !== "function" ||
      typeof wasm.processBlock !== "function" ||
      typeof wasm.setMasterGain !== "function"
    ) {
      throw new Error(`${WASM_NAMESPACE} is missing its exports`);
    }

    wasmReady = true;
    updateStatus("WASM loaded. Strike a bar to start audio.");
  } catch (error) {
    console.error("Failed to load WASM demo", error);
    updateStatus(error.message, true);
  }
}

window.addEventListener("load", init);
