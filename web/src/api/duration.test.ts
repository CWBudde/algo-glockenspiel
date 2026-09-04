import { describe, expect, it } from "vitest";

import { parseDuration } from "./duration";

describe("parseDuration", () => {
  it("reads a bare number as seconds", () => {
    expect(parseDuration("30")).toEqual({ seconds: 30 });
  });

  it("reads a Go duration string", () => {
    expect(parseDuration("30s")).toEqual({ seconds: 30 });
    expect(parseDuration("2m")).toEqual({ seconds: 120 });
    expect(parseDuration("1h30m")).toEqual({ seconds: 5400 });
  });

  it("rejects an empty string", () => {
    expect(parseDuration("")).toEqual({
      error: "The time budget is required.",
    });
  });

  it("rejects a string that is neither a number nor a duration", () => {
    const result = parseDuration("soon");

    expect("error" in result).toBe(true);
  });
});
