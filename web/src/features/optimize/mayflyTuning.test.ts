import { describe, expect, it } from "vitest";

import {
  mayflyTuningFieldsFor,
  type MayflyTuningField,
} from "../../api/mayflyTuning.generated";
import {
  buildMayflyTuningDocument,
  parseOptionalInt,
  parseOptionalNumber,
  tuningErrorKey,
} from "./FitForm";

/**
 * These are pure-function tests on purpose. web/package.json carries vitest but
 * not @testing-library/react, so nothing here renders a component; the document
 * builder is exported precisely so that the interesting half -- what reaches
 * the wire -- can be checked without a DOM.
 */

/** The knobs the form would show for a dialect, as the component asks for them. */
function fieldsFor(variant: string): readonly MayflyTuningField[] {
  return mayflyTuningFieldsFor(variant);
}

function built(variant: string, values: Record<string, string>) {
  const result = buildMayflyTuningDocument(fieldsFor(variant), values);

  if ("errors" in result) {
    throw new Error(`unexpected errors: ${JSON.stringify(result.errors)}`);
  }

  return result;
}

describe("mayflyTuningFieldsFor", () => {
  it("returns only the shared knobs for auto", () => {
    const fields = fieldsFor("auto");

    expect(fields.length).toBeGreaterThan(0);
    expect(fields.every((field) => field.variant === "")).toBe(true);
    expect(fields.some((field) => field.key === "npop")).toBe(true);
    // elite_count belongs to desma, and no dialect is chosen yet.
    expect(fields.some((field) => field.key === "elite_count")).toBe(false);
  });

  it("returns the shared knobs plus the dialect's own", () => {
    const shared = fieldsFor("auto");
    const desma = fieldsFor("desma");

    expect(desma.length).toBeGreaterThan(shared.length);
    expect(desma.some((field) => field.key === "elite_count")).toBe(true);
    // A knob another dialect owns stays out.
    expect(desma.some((field) => field.key === "levy_alpha")).toBe(false);
  });
});

describe("buildMayflyTuningDocument", () => {
  it("sends nothing for an untouched form", () => {
    const result = built("desma", {});

    expect(result.count).toBe(0);
    expect(result.document).toEqual({});
  });

  it("treats an empty string as omitted rather than as zero", () => {
    // The distinction the whole design turns on: nc reserves 0 for "no
    // crossover at all", so an empty field must not become one.
    const result = built("desma", { nc: "", npop: "", g: "" });

    expect(result.count).toBe(0);
    expect(result.document).not.toHaveProperty("nc");
    expect(result.document).not.toHaveProperty("npop");
  });

  it("carries a set knob, and a written zero", () => {
    const result = built("desma", { npop: "40", nc: "0" });

    expect(result.count).toBe(2);
    expect(result.document.npop).toBe(40);
    expect(result.document.nc).toBe(0);
  });

  it("reads each kind as its own type", () => {
    const result = built("gsasma", {
      selection: "rank",
      apply_obl_to_global_best: "true",
      cooling_rate: "0.5",
      nm: "3",
    });

    expect(result.document.selection).toBe("rank");
    expect(result.document.apply_obl_to_global_best).toBe(true);
    expect(result.document.cooling_rate).toBe(0.5);
    expect(result.document.nm).toBe(3);
  });

  it("nests the convergence and schedule keys", () => {
    const result = built("desma", {
      target_cost: "0",
      min_improvement: "1e-6",
      stagnation_iterations: "50",
      min_iterations: "5",
      epochs: "3",
      restarts: "1",
      classify_evals: "400",
      npop: "40",
    });

    expect(result.document.convergence).toEqual({
      target_cost: 0,
      min_improvement: 1e-6,
      stagnation_iterations: 50,
      min_iterations: 5,
    });
    expect(result.document.schedule).toEqual({
      epochs: 3,
      restarts: 1,
      classify_evals: 400,
    });
    // The nested keys never appear at the top level as well.
    expect(result.document).not.toHaveProperty("target_cost");
    expect(result.document).not.toHaveProperty("epochs");
    expect(result.document.npop).toBe(40);
  });

  it("omits an empty convergence or schedule block entirely", () => {
    const result = built("desma", { npop: "40", target_cost: "" });

    expect(result.document).not.toHaveProperty("convergence");
    expect(result.document).not.toHaveProperty("schedule");
  });

  it("writes only the block a set key belongs to", () => {
    const result = built("desma", { epochs: "2" });

    expect(result.document.schedule).toEqual({ epochs: 2 });
    expect(result.document).not.toHaveProperty("convergence");
  });

  it("never carries a knob the given fields do not list", () => {
    // The shared table has no elite_count, so a value left over from a
    // previously selected dialect cannot reach the document.
    const result = built("auto", { elite_count: "4", npop: "40" });

    expect(result.document).not.toHaveProperty("elite_count");
    expect(result.count).toBe(1);
  });

  it("reports a range violation under the knob's own error key", () => {
    // mu is [0,1] inclusive.
    const result = buildMayflyTuningDocument(fieldsFor("desma"), { mu: "2" });

    expect("errors" in result).toBe(true);

    if ("errors" in result) {
      expect(result.errors[tuningErrorKey("mu")]).toContain("at most 1");
    }
  });

  it("refuses a fractional value for an integer knob", () => {
    const result = buildMayflyTuningDocument(fieldsFor("desma"), {
      npop: "4.5",
    });

    expect("errors" in result).toBe(true);

    if ("errors" in result) {
      expect(result.errors[tuningErrorKey("npop")]).toContain("whole number");
    }
  });

  it("honours an exclusive bound", () => {
    // cooling_rate is strictly inside (0,1), so both ends are refused.
    const result = buildMayflyTuningDocument(fieldsFor("gsasma"), {
      cooling_rate: "1",
    });

    expect("errors" in result).toBe(true);

    if ("errors" in result) {
      expect(result.errors[tuningErrorKey("cooling_rate")]).toContain(
        "below 1",
      );
    }
  });
});

describe("parseOptionalInt", () => {
  it("reads an empty field as omitted, not as zero", () => {
    expect(parseOptionalInt("The offspring count", "   ", -1, 4096)).toEqual(
      {},
    );
  });

  it("keeps a written zero, which is a value of its own", () => {
    expect(parseOptionalInt("The offspring count", "0", -1, 4096)).toEqual({
      value: 0,
    });
  });

  it("keeps the -1 that means 'derive it'", () => {
    expect(parseOptionalInt("The offspring count", "-1", -1, 4096)).toEqual({
      value: -1,
    });
  });

  it("refuses a value outside the range", () => {
    const parsed = parseOptionalInt("The offspring count", "-2", -1, 4096);

    expect(parsed).toHaveProperty("error");
  });

  it("refuses a fractional value", () => {
    expect(parseOptionalInt("The offspring count", "1.5", -1, 4096)).toEqual({
      error: "The offspring count must be a whole number.",
    });
  });
});

describe("parseOptionalNumber", () => {
  it("reads an empty field as omitted", () => {
    expect(parseOptionalNumber("The target cost", "", -1e12, 1e12)).toEqual({});
  });

  it("keeps a written zero, which is a usable cost target", () => {
    expect(parseOptionalNumber("The target cost", "0", -1e12, 1e12)).toEqual({
      value: 0,
    });
  });

  it("reads the decimal grammar Go's ParseFloat reads", () => {
    expect(parseOptionalNumber("The target cost", "1e-6", -1e12, 1e12)).toEqual(
      { value: 1e-6 },
    );
    expect(parseOptionalNumber("The target cost", "-.5", -1e12, 1e12)).toEqual({
      value: -0.5,
    });
  });

  it("refuses what Number() would take but ParseFloat would not", () => {
    // Number("0x10") is 16 and Number("Infinity") is finite-looking; neither
    // is a decimal the server would read.
    expect(parseOptionalNumber("The target cost", "0x10", -1e12, 1e12)).toEqual(
      { error: "The target cost must be a number." },
    );
    expect(
      parseOptionalNumber("The target cost", "Infinity", -1e12, 1e12),
    ).toEqual({ error: "The target cost must be a number." });
  });

  it("refuses a value outside the range", () => {
    expect(parseOptionalNumber("The offspring ratio", "-1", 0, 4096)).toEqual({
      error: "The offspring ratio must be in [0, 4096].",
    });
  });
});
