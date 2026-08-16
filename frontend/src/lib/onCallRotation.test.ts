import { describe, it, expect } from "vitest";
import { resolveOnCallSet } from "./onCallRotation";
import type { OnCallParticipant, OnCallWorkingHoursInterval } from "../types/onCallSchedule";

function participant(name: string): OnCallParticipant {
  return { userId: name.toLowerCase(), userName: name };
}

function names(set: OnCallParticipant[]): string[] {
  return set.map((p) => p.userName);
}

describe("resolveOnCallSet", () => {
  it("is empty with no participants", () => {
    const handover = new Date("2026-01-01T09:00:00Z");
    expect(resolveOnCallSet([], handover, 7, 1, "all_day", [], null, new Date(handover.getTime() + 3600_000))).toEqual([]);
  });

  it("is empty before the rotation has started", () => {
    const alice = participant("Alice");
    const handover = new Date("2026-01-01T09:00:00Z");
    expect(resolveOnCallSet([alice], handover, 7, 1, "all_day", [], null, new Date(handover.getTime() - 60_000))).toEqual([]);
  });

  it("keeps a single participant always on", () => {
    const alice = participant("Alice");
    const handover = new Date("2026-01-01T09:00:00Z");
    const thirtyDaysLater = new Date(handover.getTime() + 30 * 24 * 3600_000);
    expect(names(resolveOnCallSet([alice], handover, 7, 1, "all_day", [], null, thirtyDaysLater))).toEqual(["Alice"]);
  });

  it("alternates weekly between two participants", () => {
    const alice = participant("Alice");
    const bob = participant("Bob");
    const participants = [alice, bob];
    const handover = new Date("2026-01-01T09:00:00Z");
    const addDays = (n: number) => new Date(handover.getTime() + n * 24 * 3600_000);

    expect(names(resolveOnCallSet(participants, handover, 7, 1, "all_day", [], null, handover))).toEqual(["Alice"]);
    expect(names(resolveOnCallSet(participants, handover, 7, 1, "all_day", [], null, addDays(3)))).toEqual(["Alice"]);
    expect(names(resolveOnCallSet(participants, handover, 7, 1, "all_day", [], null, addDays(7)))).toEqual(["Bob"]);
    expect(names(resolveOnCallSet(participants, handover, 7, 1, "all_day", [], null, addDays(14)))).toEqual(["Alice"]);
    expect(names(resolveOnCallSet(participants, handover, 7, 1, "all_day", [], null, addDays(35)))).toEqual(["Bob"]);
  });

  it("supports a daily cadence", () => {
    const alice = participant("Alice");
    const bob = participant("Bob");
    const participants = [alice, bob];
    const handover = new Date("2026-01-01T09:00:00Z");
    const addDays = (n: number) => new Date(handover.getTime() + n * 24 * 3600_000);

    expect(names(resolveOnCallSet(participants, handover, 1, 1, "all_day", [], null, addDays(2)))).toEqual(["Alice"]);
    expect(names(resolveOnCallSet(participants, handover, 1, 1, "all_day", [], null, addDays(3)))).toEqual(["Bob"]);
  });

  it("splits concurrent shifts into consecutive groups that alternate", () => {
    const chris = participant("Chris");
    const sam = participant("SamStarling");
    const willis = participant("SamWillis");
    const tom = participant("Tom");
    const participants = [chris, sam, willis, tom];
    const handover = new Date("2026-01-01T09:00:00Z");
    const addDays = (n: number) => new Date(handover.getTime() + n * 24 * 3600_000);

    expect(names(resolveOnCallSet(participants, handover, 7, 2, "all_day", [], null, handover))).toEqual([
      "Chris",
      "SamStarling",
    ]);
    expect(names(resolveOnCallSet(participants, handover, 7, 2, "all_day", [], null, addDays(7)))).toEqual([
      "SamWillis",
      "Tom",
    ]);
    expect(names(resolveOnCallSet(participants, handover, 7, 2, "all_day", [], null, addDays(14)))).toEqual([
      "Chris",
      "SamStarling",
    ]);
  });

  it("leaves the last group smaller when concurrency doesn't divide evenly", () => {
    const alice = participant("Alice");
    const bob = participant("Bob");
    const carol = participant("Carol");
    const participants = [alice, bob, carol];
    const handover = new Date("2026-01-01T09:00:00Z");
    const addDays = (n: number) => new Date(handover.getTime() + n * 24 * 3600_000);

    expect(names(resolveOnCallSet(participants, handover, 7, 2, "all_day", [], null, handover))).toEqual(["Alice", "Bob"]);
    expect(names(resolveOnCallSet(participants, handover, 7, 2, "all_day", [], null, addDays(7)))).toEqual(["Carol"]);
    expect(names(resolveOnCallSet(participants, handover, 7, 2, "all_day", [], null, addDays(14)))).toEqual([
      "Alice",
      "Bob",
    ]);
  });

  it("clamps concurrency to the participant count without duplicating", () => {
    const alice = participant("Alice");
    const handover = new Date("2026-01-01T09:00:00Z");
    expect(names(resolveOnCallSet([alice], handover, 7, 5, "all_day", [], null, handover))).toEqual(["Alice"]);
  });

  describe("working hours gap", () => {
    const alice = participant("Alice");
    const handover = new Date("2026-01-05T00:00:00"); // a Monday, local time
    const businessHours: OnCallWorkingHoursInterval[] = [
      { weekdays: [1, 2, 3, 4, 5], startMinute: 9 * 60, endMinute: 17 * 60 },
    ];

    it("is on call within business hours", () => {
      const monday10am = new Date("2026-01-05T10:00:00");
      expect(names(resolveOnCallSet([alice], handover, 7, 1, "specific_times", businessHours, null, monday10am))).toEqual([
        "Alice",
      ]);
    });

    it("is a gap outside business hours", () => {
      const monday8pm = new Date("2026-01-05T20:00:00");
      expect(resolveOnCallSet([alice], handover, 7, 1, "specific_times", businessHours, null, monday8pm)).toEqual([]);
    });

    it("is a gap on the weekend", () => {
      const saturday10am = new Date("2026-01-10T10:00:00");
      expect(resolveOnCallSet([alice], handover, 7, 1, "specific_times", businessHours, null, saturday10am)).toEqual([]);
    });
  });

  it("lets an override win outright over the computed rotation", () => {
    const alice = participant("Alice");
    const bob = participant("Bob");
    const override = participant("Carol");
    const handover = new Date("2026-01-01T09:00:00Z");
    expect(names(resolveOnCallSet([alice, bob], handover, 7, 1, "all_day", [], override, handover))).toEqual(["Carol"]);
  });

  it("lets an override win even during a working-hours gap", () => {
    const alice = participant("Alice");
    const override = participant("Carol");
    const handover = new Date("2026-01-05T00:00:00");
    const businessHours: OnCallWorkingHoursInterval[] = [{ weekdays: [1], startMinute: 9 * 60, endMinute: 17 * 60 }];
    const outsideHours = new Date("2026-01-05T20:00:00");
    expect(
      names(resolveOnCallSet([alice], handover, 7, 1, "specific_times", businessHours, override, outsideHours)),
    ).toEqual(["Carol"]);
  });
});
