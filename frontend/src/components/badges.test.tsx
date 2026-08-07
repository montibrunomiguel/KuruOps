import { describe, it, expect } from "vitest";
import { render, screen } from "@testing-library/react";
import {
  SeverityBadge,
  AlertStatusBadge,
  ClassificationBadge,
  PriorityBadge,
  PhasePill,
} from "./badges";

describe("SeverityBadge", () => {
  it("renders the label and severity-specific class for each severity", () => {
    const cases: Array<[string, string]> = [
      ["critical", "Critical"],
      ["high", "High"],
      ["medium", "Medium"],
      ["low", "Low"],
      ["informational", "Informational"],
    ];
    for (const [severity, label] of cases) {
      const { unmount } = render(<SeverityBadge severity={severity as never} />);
      expect(screen.getByText(label)).toBeInTheDocument();
      expect(screen.getByText(label).closest(".badge")).toHaveClass(`badge-sev-${severity === "informational" ? "info" : severity}`);
      unmount();
    }
  });
});

describe("AlertStatusBadge", () => {
  it("renders the correct label per status", () => {
    render(<AlertStatusBadge status="investigating" />);
    expect(screen.getByText("Investigating")).toBeInTheDocument();
  });
});

describe("ClassificationBadge", () => {
  it("renders the correct label per classification", () => {
    render(<ClassificationBadge classification="false_positive" />);
    expect(screen.getByText("False Positive")).toBeInTheDocument();
  });
});

describe("PriorityBadge", () => {
  it("upper-cases the priority", () => {
    render(<PriorityBadge priority="p1" />);
    expect(screen.getByText("P1")).toBeInTheDocument();
  });
});

describe("PhasePill", () => {
  it("renders the mapped phase label", () => {
    render(<PhasePill phase="detection_analysis" />);
    expect(screen.getByText(/detec/i)).toBeInTheDocument();
  });
});
