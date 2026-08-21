import { Skeleton, SkeletonBlock } from "./Skeleton";

// AlertDetailSkeleton mirrors AlertDetailPage/IncidentDetailPage's real
// detail-header + two-column detail-layout shape (see either page's own
// `if (loading) return ...` gate) -- swapped in instead of a bare "Loading…"
// so the cold-load moment already looks like the page that's about to
// render.
export function AlertDetailSkeleton() {
  return (
    <SkeletonBlock>
      <div className="detail-header">
        <div className="skeleton-stack">
          <div className="skeleton-row">
            <Skeleton width={70} height={12} />
            <Skeleton width={60} height={18} radius={9} />
            <Skeleton width={70} height={18} radius={9} />
          </div>
          <Skeleton width={320} height={22} />
          <Skeleton width={220} height={13} />
        </div>
        <div className="skeleton-row">
          <Skeleton width={110} height={30} radius={7} />
          <Skeleton width={90} height={30} radius={7} />
        </div>
      </div>

      <div className="detail-layout">
        <div className="detail-main skeleton-stack">
          <div className="panel skeleton-stack">
            <Skeleton width="30%" height={14} />
            <Skeleton width="100%" height={13} />
            <Skeleton width="80%" height={13} />
          </div>
          <div className="panel skeleton-stack">
            <Skeleton width="30%" height={14} />
            <Skeleton width="100%" height={80} radius={8} />
          </div>
          <div className="panel skeleton-stack">
            <Skeleton width="30%" height={14} />
            <Skeleton width="100%" height={13} />
          </div>
        </div>

        <div className="detail-side skeleton-stack">
          <div className="panel skeleton-stack">
            <Skeleton width="50%" height={14} />
            <Skeleton width="100%" height={32} radius={7} />
          </div>
          <div className="panel skeleton-stack">
            <Skeleton width="50%" height={14} />
            <Skeleton width="100%" height={13} />
            <Skeleton width="70%" height={13} />
          </div>
        </div>
      </div>
    </SkeletonBlock>
  );
}
