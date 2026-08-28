import { describe, it, expect, afterEach } from "vitest";
import i18n, { setLanguage } from "./index";

describe("setLanguage", () => {
  afterEach(() => {
    // Restore the deterministic language every other test file's
    // assertions are written against -- see src/test/setup.ts.
    void i18n.changeLanguage("en");
    localStorage.removeItem("kuruops.language");
  });

  it("changes the active i18next language", async () => {
    setLanguage("pt");
    await new Promise((r) => setTimeout(r, 0));
    expect(i18n.language).toBe("pt");
  });

  it("persists the choice to localStorage so a reload keeps it", () => {
    setLanguage("pt");
    expect(localStorage.getItem("kuruops.language")).toBe("pt");

    setLanguage("en");
    expect(localStorage.getItem("kuruops.language")).toBe("en");
  });
});
