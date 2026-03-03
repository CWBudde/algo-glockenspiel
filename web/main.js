const FIRST_NOTE = 72; // C5
const OCTAVES = 2;
const SEMITONES = OCTAVES * 12;
const WHITE_OFFSETS = new Set([0, 2, 4, 5, 7, 9, 11]);
const KEY_BINDINGS = [
  'A', 'W', 'S', 'E', 'D', 'F', 'T', 'G', 'Y', 'H', 'U', 'J',
  'K', 'O', 'L', 'P', ';', "'", ']', '\\', 'Z', 'X', 'C', 'V',
];

let audioContext = null;
let outputNode = null;
let wasmMemory = null;
let wasmReady = false;
let audioReady = false;
let initAudioPromise = null;
let masterGain = 0.7;
let strikeVelocity = 96;

const activeVoices = [];
const pressedKeys = new Set();

function midiToName(note) {
  const names = ['C', 'C#', 'D', 'D#', 'E', 'F', 'F#', 'G', 'G#', 'A', 'A#', 'B'];
  const pitchClass = note % 12;
  const octave = Math.floor(note / 12) - 1;
  return `${names[pitchClass]}${octave}`;
}

function updateStatus(message, isError = false) {
  const status = document.getElementById('status');
  status.textContent = message;
  status.dataset.error = isError ? 'true' : 'false';
}

function clamp(value, min, max) {
  return Math.min(max, Math.max(min, value));
}

function panForNote(note) {
  const relative = (note - FIRST_NOTE) / Math.max(1, SEMITONES - 1);
  return relative * 1.2 - 0.6;
}

function renderNoteIntoVoice(note, velocity) {
  const rendered = wasmRenderNote(note, velocity);
  if (!rendered || !rendered.length || !rendered.ptr || !wasmMemory) {
    return null;
  }

  const samples = new Float32Array(
    wasmMemory.buffer,
    Number(rendered.ptr),
    Number(rendered.length),
  );

  return {
    note,
    pan: panForNote(note),
    position: 0,
    samples: new Float32Array(samples),
  };
}

async function initAudio() {
  if (audioReady) return;
  if (initAudioPromise) return initAudioPromise;

  initAudioPromise = (async () => {
    audioContext = new (window.AudioContext || window.webkitAudioContext)();

    const initError = wasmInit(audioContext.sampleRate);
    if (typeof initError === 'string' && initError.length > 0) {
      throw new Error(initError);
    }

    outputNode = audioContext.createScriptProcessor(512, 0, 2);
    outputNode.onaudioprocess = (event) => {
      const output = event.outputBuffer;
      const left = output.getChannelData(0);
      const right = output.getChannelData(1);

      left.fill(0);
      right.fill(0);

      for (let voiceIndex = activeVoices.length - 1; voiceIndex >= 0; voiceIndex -= 1) {
        const voice = activeVoices[voiceIndex];
        const samples = voice.samples;
        const pan = voice.pan;
        const leftGain = masterGain * (1 - pan) * 0.5;
        const rightGain = masterGain * (1 + pan) * 0.5;

        for (let frame = 0; frame < left.length; frame += 1) {
          if (voice.position >= samples.length) {
            activeVoices.splice(voiceIndex, 1);
            break;
          }

          const sample = samples[voice.position];
          left[frame] += sample * leftGain;
          right[frame] += sample * rightGain;
          voice.position += 1;
        }
      }

      for (let frame = 0; frame < left.length; frame += 1) {
        left[frame] = Math.tanh(left[frame]);
        right[frame] = Math.tanh(right[frame]);
      }
    };

    outputNode.connect(audioContext.destination);
    await audioContext.resume();

    audioReady = true;
    updateStatus(`Ready at ${Math.round(audioContext.sampleRate)} Hz. Strike a bar.`);
  })();

  try {
    await initAudioPromise;
  } finally {
    initAudioPromise = null;
  }
}

function strike(note) {
  if (!wasmReady) {
    return;
  }

  const start = () => {
    const voice = renderNoteIntoVoice(note, strikeVelocity);
    if (!voice) {
      updateStatus('Failed to render note buffer.', true);
      return;
    }

    activeVoices.push(voice);
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

function createBar(note, index) {
  const bar = document.createElement('button');
  const accidental = !WHITE_OFFSETS.has(note % 12);
  bar.type = 'button';
  bar.className = accidental ? 'bar accidental' : 'bar natural';
  bar.dataset.note = String(note);

  const name = document.createElement('span');
  name.className = 'note-name';
  name.textContent = midiToName(note);

  const hint = document.createElement('span');
  hint.className = 'key-hint';
  hint.textContent = KEY_BINDINGS[index] || '';

  bar.append(name, hint);

  const activate = () => {
    bar.classList.add('active');
    strike(note);
    window.clearTimeout(bar._activeTimer);
    bar._activeTimer = window.setTimeout(() => {
      bar.classList.remove('active');
    }, 180);
  };

  bar.addEventListener('pointerdown', (event) => {
    event.preventDefault();
    activate();
  });

  return bar;
}

function buildInstrument() {
  const instrument = document.getElementById('glockenspiel');
  for (let index = 0; index < SEMITONES; index += 1) {
    instrument.appendChild(createBar(FIRST_NOTE + index, index));
  }
}

function bindControls() {
  const velocity = document.getElementById('velocity');
  const velocityValue = document.getElementById('velocity-value');
  const gain = document.getElementById('gain');
  const gainValue = document.getElementById('gain-value');

  velocity.addEventListener('input', () => {
    strikeVelocity = clamp(Number(velocity.value), 1, 127);
    velocityValue.textContent = String(strikeVelocity);
  });

  gain.addEventListener('input', () => {
    masterGain = clamp(Number(gain.value) / 100, 0.1, 1.0);
    gainValue.textContent = `${Math.round(masterGain * 100)}%`;
  });
}

function bindKeyboard() {
  const keyMap = new Map();
  KEY_BINDINGS.forEach((key, index) => {
    keyMap.set(key, FIRST_NOTE + index);
  });

  document.addEventListener('keydown', (event) => {
    if (event.repeat) {
      return;
    }

    const normalized = event.key.toUpperCase();
    const note = keyMap.get(normalized);
    if (note === undefined || pressedKeys.has(normalized)) {
      return;
    }

    pressedKeys.add(normalized);
    const bar = document.querySelector(`[data-note="${note}"]`);
    if (bar) {
      bar.classList.add('active');
      window.setTimeout(() => bar.classList.remove('active'), 180);
    }
    strike(note);
  });

  document.addEventListener('keyup', (event) => {
    pressedKeys.delete(event.key.toUpperCase());
  });
}

async function init() {
  try {
    const go = new Go();
    const response = await fetch('dist/glockenspiel.wasm');
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
      throw new Error('WASM memory export not found');
    }

    window.__algoGlockenspielWasmMemory = wasmMemory;
    go.run(result.instance);

    await new Promise((resolve) => window.setTimeout(resolve, 50));

    if (typeof wasmInit === 'undefined' || typeof wasmRenderNote === 'undefined') {
      throw new Error('WASM exports not found');
    }

    wasmReady = true;
    buildInstrument();
    bindControls();
    bindKeyboard();
    updateStatus('WASM loaded. Click a bar to start audio.');
  } catch (error) {
    console.error('Failed to load WASM demo', error);
    updateStatus(error.message, true);
  }
}

window.addEventListener('load', init);
