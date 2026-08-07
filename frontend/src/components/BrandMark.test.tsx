import { describe, it, expect, beforeEach } from "vitest";
import { render, screen } from "@testing-library/react";
import { act } from "react";
import { BrandMark } from "./BrandMark";
import { applyTheme } from "../theme";

describe("BrandMark", () => {
  beforeEach(() => {
    localStorage.clear();
  });

  it("renders the dark-theme mark by default (unset theme reads as dark)", () => {
    render(<BrandMark />);
    const img = screen.getByAltText("ArgusOps");
    expect(img).toHaveAttribute("src", "/logo-mark-dark.png");
  });

  it("renders the light-theme mark when the stored theme is light", () => {
    localStorage.setItem("argusops.theme", "light");
    render(<BrandMark />);
    expect(screen.getByAltText("ArgusOps")).toHaveAttribute("src", "/logo-mark-light.png");
  });

  it("swaps live when the theme changes after mount", () => {
    render(<BrandMark />);
    expect(screen.getByAltText("ArgusOps")).toHaveAttribute("src", "/logo-mark-dark.png");

    act(() => applyTheme("light"));
    expect(screen.getByAltText("ArgusOps")).toHaveAttribute("src", "/logo-mark-light.png");
  });
});
