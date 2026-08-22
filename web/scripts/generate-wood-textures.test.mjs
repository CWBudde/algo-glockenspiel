import assert from "node:assert/strict";
import { test } from "node:test";

import {
  encodePng,
  filterScanlines,
  matchesPng,
} from "./generate-wood-textures.mjs";

const width = 2;
const height = 1;
const pixels = Buffer.from([10, 20, 30, 255, 14, 25, 36, 255]);
const scanlines = filterScanlines(width, height, pixels);

test("freshness ignores equivalent zlib streams", () => {
  const stored = encodePng(width, height, scanlines, 0);
  const generated = encodePng(width, height, scanlines, 9);

  assert.notDeepEqual(stored, generated);
  assert.equal(matchesPng(stored, width, height, scanlines), true);
  assert.equal(matchesPng(generated, width, height, scanlines), true);
});

test("freshness rejects stale pixels and malformed PNG data", () => {
  const png = encodePng(width, height, scanlines);
  const staleScanlines = Buffer.from(scanlines);
  staleScanlines[staleScanlines.length - 1] ^= 1;

  assert.equal(matchesPng(png, width, height, staleScanlines), false);
  assert.equal(matchesPng(png, width + 1, height, scanlines), false);

  const corrupt = Buffer.from(png);
  corrupt[corrupt.length - 5] ^= 1;
  assert.equal(matchesPng(corrupt, width, height, scanlines), false);
});
