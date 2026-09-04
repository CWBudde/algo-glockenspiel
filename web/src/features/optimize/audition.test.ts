import { describe, expect, it } from "vitest";

import { auditionAppliesToActiveJob } from "./Audition";

/**
 * A pure-function test, in the same style as mayflyTuning.test.ts and
 * runList.test.ts: web/package.json carries vitest but not
 * @testing-library/react, so this checks the predicate Audition gates its
 * controls on rather than rendering the component.
 */
describe("auditionAppliesToActiveJob", () => {
  it("applies when the displayed job is the active one", () => {
    expect(auditionAppliesToActiveJob("fit-7", "fit-7")).toBe(true);
  });

  it("does not apply to a different, historical job", () => {
    expect(auditionAppliesToActiveJob("fit-3", "fit-7")).toBe(false);
  });

  it("does not apply when nothing is displayed", () => {
    expect(auditionAppliesToActiveJob(null, "fit-7")).toBe(false);
  });

  it("does not apply when nothing is active either", () => {
    expect(auditionAppliesToActiveJob(null, null)).toBe(false);
  });
});
