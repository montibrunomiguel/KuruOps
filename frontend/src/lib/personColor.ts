// Deterministic hash -> palette color for tagging a person consistently
// across the on-call responder list and timeline (same person, same color,
// every render, with no server-assigned color to persist). Palette picked
// for pairwise contrast against both light/dark backgrounds -- see
// styles/tokens.css for the app's existing severity/status colors, which
// this deliberately doesn't reuse (those carry their own meaning).
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

export function personColor(id: string): string {
  let hash = 0;
  for (let i = 0; i < id.length; i++) {
    hash = (hash * 31 + id.charCodeAt(i)) >>> 0;
  }
  return PALETTE[hash % PALETTE.length];
}

// relativeLuminance implements the WCAG 2 formula (sRGB -> linear -> the
// 0.2126/0.7152/0.0722 weighted sum) that contrastRatio and
// personTextColor below are both built on.
function relativeLuminance(hex: string): number {
  const r = parseInt(hex.slice(1, 3), 16) / 255;
  const g = parseInt(hex.slice(3, 5), 16) / 255;
  const b = parseInt(hex.slice(5, 7), 16) / 255;
  const linear = (c: number) => (c <= 0.03928 ? c / 12.92 : Math.pow((c + 0.055) / 1.055, 2.4));
  return 0.2126 * linear(r) + 0.7152 * linear(g) + 0.0722 * linear(b);
}

function contrastRatio(hexA: string, hexB: string): number {
  const lA = relativeLuminance(hexA);
  const lB = relativeLuminance(hexB);
  const lighter = Math.max(lA, lB) + 0.05;
  const darker = Math.min(lA, lB) + 0.05;
  return lighter / darker;
}

// personTextColor picks black or white -- whichever gives a higher-contrast
// (never a fixed, always-lower one) reading of the person's name label
// rendered on top of a personColor() swatch. Checked against this exact
// PALETTE: white text fails WCAG AA's 4.5:1 small-text threshold against 9
// of these 10 colors (as low as 1.59:1 on the yellow); black clears 4.5:1
// against every one of them (4.65:1 at its worst, on the purple). A fixed
// "always black" constant would happen to pass today, but computing it per
// swatch means a future PALETTE addition can't silently reintroduce this
// same failure the way the original `color: "#fff"` did.
export function personTextColor(background: string): "#000" | "#fff" {
  // Full 6-digit hex, not the "#000"/"#fff" shorthand -- relativeLuminance
  // only parses 6-digit hex (hex.slice(1,3)/(3,5)/(5,7)), and a 3-digit
  // shorthand silently parses as NaN there rather than throwing, which
  // made contrastRatio NaN and every ">=" comparison against it false --
  // i.e. this function always returned "#fff" regardless of background,
  // silently defeating the whole fix. Caught by
  // OnCallTimeline.test.tsx asserting the exact expected color, not just
  // "black or white".
  return contrastRatio(background, "#000000") >= contrastRatio(background, "#ffffff") ? "#000" : "#fff";
}
