import "@testing-library/jest-dom/vitest";
import { configure } from "@testing-library/react";
import i18n from "../i18n";

// Tests assert on rendered copy, so the language needs to be deterministic
// regardless of the machine/CI runner's navigator.language -- English is
// the language every existing test's assertions were already written
// against.
void i18n.changeLanguage("en");

// Every route in App.tsx is React.lazy now (see App.tsx's code-splitting),
// so any findBy*/waitFor reaching a routed page waits on a real dynamic
// import() resolving on top of whatever async work that page's own effects
// do -- the default 1000ms asyncUtilTimeout is tight enough to flake on a
// loaded CI runner even though nothing is actually broken.
configure({ asyncUtilTimeout: 8000 });
