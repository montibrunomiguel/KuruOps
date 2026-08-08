import { describe, it, expect, vi, afterEach } from "vitest";
import { timeRangeParams, EMPTY_TIME_RANGE } from "./TimeRangeFilter";

describe("timeRangeParams", () => {
  afterEach(() => {
    vi.useRealTimers();
  });

  it("returns no since/until for the 'Any time' preset", () => {
    expect(timeRangeParams(EMPTY_TIME_RANGE)).toEqual({});
  });

  it("computes an ISO since timestamp N hours before now for each preset, no until", () => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date("2026-01-15T12:00:00.000Z"));

    expect(timeRangeParams({ preset: "24h", from: "", to: "" })).toEqual({ since: "2026-01-14T12:00:00.000Z" });
    expect(timeRangeParams({ preset: "7d", from: "", to: "" })).toEqual({ since: "2026-01-08T12:00:00.000Z" });
    expect(timeRangeParams({ preset: "30d", from: "", to: "" })).toEqual({ since: "2025-12-16T12:00:00.000Z" });
    expect(timeRangeParams({ preset: "90d", from: "", to: "" })).toEqual({ since: "2025-10-17T12:00:00.000Z" });
  });

  it("converts custom from/to datetime-local values to ISO instants", () => {
    const result = timeRangeParams({ preset: "custom", from: "2026-01-01T09:00", to: "2026-01-02T18:30" });
    expect(result.since).toBe(new Date("2026-01-01T09:00").toISOString());
    expect(result.until).toBe(new Date("2026-01-02T18:30").toISOString());
  });

  it("a custom range with only 'from' set omits until", () => {
    const result = timeRangeParams({ preset: "custom", from: "2026-01-01T09:00", to: "" });
    expect(result.since).toBe(new Date("2026-01-01T09:00").toISOString());
    expect(result.until).toBeUndefined();
  });

  it("a custom range with only 'to' set omits since", () => {
    const result = timeRangeParams({ preset: "custom", from: "", to: "2026-01-02T18:30" });
    expect(result.since).toBeUndefined();
    expect(result.until).toBe(new Date("2026-01-02T18:30").toISOString());
  });

  it("an empty custom range omits both", () => {
    expect(timeRangeParams({ preset: "custom", from: "", to: "" })).toEqual({});
  });
});
