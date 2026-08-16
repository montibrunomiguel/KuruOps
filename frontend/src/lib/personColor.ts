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
