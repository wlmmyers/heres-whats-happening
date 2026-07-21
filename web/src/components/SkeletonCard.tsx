import { Skeleton } from './Skeleton';
import * as s from './SkeletonCard.css';

export default function SkeletonCard({ height = 150 }: { height?: number }) {
  return (
    <div className={s.skeletonCard} style={{ height }}>
      <Skeleton type="fill" />
    </div>
  );
}
