import { useCityCalendar } from '../hooks/useCityCalendar';
import EventCard from '../components/EventCard';
import { useAuth } from '../auth/useAuth';
import { SkeletonEventCards } from './SkeletonEventCards';
import * as s from '../pages/CalendarPage.css';
import clsx from 'clsx';
import { LazyList } from './LazyList';

type Props = {
  gatePending: boolean;
  displayStyle: 'Full' | 'Condensed';
};

export const CalendarEventsAllCity = ({ gatePending, displayStyle }: Props) => {
  const { user } = useAuth();
  const { data, fetchNextPage, hasNextPage, isLoading, isError } = useCityCalendar(user?.city_id);
  const events = data?.pages.map((p) => p.events).flat() || [];
  const loading = gatePending || isLoading;
  const errored = isError;

  return loading ? (
    <SkeletonEventCards count={5} />
  ) : errored ? (
    <div className={s.errorBox}>Couldn't load your calendar, please try again.</div>
  ) : events.length === 0 ? (
    <div className={s.emptyState}>Nothing on the calendar in Seattle right now.</div>
  ) : (
    <LazyList fetchNextPage={fetchNextPage} hasNextPage={hasNextPage}>
      <ul
        className={clsx(s.list, {
          [s.listCondensed]: displayStyle === 'Condensed',
        })}
      >
        {events.map((e) => (
          <li
            key={e.id}
            className={clsx(s.listItem, {
              [s.listItemCondensed]: displayStyle === 'Condensed',
            })}
          >
            <EventCard event={e} interactive shorterMinHeight />
          </li>
        ))}
      </ul>
    </LazyList>
  );
};
