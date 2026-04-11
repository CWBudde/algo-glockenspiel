const TEXTURE_WIDTH = 1024;
const TEXTURE_HEIGHT = 576;
const SEED = 918273;

// The panel texture is a tangential slice through cylindrical tree space:
// growth rings, z-driven distortion, longitudinal pores, and radial rays.
const BASE_PRESET = {
  boardWidthCm: 56,
  boardHeightCm: 31.5,
  tangentOffsetCm: 12.2,
  ringSpacingCm: 1.18,
  ringWidthJitter: 0.16,
  latewoodThreshold: 0.72,
  latewoodTransition: 0.13,
  beerBase: [0.73, 0.57, 0.38],
  alphaBase: 0.74,
  alphaLatewood: 0.92,
  alphaRingNoise: 0.12,
  alphaDetailNoise: 0.07,
  poreCellCm: 0.42,
  poreRadiusEarlyCm: 0.095,
  poreRadiusLateCm: 0.036,
  poreDensity: 0.46,
  poreDarkening: 0.42,
  rayThetaCell: 0.045,
  rayHeightCm: 1.95,
  rayWidthCm: 0.07,
  rayLengthCm: 0.72,
  rayStrength: 0.14,
  rippleAmpCm: 0.24,
  rippleFreqZ: 1.45,
  rippleFreqCirc: 0.82,
  ripple2AmpCm: 0.11,
  ripple2FreqZ: 3.6,
  ripple2FreqCirc: 0.22,
  tangentialAmpCm: 0.08,
  tangentialFreqZ: 0.95,
  tangentialFreqCirc: 0.52,
  finishHighlight: 0.1,
};

const SPECIES_PRESETS = {
  beech: {
    name: "Beech",
    description: "Light diffuse-porous beech with subtle ring contrast and very restrained figure.",
    beerBase: [0.89, 0.8, 0.64],
    alphaBase: 0.22,
    alphaLatewood: 0.34,
    alphaRingNoise: 0.05,
    alphaDetailNoise: 0.03,
    poreCellCm: 0.58,
    poreRadiusEarlyCm: 0.022,
    poreRadiusLateCm: 0.014,
    poreDensity: 0.16,
    poreDarkening: 0.08,
    rayThetaCell: 0.06,
    rayHeightCm: 1.5,
    rayWidthCm: 0.03,
    rayLengthCm: 0.44,
    rayStrength: 0.04,
    ringSpacingCm: 1.42,
    latewoodThreshold: 0.79,
    latewoodTransition: 0.08,
    rippleAmpCm: 0.03,
    rippleFreqZ: 0.52,
    rippleFreqCirc: 0.18,
    ripple2AmpCm: 0.01,
    ripple2FreqZ: 1.1,
    ripple2FreqCirc: 0.08,
    tangentialAmpCm: 0.008,
    tangentialFreqZ: 0.36,
    tangentialFreqCirc: 0.14,
    finishHighlight: 0.025,
  },
  walnut: {
    name: "Walnut",
    description: "Dark semi-ring-porous walnut with restrained rays and soft curl.",
    beerBase: [0.67, 0.5, 0.32],
    alphaBase: 0.9,
    alphaLatewood: 0.62,
    alphaRingNoise: 0.09,
    alphaDetailNoise: 0.05,
    poreCellCm: 0.46,
    poreRadiusEarlyCm: 0.08,
    poreRadiusLateCm: 0.028,
    poreDensity: 0.34,
    poreDarkening: 0.34,
    rayThetaCell: 0.06,
    rayHeightCm: 1.65,
    rayWidthCm: 0.04,
    rayLengthCm: 0.58,
    rayStrength: 0.06,
    ringSpacingCm: 1.28,
    latewoodThreshold: 0.75,
    latewoodTransition: 0.11,
    rippleAmpCm: 0.13,
    rippleFreqZ: 1.1,
    rippleFreqCirc: 0.56,
    ripple2AmpCm: 0.05,
    ripple2FreqZ: 2.8,
    ripple2FreqCirc: 0.16,
    tangentialAmpCm: 0.04,
    tangentialFreqZ: 0.72,
    tangentialFreqCirc: 0.34,
    finishHighlight: 0.07,
  },
  oak: {
    name: "Oak",
    description: "Ring-porous oak with strong pore structure and prominent medullary rays.",
    beerBase: [0.79, 0.64, 0.43],
    alphaBase: 0.58,
    alphaLatewood: 1.18,
    alphaRingNoise: 0.14,
    alphaDetailNoise: 0.08,
    poreCellCm: 0.34,
    poreRadiusEarlyCm: 0.12,
    poreRadiusLateCm: 0.022,
    poreDensity: 0.62,
    poreDarkening: 0.48,
    rayThetaCell: 0.03,
    rayHeightCm: 2.4,
    rayWidthCm: 0.12,
    rayLengthCm: 1.2,
    rayStrength: 0.22,
    ringSpacingCm: 1.05,
    latewoodThreshold: 0.67,
    latewoodTransition: 0.16,
    rippleAmpCm: 0.09,
    rippleFreqZ: 0.88,
    rippleFreqCirc: 0.34,
    ripple2AmpCm: 0.03,
    ripple2FreqZ: 1.9,
    ripple2FreqCirc: 0.12,
    tangentialAmpCm: 0.02,
    tangentialFreqZ: 0.55,
    tangentialFreqCirc: 0.2,
    finishHighlight: 0.04,
  },
  maple: {
    name: "Maple",
    description: "Diffuse-porous curly maple with fine pores and stronger figured ripple.",
    beerBase: [0.84, 0.72, 0.53],
    alphaBase: 0.48,
    alphaLatewood: 0.82,
    alphaRingNoise: 0.11,
    alphaDetailNoise: 0.06,
    poreCellCm: 0.54,
    poreRadiusEarlyCm: 0.032,
    poreRadiusLateCm: 0.018,
    poreDensity: 0.2,
    poreDarkening: 0.16,
    rayThetaCell: 0.05,
    rayHeightCm: 1.7,
    rayWidthCm: 0.045,
    rayLengthCm: 0.68,
    rayStrength: 0.08,
    ringSpacingCm: 1.36,
    latewoodThreshold: 0.74,
    latewoodTransition: 0.09,
    rippleAmpCm: 0.28,
    rippleFreqZ: 1.9,
    rippleFreqCirc: 1.02,
    ripple2AmpCm: 0.14,
    ripple2FreqZ: 4.2,
    ripple2FreqCirc: 0.24,
    tangentialAmpCm: 0.11,
    tangentialFreqZ: 1.22,
    tangentialFreqCirc: 0.64,
    finishHighlight: 0.14,
  },
};

const DEFAULT_SPECIES = "beech";
const textureCache = new Map();

const LIGHT_DIR = normalize3([-0.35, 0.92, 0.18]);
const VIEW_DIR = normalize3([0.08, 0.97, 0.24]);
const HALF_DIR = normalize3([
  LIGHT_DIR[0] + VIEW_DIR[0],
  LIGHT_DIR[1] + VIEW_DIR[1],
  LIGHT_DIR[2] + VIEW_DIR[2],
]);

export function getWoodSpeciesOptions() {
  return Object.entries(SPECIES_PRESETS).map(([id, preset]) => ({
    id,
    label: preset.name,
    description: preset.description,
  }));
}

export function applyWoodTexture(root = null, species = DEFAULT_SPECIES) {
  if (typeof document === "undefined") {
    return;
  }

  const target = root ?? document.documentElement;
  if (!target) {
    return;
  }

  const resolvedSpecies = SPECIES_PRESETS[species] ? species : DEFAULT_SPECIES;
  let textureUrl = textureCache.get(resolvedSpecies);
  if (!textureUrl) {
    textureUrl = createWoodTexture(resolvePreset(resolvedSpecies));
    textureCache.set(resolvedSpecies, textureUrl);
  }

  target.style.setProperty("--wood-panel-texture", `url("${textureUrl}")`);
  target.dataset.woodSpecies = resolvedSpecies;
}

function resolvePreset(species) {
  return { ...BASE_PRESET, ...SPECIES_PRESETS[species] };
}

function createWoodTexture(preset) {
  const canvas = document.createElement("canvas");
  canvas.width = TEXTURE_WIDTH;
  canvas.height = TEXTURE_HEIGHT;

  const context = canvas.getContext("2d", { alpha: false });
  if (!context) {
    return "assets/wood-panel.svg";
  }

  const image = context.createImageData(TEXTURE_WIDTH, TEXTURE_HEIGHT);
  const pixels = image.data;

  for (let y = 0; y < TEXTURE_HEIGHT; y += 1) {
    const v = y / (TEXTURE_HEIGHT - 1);
    for (let x = 0; x < TEXTURE_WIDTH; x += 1) {
      const u = x / (TEXTURE_WIDTH - 1);
      const color = sampleBoard(preset, u, v);
      const index = (y * TEXTURE_WIDTH + x) * 4;
      pixels[index] = color[0];
      pixels[index + 1] = color[1];
      pixels[index + 2] = color[2];
      pixels[index + 3] = 255;
    }
  }

  context.putImageData(image, 0, 0);
  return canvas.toDataURL("image/png");
}

function sampleBoard(preset, u, v) {
  const point = boardToTree(preset, u, v);
  const distorted = distortPoint(preset, point);
  const ring = sampleGrowthRing(preset, distorted);
  const pore = samplePores(preset, distorted);
  const ray = sampleRays(preset, distorted);
  const fiber = sampleFiberDirection(preset, point);

  let alpha = preset.alphaBase;
  alpha += preset.alphaLatewood * ring.latewood;
  alpha += preset.alphaRingNoise * ring.ringNoise;
  alpha += preset.alphaDetailNoise * anisotropicNoise(distorted.x * 0.85, distorted.z * 0.42, SEED + 71, 3);
  alpha += pore.weight * preset.poreDarkening;
  alpha = Math.max(0.35, alpha);

  const beerColor = beerShade(preset.beerBase, alpha);
  const figure = sampleFigureHighlight(fiber, ray.weight, ring.earlywood);

  let color = [
    beerColor[0] * (0.98 + 0.07 * ring.detail),
    beerColor[1] * (0.98 + 0.06 * ring.detail),
    beerColor[2] * (0.98 + 0.05 * ring.detail),
  ];

  const earlywoodSink = 0.05 * ring.earlywood;
  color = darken(color, earlywoodSink + pore.weight * 0.30);
  color = lighten(color, ray.weight * preset.rayStrength * 0.8);
  color = lighten(color, figure * preset.finishHighlight);

  const topLight = 0.03 * (1 - v);
  const sideFalloff = 0.04 * Math.pow(Math.abs(u - 0.5) / 0.5, 2);
  color = lighten(color, topLight - sideFalloff);

  return [
    clamp255(color[0] * 255),
    clamp255(color[1] * 255),
    clamp255(color[2] * 255),
  ];
}

function boardToTree(preset, u, v) {
  return {
    x: (u - 0.5) * preset.boardWidthCm,
    y: preset.tangentOffsetCm,
    z: (0.5 - v) * preset.boardHeightCm,
  };
}

function distortPoint(preset, point) {
  const polar = toPolar(point.x, point.y);
  const distortion = distortionField(preset, polar.r, polar.theta, point.z);
  return {
    x: point.x + distortion.mr * polar.rx + distortion.mt * polar.tx,
    y: point.y + distortion.mr * polar.ry + distortion.mt * polar.ty,
    z: point.z,
  };
}

function distortionField(preset, r, theta, z) {
  const circum = r * theta;
  const phase1 = z * preset.rippleFreqZ + circum * preset.rippleFreqCirc;
  const phase2 = z * preset.ripple2FreqZ - circum * preset.ripple2FreqCirc;

  const mr = preset.rippleAmpCm * Math.sin(phase1 + 0.55 * lowFreqNoise(circum * 0.18, z * 0.24, SEED + 101))
    + preset.ripple2AmpCm * Math.sin(phase2 + 0.25 * lowFreqNoise(circum * 0.33, z * 0.17, SEED + 103))
    + 0.03 * lowFreqNoise(theta * 3.4, z * 0.8, SEED + 107);

  const mt = preset.tangentialAmpCm * Math.sin(z * preset.tangentialFreqZ + circum * preset.tangentialFreqCirc)
    + 0.02 * lowFreqNoise(circum * 0.27 + 13.7, z * 0.31 - 2.1, SEED + 109);

  const dmrDz = preset.rippleAmpCm * preset.rippleFreqZ * Math.cos(phase1)
    + preset.ripple2AmpCm * preset.ripple2FreqZ * Math.cos(phase2);
  const dmtDz = preset.tangentialAmpCm * preset.tangentialFreqZ
    * Math.cos(z * preset.tangentialFreqZ + circum * preset.tangentialFreqCirc);

  return { mr, mt, dmrDz, dmtDz };
}

function sampleGrowthRing(preset, point) {
  const season = sampleSeason(preset, point.x, point.y);
  const detail = anisotropicNoise(point.x * 0.7, point.z * 1.9, SEED + 167, 3);

  return {
    latewood: season.latewood,
    earlywood: season.earlywood,
    ringNoise: season.ringNoise,
    detail,
  };
}

function samplePores(preset, point) {
  const cell = preset.poreCellCm;
  const cellX = Math.floor(point.x / cell);
  const cellY = Math.floor(point.y / cell);
  let weight = 0;

  for (let oy = -1; oy <= 1; oy += 1) {
    for (let ox = -1; ox <= 1; ox += 1) {
      const ix = cellX + ox;
      const iy = cellY + oy;
      if (hash2(ix, iy, SEED + 201) > preset.poreDensity) {
        continue;
      }

      const centerX = (ix + 0.18 + 0.64 * hash2(ix, iy, SEED + 203)) * cell;
      const centerY = (iy + 0.18 + 0.64 * hash2(ix, iy, SEED + 205)) * cell;
      const season = sampleSeason(preset, centerX, centerY);
      const poreRadius = lerp(preset.poreRadiusLateCm, preset.poreRadiusEarlyCm, season.earlywood);
      const dx = point.x - centerX;
      const dy = point.y - centerY;
      const radius = Math.hypot(dx, dy);
      if (radius >= poreRadius) {
        continue;
      }

      weight += wyvill(radius / poreRadius) * (0.65 + 0.35 * season.earlywood);
    }
  }

  return { weight: Math.min(1, weight) };
}

function sampleRays(preset, point) {
  const polar = toPolar(point.x, point.y);
  const thetaCell = preset.rayThetaCell;
  const zCell = preset.rayHeightCm;
  const thetaIndex = Math.floor(polar.theta / thetaCell);
  const zIndex = Math.floor(point.z / zCell);
  let weight = 0;

  for (let oz = -1; oz <= 1; oz += 1) {
    for (let ot = -1; ot <= 1; ot += 1) {
      const it = thetaIndex + ot;
      const iz = zIndex + oz;
      if (hash2(it, iz, SEED + 251) < 0.62) {
        continue;
      }

      const theta0 = (it + 0.2 + 0.6 * hash2(it, iz, SEED + 253)) * thetaCell;
      const z0 = (iz + 0.2 + 0.6 * hash2(it, iz, SEED + 257)) * zCell;
      const radialDist = Math.abs(point.x * Math.sin(theta0) - point.y * Math.cos(theta0));
      const longDist = Math.abs(point.z - z0);
      const q = Math.hypot(radialDist / preset.rayWidthCm, longDist / preset.rayLengthCm);
      if (q >= 1) {
        continue;
      }

      weight += wyvill(q) * (0.55 + 0.45 * hash2(it, iz, SEED + 263));
    }
  }

  return { weight: Math.min(1, weight) };
}

function sampleFiberDirection(preset, point) {
  const polar = toPolar(point.x, point.y);
  const distortion = distortionField(preset, polar.r, polar.theta, point.z);
  const fx = -(distortion.dmrDz * polar.rx + distortion.dmtDz * polar.tx);
  const fy = -(distortion.dmrDz * polar.ry + distortion.dmtDz * polar.ty);
  return normalize3([fx, fy, 1]);
}

function sampleFigureHighlight(fiber, rayWeight, earlywood) {
  const tangentFiber = normalize3([fiber[0], 0, fiber[2]]);
  const tangentHalf = normalize3([HALF_DIR[0], 0, HALF_DIR[2]]);
  const alignment = Math.max(0, dot3(tangentFiber, tangentHalf));
  const lift = Math.max(0, fiber[1] * HALF_DIR[1]);
  return Math.pow(alignment, 24) * (0.35 + 0.65 * lift) * (0.55 + 0.45 * earlywood)
    + rayWeight * 0.05;
}

function beerShade(base, alpha) {
  return [
    Math.pow(base[0], alpha),
    Math.pow(base[1], alpha),
    Math.pow(base[2], alpha),
  ];
}

function darken(color, amount) {
  const factor = Math.max(0, 1 - amount);
  return [color[0] * factor, color[1] * factor, color[2] * factor];
}

function lighten(color, amount) {
  if (amount <= 0) {
    return darken(color, -amount);
  }
  return [
    color[0] + (1 - color[0]) * amount,
    color[1] + (1 - color[1]) * amount,
    color[2] + (1 - color[2]) * amount,
  ];
}

function wyvill(q) {
  const q2 = q * q;
  const t = Math.max(0, 1 - q2);
  return t * t * t;
}

function anisotropicNoise(x, y, seed, octaves) {
  return fbm(x, y * 0.55, seed, octaves);
}

function lowFreqNoise(x, y, seed) {
  return fbm(x, y, seed, 2);
}

function sampleSeason(preset, x, y) {
  const polar = toPolar(x, y);
  const widthNoise = 1 + preset.ringWidthJitter * ringNoise(polar.r * 0.33, SEED + 151);
  const growthCoordinate = polar.r / (preset.ringSpacingCm * widthNoise)
    + 0.08 * ringNoise(polar.r * 0.12 + 8.4, SEED + 157);
  const ringPhase = fract(growthCoordinate);
  const latewood = smoothstep(
    preset.latewoodThreshold,
    preset.latewoodThreshold + preset.latewoodTransition,
    ringPhase,
  );
  return {
    latewood,
    earlywood: 1 - latewood,
    ringNoise: ringNoise(growthCoordinate * 0.9, SEED + 163),
  };
}

function ringNoise(x, seed) {
  const x0 = Math.floor(x);
  const x1 = x0 + 1;
  const t = smoothstep01(x - x0);
  const a = hash1(x0, seed) * 2 - 1;
  const b = hash1(x1, seed) * 2 - 1;
  return lerp(a, b, t);
}

function fbm(x, y, seed, octaves) {
  let amplitude = 0.5;
  let frequency = 1;
  let sum = 0;
  let norm = 0;

  for (let octave = 0; octave < octaves; octave += 1) {
    sum += amplitude * valueNoise(x * frequency, y * frequency, seed + octave * 17);
    norm += amplitude;
    amplitude *= 0.5;
    frequency *= 2;
  }

  return sum / (norm || 1);
}

function valueNoise(x, y, seed) {
  const x0 = Math.floor(x);
  const y0 = Math.floor(y);
  const xf = x - x0;
  const yf = y - y0;
  const u = smoothstep01(xf);
  const v = smoothstep01(yf);

  const a = hash2(x0, y0, seed);
  const b = hash2(x0 + 1, y0, seed);
  const c = hash2(x0, y0 + 1, seed);
  const d = hash2(x0 + 1, y0 + 1, seed);

  const top = lerp(a, b, u);
  const bottom = lerp(c, d, u);
  return lerp(top, bottom, v) * 2 - 1;
}

function toPolar(x, y) {
  const r = Math.max(1e-4, Math.hypot(x, y));
  const theta = Math.atan2(y, x);
  return {
    r,
    theta,
    rx: x / r,
    ry: y / r,
    tx: -y / r,
    ty: x / r,
  };
}

function normalize3(vector) {
  const length = Math.hypot(vector[0], vector[1], vector[2]) || 1;
  return [vector[0] / length, vector[1] / length, vector[2] / length];
}

function dot3(a, b) {
  return a[0] * b[0] + a[1] * b[1] + a[2] * b[2];
}

function hash1(x, seed) {
  let h = Math.imul(x | 0, 374761393);
  h ^= Math.imul(seed | 0, 1442695041);
  h ^= h >>> 13;
  h = Math.imul(h, 1274126177);
  h ^= h >>> 16;
  return (h >>> 0) / 4294967296;
}

function hash2(x, y, seed) {
  let h = Math.imul(x | 0, 374761393);
  h = Math.imul(h ^ Math.imul(y | 0, 668265263), 1274126177);
  h ^= Math.imul(seed | 0, 1442695041);
  h ^= h >>> 13;
  h = Math.imul(h, 1274126177);
  h ^= h >>> 16;
  return (h >>> 0) / 4294967296;
}

function fract(value) {
  return value - Math.floor(value);
}

function smoothstep(edge0, edge1, value) {
  const t = Math.max(0, Math.min(1, (value - edge0) / (edge1 - edge0 || 1)));
  return t * t * (3 - 2 * t);
}

function smoothstep01(value) {
  return value * value * (3 - 2 * value);
}

function lerp(a, b, t) {
  return a + (b - a) * t;
}

function clamp255(value) {
  return Math.max(0, Math.min(255, Math.round(value)));
}
