import { describe, it, expect } from "vitest";
import i18n from "../i18n";
import { describeActivity } from "./activityText";
import type { ActivityEvent } from "../types/dashboard";

function eventFixture(overrides: Partial<ActivityEvent> = {}): ActivityEvent {
  return {
    kind: "alert",
    contextId: "a1b2c3d4-0000-0000-0000-000000000000",
    contextTitle: "Suspicious login",
    eventType: "received",
    actorType: "system",
    data: {},
    createdAt: "2026-01-01T00:00:00Z",
    ...overrides,
  };
}

describe("describeActivity", () => {
  it("describes an alert received event using the source as the actor", () => {
    const result = describeActivity(eventFixture({ eventType: "received", contextTitle: "Wazuh" }), i18n.t);
    expect(result.text).toContain("Wazuh");
    expect(result.tone).toBe("tone-accent");
  });

  it("describes an alert escalated event, pulling the incident id out of data", () => {
    const result = describeActivity(
      eventFixture({ eventType: "escalated", data: { incidentId: "c8d4a2f6-0000-0000-0000-000000000000" } }),
      i18n.t,
    );
    expect(result.text).toContain("c8d4a2f6");
    expect(result.tone).toBe("tone-critical");
  });

  it("describes an alert closed event, including the classification", () => {
    const result = describeActivity(eventFixture({ eventType: "closed", data: { classification: "true_positive" } }), i18n.t);
    expect(result.text).toContain("true positive");
    expect(result.tone).toBe("tone-success");
  });

  it("falls back to a readable event-type string for an unknown alert event type", () => {
    const result = describeActivity(eventFixture({ eventType: "something_new" }), i18n.t);
    expect(result.text).toContain("something new");
  });

  it("describes an incident created event", () => {
    const result = describeActivity(eventFixture({ kind: "incident", eventType: "created", contextId: "c8d4a2f6-0000-0000-0000-000000000000" }), i18n.t);
    expect(result.text).toContain("c8d4a2f6");
  });

  it("describes an incident phase_skipped event with the high tone", () => {
    const result = describeActivity(eventFixture({ kind: "incident", eventType: "phase_skipped" }), i18n.t);
    expect(result.tone).toBe("tone-high");
  });

  it("describes an incident alert_linked event, pulling the alert id out of data", () => {
    const result = describeActivity(
      eventFixture({ kind: "incident", eventType: "alert_linked", data: { alertId: "b8e21d4a-0000-0000-0000-000000000000" } }),
      i18n.t,
    );
    expect(result.text).toContain("b8e21d4a");
  });

  it("falls back to a readable event-type string for an unknown incident event type", () => {
    const result = describeActivity(eventFixture({ kind: "incident", eventType: "something_new" }), i18n.t);
    expect(result.text).toContain("something new");
  });
});
