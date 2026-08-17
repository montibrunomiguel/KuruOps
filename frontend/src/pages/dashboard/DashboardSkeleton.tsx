import { Skeleton, SkeletonBlock } from "../../components/Skeleton";

// DashboardSkeleton mirrors AlertsTabPanel/IncidentsTabPanel's real layout
// (filter bar -> 4 stat cards -> two 2-column panel pairs -> an activity
// list) so the very first paint of a dashboard tab has real structure
// instead of a blank panel. Shown only while the first-mount fetch is in
// flight -- subsequent filter changes keep using the tab's own inline
// "—" placeholders (see AlertsTabPanel's statsLoading usage), this is
// purely for cold load.
export function DashboardSkeleton() {
  return (
    <SkeletonBlock>
      <div className="filter-bar">
        <Skeleton width={140} height={32} radius={7} />
        <Skeleton width={140} height={32} radius={7} />
        <Skeleton width={160} height={32} radius={7} />
        <Skeleton width={120} height={32} radius={7} />
        <Skeleton width={180} height={32} radius={7} />
      </div>

      <div className="stat-grid" data-cols="4">
        {[0, 1, 2, 3].map((i) => (
          <div className="stat-card" key={i}>
            <div className="stat-card-head">
              <Skeleton width="60%" height={11} />
              <Skeleton width={26} height={26} radius={7} />
            </div>
            <Skeleton width="40%" height={22} />
          </div>
        ))}
      </div>

      <div className="dashboard-grid-2">
        <div className="panel skeleton-stack">
          <Skeleton width="35%" height={14} />
          <Skeleton width="100%" height={140} radius={8} />
        </div>
        <div className="panel skeleton-stack">
          <Skeleton width="45%" height={14} />
          {[0, 1, 2].map((i) => (
            <Skeleton key={i} width="100%" height={30} radius={6} />
          ))}
        </div>
      </div>

      <div className="dashboard-grid-2">
        <div className="panel skeleton-stack">
          <Skeleton width="40%" height={14} />
          {[0, 1, 2].map((i) => (
            <Skeleton key={i} width="100%" height={30} radius={6} />
          ))}
        </div>
        <div className="panel skeleton-stack">
          <Skeleton width="50%" height={14} />
          <Skeleton width="100%" height={140} radius={8} />
        </div>
      </div>

      <div className="panel skeleton-stack">
        <Skeleton width="30%" height={14} />
        {[0, 1, 2].map((i) => (
          <div className="skeleton-row" key={i}>
            <Skeleton width={32} height={32} radius={8} />
            <Skeleton width="70%" height={13} />
          </div>
        ))}
      </div>
    </SkeletonBlock>
  );
}
