import { useEffect, useState } from "react";
import { getStoredTheme, onThemeChange } from "../theme";

// The shield/eye mark from the project logo, square-cropped for use
// anywhere the old "A" letter placeholder used to sit (sidebar, login).
// Served from /public so it's a plain static asset, not bundled. Two crops
// exist -- logo-mark-light.png/logo-mark-dark.png -- because the source
// artwork was drawn on a matching light/dark background per theme; this
// swaps between them live so a BrandMark already on screen (e.g. in
// Sidebar) updates the instant the theme toggle fires, not just on reload.
export function BrandMark() {
  const [theme, setTheme] = useState(getStoredTheme());

  useEffect(() => onThemeChange(setTheme), []);

  const src = theme === "light" ? "/logo-mark-light.png" : "/logo-mark-dark.png";

  return (
    <div className="sidebar-brand-mark">
      <img src={src} alt="KuruOps" />
    </div>
  );
}
