import { useCityCalendar } from '../hooks/useCityCalendar';
import EventCard from './EventCard';
import { LazyList } from './LazyList';
import { SkeletonEventCards } from './SkeletonEventCards';
import * as cal from '../pages/CalendarPage.css';
import * as s from '../pages/VenuesPage.css';

type Props = {
  cityId?: string;
  venueId: string;
  venueName: string;
};

// One venue's upcoming events, paged from the city calendar by venue_id.
export const VenueEvents = ({ cityId, venueId, venueName }: Props) => {
  const { data, fetchNextPage, hasNextPage, isLoading, isError, isPlaceholderData } =
    useCityCalendar(cityId, venueId);
  const events = data?.pages.flatMap((p) => p.events) ?? [];

  // useCityCalendar keeps the previous venue's pages as placeholder data while
  // the new venue loads; showing them under the new venue's name would be wrong.
  return isLoading || isPlaceholderData ? (
    <SkeletonEventCards count={3} />
  ) : isError ? (
    <div className={cal.errorBox}>Couldn't load shows at {venueName}, please try again.</div>
  ) : events.length === 0 ? (
    <div className={s.emptyState}>No upcoming shows at {venueName}.</div>
  ) : (
    <LazyList fetchNextPage={fetchNextPage} hasNextPage={hasNextPage}>
      <ul className={cal.list}>
        {events.map((e) => (
          <li key={e.id} className={cal.listItem}>
            <EventCard event={e} interactive />
          </li>
        ))}
      </ul>
    </LazyList>
  );
};
