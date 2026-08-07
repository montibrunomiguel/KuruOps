import "@testing-library/jest-dom/vitest";
import i18n from "../i18n";

// Tests assert on rendered copy, so the language needs to be deterministic
// regardless of the machine/CI runner's navigator.language -- English is
// the language every existing test's assertions were already written
// against.
void i18n.changeLanguage("en");
