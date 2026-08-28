// Toggles the [data-theme] attribute that styles/tokens.css keys its
// dark/light custom property overrides off of. Dark is the app's default
// (":root" alone already matches dark, see tokens.css), so an unset/missing
// stored preference should read as dark, not light.
const STORAGE_KEY = "kuruops.theme";
const THEME_CHANGE_EVENT = "kuruops:theme-change";

export type Theme = "dark" | "light";

export function getStoredTheme(): Theme {
  return localStorage.getItem(STORAGE_KEY) === "light" ? "light" : "dark";
}

export function applyTheme(theme: Theme) {
  document.documentElement.dataset.theme = theme;
  localStorage.setItem(STORAGE_KEY, theme);
  window.dispatchEvent(new CustomEvent(THEME_CHANGE_EVENT, { detail: theme }));
}

// onThemeChange lets components mounted outside Sidebar (which owns the
// toggle button) react live when the theme flips, instead of only picking up
// getStoredTheme() once on mount -- e.g. BrandMark swapping its icon asset.
export function onThemeChange(handler: (theme: Theme) => void): () => void {
  const listener = (e: Event) => handler((e as CustomEvent<Theme>).detail);
  window.addEventListener(THEME_CHANGE_EVENT, listener);
  return () => window.removeEventListener(THEME_CHANGE_EVENT, listener);
}
