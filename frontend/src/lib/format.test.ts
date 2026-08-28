import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { formatDateTime, formatDate, formatRelative, formatDuration, shortId } from "./format";

describe("formatDateTime", () => {
  it("returns an em dash for undefined", () => {
    expect(formatDateTime(undefined)).toBe("—");
  });

  it("formats a valid ISO date in the current i18n locale", () => {
    const result = formatDateTime("2026-03-15T10:30:00Z");
    expect(result).not.toBe("—");
    expect(result).toMatch(/2026/);
  });
});

describe("formatDate", () => {
  it("returns an em dash for undefined", () => {
    expect(formatDate(undefined)).toBe("—");
  });

  // The whole point of formatDate (see its doc comment): a UTC-midnight
  // instant must always display as that same calendar date, regardless of
  // the machine's local timezone -- formatDateTime, which reads the
  // viewer's local time instead, would show "27" here for any timezone
  // west of UTC. This is what IOCsModal relies on for identifiedAt.
  it("keeps the UTC calendar date, not the viewer's local one", () => {
    expect(formatDate("2026-08-28T00:00:00Z")).toMatch(/28/);
    expect(formatDate("2026-08-28T00:00:00Z")).not.toMatch(/27/);
  });
});

describe("formatRelative", () => {
  beforeEach(() => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date("2026-01-01T12:00:00Z"));
  });
  afterEach(() => {
    vi.useRealTimers();
  });

  it("returns an em dash for undefined", () => {
    expect(formatRelative(undefined)).toBe("—");
  });

  it("returns 'now' for under a minute ago", () => {
    expect(formatRelative("2026-01-01T11:59:30Z")).toBe("now");
  });

  it("formats minutes ago", () => {
    expect(formatRelative("2026-01-01T11:55:00Z")).toBe("5 minutes ago");
  });

  it("formats hours ago", () => {
    expect(formatRelative("2026-01-01T09:00:00Z")).toBe("3 hours ago");
  });

  it("formats days ago", () => {
    expect(formatRelative("2025-12-29T12:00:00Z")).toBe("3 days ago");
  });
});

describe("formatDuration", () => {
  it("returns an em dash for null/undefined/NaN", () => {
    expect(formatDuration(null)).toBe("—");
    expect(formatDuration(undefined)).toBe("—");
    expect(formatDuration(NaN)).toBe("—");
  });

  it("formats seconds", () => {
    expect(formatDuration(45)).toBe("45s");
  });

  it("formats minutes", () => {
    expect(formatDuration(150)).toBe("3m");
  });

  it("formats hours", () => {
    expect(formatDuration(7200)).toBe("2h");
  });

  it("formats days once past 48 hours", () => {
    expect(formatDuration(60 * 60 * 24 * 3)).toBe("3d");
  });

  it("clamps a negative value to zero rather than going negative", () => {
    expect(formatDuration(-10)).toBe("0s");
  });
});

describe("shortId", () => {
  it("truncates to the first 8 characters", () => {
    expect(shortId("abcdef1234567890")).toBe("abcdef12");
  });

  it("returns the whole string if shorter than 8 chars", () => {
    expect(shortId("abc")).toBe("abc");
  });
});
