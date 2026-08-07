import { describe, it, expect } from "vitest";
import { render } from "@testing-library/react";
import {
  DashboardIcon,
  AlertIcon,
  ShieldIcon,
  PlaybookIcon,
  GearIcon,
  ClockIcon,
  CheckIcon,
  LinkIcon,
  CommentIcon,
  PaperclipIcon,
} from "./icons";

const icons = {
  DashboardIcon,
  AlertIcon,
  ShieldIcon,
  PlaybookIcon,
  GearIcon,
  ClockIcon,
  CheckIcon,
  LinkIcon,
  CommentIcon,
  PaperclipIcon,
};

describe("icons", () => {
  for (const [name, Icon] of Object.entries(icons)) {
    it(`${name} renders an 18x18 svg`, () => {
      const { container } = render(<Icon />);
      const svg = container.querySelector("svg");
      expect(svg).toBeInTheDocument();
      expect(svg).toHaveAttribute("width", "18");
      expect(svg).toHaveAttribute("height", "18");
    });
  }

  it("forwards extra props (e.g. className) onto the svg element", () => {
    const { container } = render(<AlertIcon className="custom-class" />);
    expect(container.querySelector("svg")).toHaveClass("custom-class");
  });
});
