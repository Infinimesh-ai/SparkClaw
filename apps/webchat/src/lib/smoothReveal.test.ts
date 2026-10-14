import { describe, expect, it } from "vitest";
import { revealArrival, revealState, revealStep } from "./smoothReveal";

describe("smooth progress reveal", () => {
  it("spreads a new suffix evenly over the observed arrival interval", () => {
    const state = revealState("");
    const first = "a".repeat(30);
    const speed = revealArrival(state, first, 1000);
    expect(speed).toBeCloseTo(30 / 300);
    expect(revealStep(state, first, speed, 100)).toBe(true);
    expect(state.shown).toHaveLength(10);
    expect(revealStep(state, first, speed, 200)).toBe(false);
    expect(state.shown).toBe(first);

    const second = first + "b".repeat(50);
    expect(revealArrival(state, second, 1500)).toBeCloseTo(50 / 400);
  });

  it("keeps unrevealed text from the previous snapshot in the next run", () => {
    const state = revealState("");
    const speed = revealArrival(state, "abcdef", 0);
    revealStep(state, "abcdef", speed, 100);
    expect(state.shown).toBe("ab");
    expect(revealArrival(state, "abcdefgh", 300)).toBeCloseTo(6 / 300);
  });

  it("shows a non-continuing snapshot immediately", () => {
    const state = revealState("Old preview");
    expect(revealArrival(state, "Different", 10)).toBe(0);
    expect(state.shown).toBe("Different");
  });

  it("always advances and never splits a surrogate pair", () => {
    const state = revealState("");
    const target = "😀😀";
    const speed = revealArrival(state, target, 0);
    revealStep(state, target, speed, 0);
    expect(state.shown).toBe("😀");
    revealStep(state, target, speed, 0);
    revealStep(state, target, speed, 0);
    expect(state.shown).toBe(target);
  });
});
