import SkeletonCard from './SkeletonCard';
import * as calendarPageStyles from '../pages/CalendarPage.css';

type Props = {
  count: number;
};

export const SkeletonEventCards = ({ count }: Props) => (
  <ul className={calendarPageStyles.list}>
    {Array.from({ length: count }, (_, i) => ({ id: `loading-${i}` })).map((e) => (
      <li key={e.id} className={calendarPageStyles.listItem}>
        <SkeletonCard height={150} />
      </li>
    ))}
  </ul>
);
