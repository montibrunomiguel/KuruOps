import type { CSSProperties, ReactNode } from "react";
import { useTranslation } from "react-i18next";

// Skeleton is a shimmering placeholder bar for content that's still
// loading -- swapped in ahead of the real layout so the first paint has
// *some* structure instead of a blank panel or a single "…" (see
// App.tsx's RouteFallback, DashboardSkeleton, AlertDetailSkeleton). Purely
// decorative (aria-hidden): the container that renders one or more of these
// is what should carry aria-busy + a visually-hidden loading label, not
// each individual bar -- see SkeletonBlock below.
export function Skeleton({ width = "100%", height = 14, radius = 6 }: { width?: number | string; height?: number; radius?: number }) {
  return (
    <span
      aria-hidden="true"
      className="skeleton-bar"
      style={{ width, height, borderRadius: radius }}
    />
  );
}

// SkeletonBlock wraps a group of Skeleton bars (or any placeholder markup)
// with the aria-busy + visually-hidden label a screen reader needs -- the
// individual bars themselves stay aria-hidden since they carry no
// information beyond "something is loading here".
export function SkeletonBlock({
  children,
  className,
  style,
}: {
  children: ReactNode;
  className?: string;
  style?: CSSProperties;
}) {
  const { t } = useTranslation();
  return (
    <div aria-busy="true" className={className} style={style}>
      <span className="visually-hidden">{t("common.loading")}</span>
      {children}
    </div>
  );
}
