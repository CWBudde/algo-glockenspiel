#!/usr/bin/env node

// Deterministic build-time port of the browser's procedural wood sampler.
// It renders a tangential slice through cylindrical tree space: growth rings,
// z-driven distortion, longitudinal pores and radial rays, shaded through a
// Beer-Lambert absorption term. The PNG writer uses only Node built-ins so the
// generated source assets do not add a browser or native image dependency.

import { deflateSync, inflateSync } from "node:zlib";
import { readFile, mkdir, writeFile } from "node:fs/promises";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const scriptDir = dirname(fileURLToPath(import.meta.url));
const webDir = resolve(scriptDir, "..");
const presetPath = resolve(webDir, "src/lib/wood-presets.json");
const outputDir = resolve(webDir, "assets/wood");
const config = JSON.parse(await readFile(presetPath, "utf8"));
const { width, height, seed } = config.texture;
const lightDir = normalize3([-0.35, 0.92, 0.18]);
const viewDir = normalize3([0.08, 0.97, 0.24]);
const halfDir = normalize3(lightDir.map((value, i) => value + viewDir[i]));

async function main() {
  const check = process.argv.includes("--check");
  let stale = false;
  for (const [id, overrides] of Object.entries(config.species)) {
    const preset = { ...config.base, ...overrides };
    const pixels = renderTexture(preset);
    const scanlines = filterScanlines(width, height, pixels);
    const outputPath = resolve(outputDir, `${id}.png`);

    if (check) {
      let existing;
      try {
        existing = await readFile(outputPath);
      } catch {
        existing = null;
      }
      if (existing === null || !matchesPng(existing, width, height, scanlines)) {
        stale = true;
        console.error(`stale wood texture: assets/wood/${id}.png`);
      }
    } else {
      await mkdir(outputDir, { recursive: true });
      await writeFile(outputPath, encodePng(width, height, scanlines));
      console.log(`generated assets/wood/${id}.png`);
    }
  }

  if (stale) {
    console.error("Run `npm run wood:generate` and commit the generated PNGs.");
    process.exitCode = 1;
  }
}

if (resolve(process.argv[1] ?? "") === fileURLToPath(import.meta.url)) {
  await main();
}

function renderTexture(preset) {
  const pixels = Buffer.allocUnsafe(width * height * 4);
  for (let y = 0; y < height; y += 1) {
    const v = y / (height - 1);
    for (let x = 0; x < width; x += 1) {
      const u = x / (width - 1);
      const color = sampleBoard(preset, u, v);
      const index = (y * width + x) * 4;
      pixels[index] = color[0];
      pixels[index + 1] = color[1];
      pixels[index + 2] = color[2];
      pixels[index + 3] = 255;
    }
  }
  return pixels;
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
  alpha += preset.alphaDetailNoise * anisotropicNoise(distorted.x * 0.85, distorted.z * 0.42, seed + 71, 3);
  alpha += pore * preset.poreDarkening;
  alpha = Math.max(0.35, alpha);

  const beerColor = preset.beerBase.map((channel) => channel ** alpha);
  const figure = sampleFigureHighlight(fiber, ray, ring.earlywood);
  let color = [
    beerColor[0] * (0.98 + 0.07 * ring.detail),
    beerColor[1] * (0.98 + 0.06 * ring.detail),
    beerColor[2] * (0.98 + 0.05 * ring.detail),
  ];
  color = darken(color, 0.05 * ring.earlywood + pore * 0.3);
  color = lighten(color, ray * preset.rayStrength * 0.8);
  color = lighten(color, figure * preset.finishHighlight);
  color = lighten(color, 0.03 * (1 - v) - 0.04 * (Math.abs(u - 0.5) / 0.5) ** 2);
  return color.map((channel) => clamp255(channel * 255));
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
  const mr =
    preset.rippleAmpCm * Math.sin(phase1 + 0.55 * lowFreqNoise(circum * 0.18, z * 0.24, seed + 101)) +
    preset.ripple2AmpCm * Math.sin(phase2 + 0.25 * lowFreqNoise(circum * 0.33, z * 0.17, seed + 103)) +
    0.03 * lowFreqNoise(theta * 3.4, z * 0.8, seed + 107);
  const mt =
    preset.tangentialAmpCm * Math.sin(z * preset.tangentialFreqZ + circum * preset.tangentialFreqCirc) +
    0.02 * lowFreqNoise(circum * 0.27 + 13.7, z * 0.31 - 2.1, seed + 109);
  return {
    mr,
    mt,
    dmrDz:
      preset.rippleAmpCm * preset.rippleFreqZ * Math.cos(phase1) +
      preset.ripple2AmpCm * preset.ripple2FreqZ * Math.cos(phase2),
    dmtDz:
      preset.tangentialAmpCm * preset.tangentialFreqZ *
      Math.cos(z * preset.tangentialFreqZ + circum * preset.tangentialFreqCirc),
  };
}

function sampleGrowthRing(preset, point) {
  const season = sampleSeason(preset, point.x, point.y);
  return {
    ...season,
    detail: anisotropicNoise(point.x * 0.7, point.z * 1.9, seed + 167, 3),
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
      if (hash2(ix, iy, seed + 201) > preset.poreDensity) continue;
      const centerX = (ix + 0.18 + 0.64 * hash2(ix, iy, seed + 203)) * cell;
      const centerY = (iy + 0.18 + 0.64 * hash2(ix, iy, seed + 205)) * cell;
      const season = sampleSeason(preset, centerX, centerY);
      const poreRadius = lerp(preset.poreRadiusLateCm, preset.poreRadiusEarlyCm, season.earlywood);
      const radius = Math.hypot(point.x - centerX, point.y - centerY);
      if (radius < poreRadius) weight += wyvill(radius / poreRadius) * (0.65 + 0.35 * season.earlywood);
    }
  }
  return Math.min(1, weight);
}

function sampleRays(preset, point) {
  const polar = toPolar(point.x, point.y);
  const thetaIndex = Math.floor(polar.theta / preset.rayThetaCell);
  const zIndex = Math.floor(point.z / preset.rayHeightCm);
  let weight = 0;
  for (let oz = -1; oz <= 1; oz += 1) {
    for (let ot = -1; ot <= 1; ot += 1) {
      const it = thetaIndex + ot;
      const iz = zIndex + oz;
      if (hash2(it, iz, seed + 251) < 0.62) continue;
      const theta0 = (it + 0.2 + 0.6 * hash2(it, iz, seed + 253)) * preset.rayThetaCell;
      const z0 = (iz + 0.2 + 0.6 * hash2(it, iz, seed + 257)) * preset.rayHeightCm;
      const radialDist = Math.abs(point.x * Math.sin(theta0) - point.y * Math.cos(theta0));
      const q = Math.hypot(radialDist / preset.rayWidthCm, Math.abs(point.z - z0) / preset.rayLengthCm);
      if (q < 1) weight += wyvill(q) * (0.55 + 0.45 * hash2(it, iz, seed + 263));
    }
  }
  return Math.min(1, weight);
}

function sampleFiberDirection(preset, point) {
  const polar = toPolar(point.x, point.y);
  const distortion = distortionField(preset, polar.r, polar.theta, point.z);
  return normalize3([
    -(distortion.dmrDz * polar.rx + distortion.dmtDz * polar.tx),
    -(distortion.dmrDz * polar.ry + distortion.dmtDz * polar.ty),
    1,
  ]);
}

function sampleFigureHighlight(fiber, rayWeight, earlywood) {
  const tangentFiber = normalize3([fiber[0], 0, fiber[2]]);
  const tangentHalf = normalize3([halfDir[0], 0, halfDir[2]]);
  const alignment = Math.max(0, dot3(tangentFiber, tangentHalf));
  const lift = Math.max(0, fiber[1] * halfDir[1]);
  return alignment ** 24 * (0.35 + 0.65 * lift) * (0.55 + 0.45 * earlywood) + rayWeight * 0.05;
}

function sampleSeason(preset, x, y) {
  const polar = toPolar(x, y);
  const widthNoise = 1 + preset.ringWidthJitter * ringNoise(polar.r * 0.33, seed + 151);
  const growthCoordinate =
    polar.r / (preset.ringSpacingCm * widthNoise) +
    0.08 * ringNoise(polar.r * 0.12 + 8.4, seed + 157);
  const ringPhase = fract(growthCoordinate);
  const latewood = smoothstep(preset.latewoodThreshold, preset.latewoodThreshold + preset.latewoodTransition, ringPhase);
  return { latewood, earlywood: 1 - latewood, ringNoise: ringNoise(growthCoordinate * 0.9, seed + 163) };
}

function darken(color, amount) {
  const factor = Math.max(0, 1 - amount);
  return color.map((channel) => channel * factor);
}

function lighten(color, amount) {
  if (amount <= 0) return darken(color, -amount);
  return color.map((channel) => channel + (1 - channel) * amount);
}

function wyvill(q) {
  return Math.max(0, 1 - q * q) ** 3;
}

function anisotropicNoise(x, y, noiseSeed, octaves) {
  return fbm(x, y * 0.55, noiseSeed, octaves);
}

function lowFreqNoise(x, y, noiseSeed) {
  return fbm(x, y, noiseSeed, 2);
}

function ringNoise(x, noiseSeed) {
  const x0 = Math.floor(x);
  return lerp(hash1(x0, noiseSeed) * 2 - 1, hash1(x0 + 1, noiseSeed) * 2 - 1, smoothstep01(x - x0));
}

function fbm(x, y, noiseSeed, octaves) {
  let amplitude = 0.5;
  let frequency = 1;
  let sum = 0;
  let norm = 0;
  for (let octave = 0; octave < octaves; octave += 1) {
    sum += amplitude * valueNoise(x * frequency, y * frequency, noiseSeed + octave * 17);
    norm += amplitude;
    amplitude *= 0.5;
    frequency *= 2;
  }
  return sum / (norm || 1);
}

function valueNoise(x, y, noiseSeed) {
  const x0 = Math.floor(x);
  const y0 = Math.floor(y);
  const u = smoothstep01(x - x0);
  const v = smoothstep01(y - y0);
  const top = lerp(hash2(x0, y0, noiseSeed), hash2(x0 + 1, y0, noiseSeed), u);
  const bottom = lerp(hash2(x0, y0 + 1, noiseSeed), hash2(x0 + 1, y0 + 1, noiseSeed), u);
  return lerp(top, bottom, v) * 2 - 1;
}

function toPolar(x, y) {
  const r = Math.max(1e-4, Math.hypot(x, y));
  return { r, theta: Math.atan2(y, x), rx: x / r, ry: y / r, tx: -y / r, ty: x / r };
}

function normalize3(vector) {
  const length = Math.hypot(...vector) || 1;
  return vector.map((value) => value / length);
}

function dot3(a, b) { return a[0] * b[0] + a[1] * b[1] + a[2] * b[2]; }
function fract(value) { return value - Math.floor(value); }
function smoothstep01(value) { return value * value * (3 - 2 * value); }
function smoothstep(a, b, value) { return smoothstep01(Math.max(0, Math.min(1, (value - a) / (b - a || 1)))); }
function lerp(a, b, amount) { return a + (b - a) * amount; }
function clamp255(value) { return Math.max(0, Math.min(255, Math.round(value))); }

function hash1(x, noiseSeed) {
  let h = Math.imul(x | 0, 374761393);
  h ^= Math.imul(noiseSeed | 0, 1442695041);
  h ^= h >>> 13;
  h = Math.imul(h, 1274126177);
  h ^= h >>> 16;
  return (h >>> 0) / 4294967296;
}

function hash2(x, y, noiseSeed) {
  let h = Math.imul(x | 0, 374761393);
  h = Math.imul(h ^ Math.imul(y | 0, 668265263), 1274126177);
  h ^= Math.imul(noiseSeed | 0, 1442695041);
  h ^= h >>> 13;
  h = Math.imul(h, 1274126177);
  h ^= h >>> 16;
  return (h >>> 0) / 4294967296;
}

export function filterScanlines(pngWidth, pngHeight, rgba) {
  const stride = pngWidth * 4;
  const scanlines = Buffer.allocUnsafe((stride + 1) * pngHeight);
  for (let y = 0; y < pngHeight; y += 1) {
    const outputOffset = y * (stride + 1);
    const inputOffset = y * stride;
    scanlines[outputOffset] = 1;
    for (let x = 0; x < stride; x += 1) {
      const left = x < 4 ? 0 : rgba[inputOffset + x - 4];
      scanlines[outputOffset + x + 1] = rgba[inputOffset + x] - left;
    }
  }
  return scanlines;
}

export function encodePng(pngWidth, pngHeight, scanlines, compressionLevel = 9) {
  const header = Buffer.alloc(13);
  header.writeUInt32BE(pngWidth, 0);
  header.writeUInt32BE(pngHeight, 4);
  header[8] = 8;
  header[9] = 6;
  return Buffer.concat([
    Buffer.from([137, 80, 78, 71, 13, 10, 26, 10]),
    pngChunk("IHDR", header),
    pngChunk("IDAT", deflateSync(scanlines, { level: compressionLevel })),
    pngChunk("IEND", Buffer.alloc(0)),
  ]);
}

// zlib is allowed to choose a different compressed representation between
// Node releases. Asset freshness is therefore defined by the PNG header and
// the inflated, deterministically filtered scanlines, not the IDAT bytes.
export function matchesPng(png, expectedWidth, expectedHeight, expectedScanlines) {
  try {
    const signature = Buffer.from([137, 80, 78, 71, 13, 10, 26, 10]);
    if (png.length < signature.length || !png.subarray(0, 8).equals(signature)) {
      return false;
    }

    let offset = 8;
    let header = null;
    const imageData = [];
    let sawEnd = false;
    while (offset < png.length) {
      if (offset + 12 > png.length) return false;
      const length = png.readUInt32BE(offset);
      const chunkEnd = offset + 12 + length;
      if (chunkEnd > png.length) return false;

      const type = png.toString("ascii", offset + 4, offset + 8);
      const data = png.subarray(offset + 8, offset + 8 + length);
      const storedCrc = png.readUInt32BE(offset + 8 + length);
      if (storedCrc !== crc32(png.subarray(offset + 4, offset + 8 + length))) {
        return false;
      }

      if (type === "IHDR") {
        if (header !== null || length !== 13) return false;
        header = data;
      } else if (type === "IDAT") {
        imageData.push(data);
      } else if (type === "IEND") {
        if (length !== 0) return false;
        sawEnd = true;
        offset = chunkEnd;
        break;
      }
      offset = chunkEnd;
    }

    if (header === null || imageData.length === 0 || !sawEnd || offset !== png.length) {
      return false;
    }
    if (
      header.readUInt32BE(0) !== expectedWidth ||
      header.readUInt32BE(4) !== expectedHeight ||
      header[8] !== 8 ||
      header[9] !== 6 ||
      header[10] !== 0 ||
      header[11] !== 0 ||
      header[12] !== 0
    ) {
      return false;
    }

    return inflateSync(Buffer.concat(imageData)).equals(expectedScanlines);
  } catch {
    return false;
  }
}

function pngChunk(type, data) {
  const name = Buffer.from(type, "ascii");
  const length = Buffer.alloc(4);
  length.writeUInt32BE(data.length);
  const checksum = Buffer.alloc(4);
  checksum.writeUInt32BE(crc32(Buffer.concat([name, data])));
  return Buffer.concat([length, name, data, checksum]);
}

function crc32(data) {
  let crc = 0xffffffff;
  for (const byte of data) {
    crc ^= byte;
    for (let bit = 0; bit < 8; bit += 1) crc = (crc >>> 1) ^ (0xedb88320 & -(crc & 1));
  }
  return (crc ^ 0xffffffff) >>> 0;
}
