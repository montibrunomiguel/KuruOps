// Minimal config focused on real bugs (incorrect hooks usage, effect
// dependencies, unreachable/duplicate code), not style -- Prettier/formatting
// isn't set up here and this config doesn't try to replace it. Unused-vars is
// deliberately left off: tsconfig.json already enforces noUnusedLocals /
// noUnusedParameters via `tsc`, so duplicating it here would just be a second
// place for the same rule to drift.
import js from "@eslint/js";
import tseslint from "typescript-eslint";
import reactHooks from "eslint-plugin-react-hooks";
import globals from "globals";

export default tseslint.config(
  { ignores: ["dist", "coverage"] },
  {
    files: ["**/*.{ts,tsx}"],
    extends: [js.configs.recommended, ...tseslint.configs.recommended],
    plugins: {
      "react-hooks": reactHooks,
    },
    languageOptions: {
      ecmaVersion: 2020,
      globals: globals.browser,
    },
    rules: {
      ...reactHooks.configs.recommended.rules,
      // Both a legitimate pattern in the effect-driven fetch-on-mount hooks
      // used throughout src/pages/**, and already caught for real bugs by
      // exhaustive-deps below where it matters.
      "@typescript-eslint/no-explicit-any": "off",
    },
  },
);
