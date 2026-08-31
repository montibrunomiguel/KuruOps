import { describe, it, expect } from "vitest";
import { personColor, personTextColor } from "./personColor";

// The actual PALETTE lives in personColor.ts (not re-exported) -- these
// values are copied here deliberately, so a change to the palette's
// contents makes this test fail loudly instead of silently drifting out of
// sync with what's actually shipped.
const PALETTE = [
  "#4f8cff",
  "#f2994a",
  "#27ae60",
  "#eb5757",
  "#9b51e0",
  "#2d9cdb",
  "#f2c94c",
  "#56ccf2",
  "#bb6bd9",
  "#219653",
];

describe("personColor", () => {
  it("is deterministic -- the same id always maps to the same color", () => {
    expect(personColor("user-123")).toBe(personColor("user-123"));
  });

  it("only ever returns a color from the palette", () => {
    for (const id of ["a", "bb", "user-1", "9249d187-91d7-4f82-ba4a-222f9f5ea8b3", ""]) {
      expect(PALETTE).toContain(personColor(id));
    }
  });

  it("different ids can map to different colors", () => {
    const colors = new Set(["u1", "u2", "u3", "u4", "u5"].map(personColor));
    expect(colors.size).toBeGreaterThan(1);
  });
});

describe("personTextColor", () => {
  it("picks black for every color in the palette, matching what WCAG AA actually requires here", () => {
    // Every PALETTE color fails AA (4.5:1) with white text except the
    // purple (#9b51e0, ~4.52:1) -- black clears 4.5:1 against all ten
    // (worst case 4.65:1, also the purple). So today's PALETTE, correctly
    // computed, is "always black" -- this test exists to catch a
    // regression (e.g. the NaN-from-3-digit-hex bug this function
    // previously had, which made it always return white instead) via an
    // exact per-color assertion, not a loose "black or white" check.
    for (const color of PALETTE) {
      expect(personTextColor(color)).toBe("#000");
    }
  });

  it("picks white text on a background dark enough that white wins", () => {
    expect(personTextColor("#000000")).toBe("#fff");
    expect(personTextColor("#0a0a2a")).toBe("#fff");
  });

  it("picks black text on a background light enough that black wins", () => {
    expect(personTextColor("#ffffff")).toBe("#000");
    expect(personTextColor("#f5f5f0")).toBe("#000");
  });
});
